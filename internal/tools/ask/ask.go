// Package ask implements the ask tool: the model asks the user up to
// 3 questions mid-run, each with options or free text. Headless
// transport is stdin/stdout; the TUI replaces it via Ask.
package ask

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/muesli/cancelreader"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Input owns a single CLI invocation's reader. Cancellation closes only this
// explicitly owned input; the close callback settles before ReadLine returns.
type Input struct {
	in        io.ReadCloser
	rd        *bufio.Reader
	gate      chan struct{}
	cancel    func() bool
	closeOnce sync.Once
	closeErr  error
}

func NewInput(in io.ReadCloser) *Input {
	return &Input{in: in, rd: bufio.NewReader(in), gate: make(chan struct{}, 1)}
}

// NewFileInput owns cancellable reads for this invocation, without changing
// stdin flags or closing the caller's file. Only its owned handles close.
func NewFileInput(in *os.File) (*Input, error) {
	info, err := in.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect input: %w", err)
	}
	if info.Mode()&os.ModeNamedPipe != 0 {
		if input, err := ownedPipeInput(in); input != nil || err != nil {
			return input, err
		}
	}
	var source io.Reader = in
	null, nullErr := os.Stat(os.DevNull)
	if info.Mode().IsRegular() || (nullErr == nil && os.SameFile(info, null)) {
		// Files and the null device do not wait for terminal input, and Linux
		// cannot poll them with epoll. Keep the existing reader fallback.
		source = struct{ io.Reader }{in}
	}
	rd, err := cancelreader.NewReader(source)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare cancellable input: %w", err)
	}
	input := NewInput(rd)
	input.cancel = rd.Cancel
	return input, nil
}
func (i *Input) Close() error {
	i.closeOnce.Do(func() { i.closeErr = i.in.Close() })
	return i.closeErr
}

func (i *Input) Read(p []byte) (int, error) { return i.rd.Read(p) }
func (i *Input) ReadLine(ctx context.Context) (string, error) {
	select {
	case i.gate <- struct{}{}:
		defer func() { <-i.gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		if i.cancel != nil {
			i.cancel()
		} else {
			_ = i.Close()
		}
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	line, err := i.rd.ReadString('\n')
	if ctx.Err() != nil {
		return line, ctx.Err()
	}
	return line, err
}

// Lines preserves buffering across a prompt. Borrowed blocking readers must
// supply ReadLine(context.Context); they are never closed or abandoned.
func Lines(in io.Reader) func(context.Context) (string, error) {
	if rd, ok := in.(interface {
		ReadLine(context.Context) (string, error)
	}); ok {
		return rd.ReadLine
	}
	rd := bufio.NewReader(in)
	_, memoryString := in.(*strings.Reader)
	_, memoryBytes := in.(*bytes.Reader)
	_, memoryBuffer := in.(*bytes.Buffer)
	return func(ctx context.Context) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if ctx.Done() != nil && !memoryString && !memoryBytes && !memoryBuffer {
			return "", fmt.Errorf("cancellable prompt requires explicitly owned input or a context-aware reader")
		}
		return rd.ReadString('\n')
	}
}

// NoneOfAbove is appended to every option list.
const NoneOfAbove = "None of the above"

// MaxOptions caps model-provided options per question; excess choices refuse.
const MaxOptions = 3

// Question is one user question: free text when Options is empty.
type Question struct {
	Text    string
	Options []string
}

// Answer is one user's response: the pick plus an optional comment.
type Answer struct {
	Picked  string
	Comment string
}

// Querier answers questions: the TUI dialog or the stdin prompter.
type Querier func(ctx context.Context, qs []Question) ([]Answer, error)

// Prompter asks on Out and reads from In, unless Ask answers instead.
type Prompter struct {
	In  io.Reader
	Out io.Writer
	// Ask answers instead of stdin when set (TUI transport).
	Ask Querier
}

// AskQuestions answers through Ask, or stdin when unset.
func (p *Prompter) AskQuestions(ctx context.Context, qs []Question) ([]Answer, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if p.Ask != nil {
		return p.Ask(ctx, qs)
	}
	return p.stdinAsk(ctx, qs)
}

// stdinAsk walks the questions on In/Out: numbered options with a
// typed pick, an optional comment, or a free-text answer.
func (p *Prompter) stdinAsk(ctx context.Context, qs []Question) ([]Answer, error) {
	read := Lines(p.In)
	line := func() (string, error) {
		s, err := read(ctx)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil && len(s) == 0 {
			return "", fmt.Errorf("cannot read answer: %w", err)
		}
		return strings.TrimSuffix(strings.TrimSuffix(s, "\r\n"), "\n"), nil
	}
	out := make([]Answer, 0, len(qs))
	for i, q := range qs {
		head := fmt.Sprintf("Q%d/%d: %s\n", i+1, len(qs), q.Text)
		if len(q.Options) == 0 {
			fmt.Fprintf(p.Out, "%sanswer: ", head)
			a, err := line()
			if err != nil {
				return nil, err
			}
			out = append(out, Answer{Picked: a})
			continue
		}
		fmt.Fprint(p.Out, head)
		for n, o := range q.Options {
			fmt.Fprintf(p.Out, "  %d) %s\n", n+1, o)
		}
		fmt.Fprint(p.Out, "pick 1-N or type answer: ")
		pick, err := line()
		if err != nil {
			return nil, err
		}
		if n, err := strconv.Atoi(strings.TrimSpace(pick)); err == nil && n >= 1 && n <= len(q.Options) {
			pick = q.Options[n-1]
		}
		fmt.Fprint(p.Out, "comment (optional): ")
		comment, err := line()
		if err != nil {
			return nil, err
		}
		out = append(out, Answer{Picked: pick, Comment: comment})
	}
	return out, nil
}

// parseOptions splits comma-separated options, trims, drops empties,
// appends NoneOfAbove. The tool refuses excess choices before asking.
func parseOptions(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append(out, NoneOfAbove)
}

// questions builds up to 3 questions from slots; orphan options die.
func questions(args tool.Args) []Question {
	str := func(name string) string {
		s, _ := args[name].(string)
		return strings.TrimSpace(s)
	}
	var qs []Question
	if q := str("question"); q != "" {
		qs = append(qs, Question{Text: q, Options: parseOptions(str("options"))})
	}
	for _, n := range []string{"2", "3"} {
		if q := str("question" + n); q != "" {
			qs = append(qs, Question{Text: q, Options: parseOptions(str("options" + n))})
		}
	}
	return qs
}

// format renders answers back to the model, one line each.
func format(as []Answer) string {
	if len(as) == 0 {
		return "unanswered"
	}
	lines := make([]string, 0, len(as))
	for i, a := range as {
		pick := a.Picked
		if strings.TrimSpace(pick) == "" {
			pick = "no answer"
		}
		l := fmt.Sprintf("%d: %s", i+1, pick)
		if c := a.Comment; strings.TrimSpace(c) != "" {
			l += " — " + c
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// New returns the ask definition bound to p.
func New(p *Prompter, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	return tool.Definition{Prompts: pc,
		Name:    "ask",
		Label:   "ask",
		Desc:    pc.Text("tools.ask.description"),
		Purpose: pc.Text("tools.ask.purpose"),
		Params: []tool.Param{
			{Name: "question", Desc: pc.Text("tools.ask.parameters.question"), Type: tool.TypeString, Required: true},
			{Name: "options", Desc: pc.Text("tools.ask.parameters.options"), Type: tool.TypeString},
			{Name: "question2", Desc: pc.Text("tools.ask.parameters.question2"), Type: tool.TypeString},
			{Name: "options2", Desc: pc.Text("tools.ask.parameters.options2"), Type: tool.TypeString},
			{Name: "question3", Desc: pc.Text("tools.ask.parameters.question3"), Type: tool.TypeString},
			{Name: "options3", Desc: pc.Text("tools.ask.parameters.options3"), Type: tool.TypeString},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			qs := questions(args)
			for i, q := range qs {
				if len(q.Options) > MaxOptions+1 { // includes the host's NoneOfAbove choice
					return tool.Result{}, fmt.Errorf("question %d has %d options; at most %d model-authored options are allowed; no question asked", i+1, len(q.Options)-1, MaxOptions)
				}
			}
			as, err := p.AskQuestions(ctx, qs)
			if err != nil {
				return tool.Result{}, err
			}
			return tool.Result{Text: format(as)}, nil
		},
	}
}
