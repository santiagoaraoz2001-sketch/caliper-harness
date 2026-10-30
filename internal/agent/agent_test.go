package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// scriptModel replays canned replies, recording what it was shown.
type scriptModel struct {
	replies []string
	seen    [][]llm.Message
	err     error
	usage   llm.Usage
	// usages, when set, returns per-call usage by call index (last
	// repeats) instead of the constant usage.
	usages []llm.Usage
	// failIfContains errors only calls whose messages contain the text
	// (e.g. the compaction prompt) instead of failing everything.
	failIfContains string
	failErr        error
}

func (m *scriptModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	m.seen = append(m.seen, append([]llm.Message(nil), msgs...))
	if m.err != nil {
		return "", "", llm.Usage{}, m.err
	}
	if m.failIfContains != "" {
		for _, msg := range msgs {
			if strings.Contains(msg.Content, m.failIfContains) {
				return "", "", llm.Usage{}, m.failErr
			}
		}
	}
	u := m.usage
	if len(m.usages) > 0 {
		u = m.usages[min(len(m.seen)-1, len(m.usages)-1)]
	}
	if len(m.replies) == 0 {
		return "out of script", "", u, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, "script", u, nil
}

func echoTool(calls *[]string) tool.Definition {
	return tool.Definition{
		Name: "echo", Label: "echo", Desc: "record and repeat",
		Purpose: "Test double.",
		Params:  []tool.Param{{Name: "text", Desc: "text", Type: tool.TypeString, Required: true}},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			*calls = append(*calls, args["text"].(string))
			return tool.Result{Text: "echo:" + args["text"].(string)}, nil
		},
	}
}

func TestTwoStepTaskCompletes(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	model := &scriptModel{replies: []string{
		"```tool\ntool: echo\ntext: hi\n```",
		"done: hi",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	final, transcript, err := r.Run(context.Background(), "say hi via echo")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if final != "done: hi" {
		t.Errorf("final=%q", final)
	}
	if len(calls) != 1 || calls[0] != "hi" {
		t.Errorf("tool calls=%v", calls)
	}
	joined := ""
	for _, m := range transcript {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "echo:hi") {
		t.Error("tool result missing from transcript")
	}
}

func TestRepairRoundtripExecutes(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	model := &scriptModel{replies: []string{
		"```tool\ntool: echo\nslot: text\nvalue: hi\n```",
		"```tool\ntool: echo\ntext: hi\n```",
		"done",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	final, transcript, err := r.Run(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if final != "done" {
		t.Errorf("final=%q", final)
	}
	if len(calls) != 1 || calls[0] != "hi" {
		t.Errorf("tool calls=%v", calls)
	}
	joined := ""
	for _, m := range transcript {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "```tool block starting with") {
		t.Error("repair must show the model the block shape")
	}
}

// recordSink is a fake SessionLog capturing appends.
type recordSink struct {
	ids   []string
	kinds []string
	texts []string
	err   error
}

func (s *recordSink) Append(_ context.Context, id, kind, text string) error {
	if s.err != nil {
		return s.err
	}
	s.ids = append(s.ids, id)
	s.kinds = append(s.kinds, kind)
	s.texts = append(s.texts, text)
	return nil
}

func TestRunPersistsEvents(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	model := &scriptModel{replies: []string{
		"```tool\ntool: echo\ntext: hi\n```",
		"done: hi",
	}}
	sink := &recordSink{}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, SessionID: "s1", Session: sink}
	final, _, err := r.Run(context.Background(), "say hi via echo")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if final != "done: hi" {
		t.Fatalf("final=%q", final)
	}
	wantKinds := []string{"prompt", "context", "context", "reply", "tool", "call_resolution", "result", "context", "consumed", "reply", "done", "evidence", "continuation"}
	if len(sink.kinds) != len(wantKinds) {
		t.Fatalf("kinds=%v", sink.kinds)
	}
	for i, k := range wantKinds {
		if sink.kinds[i] != k {
			t.Fatalf("kinds=%v", sink.kinds)
		}
		if sink.ids[i] != "s1" {
			t.Fatalf("ids=%v", sink.ids)
		}
	}
	if sink.texts[0] != "say hi via echo" {
		t.Errorf("prompt=%q", sink.texts[0])
	}
	if sink.texts[4] != "echo text=hi" {
		t.Errorf("tool=%q", sink.texts[4])
	}
	if !strings.Contains(sink.texts[6], "echo:hi") {
		t.Errorf("result=%q", sink.texts[6])
	}
	if sink.texts[9] != "done: hi" {
		t.Errorf("done=%q", sink.texts[9])
	}
}

func TestRunPersistenceFailureIsLoud(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"answer"}}
	sink := &recordSink{err: errors.New("disk full")}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, SessionID: "s1", Session: sink}
	final, _, err := r.Run(context.Background(), "hi")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("want persistence error, got reply=%q err=%v", final, err)
	}
	if final != "answer" {
		t.Fatalf("reply lost on persistence failure: %q", final)
	}
}

func TestTokenBudgetStopsOnServerCount(t *testing.T) {
	for _, lastPrompt := range []int{2400, 1900} {
		reg := tool.NewRegistry()
		var calls []string
		reg.Register(echoTool(&calls))
		model := &scriptModel{replies: []string{echoBlock("a"), "done"},
			usage: llm.Usage{In: lastPrompt, Out: 5, Reported: true}}
		r := &Runner{Model: model, Registry: reg, MaxSteps: 5, MaxTokens: 1900}
		final, _, err := r.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("in=%d: budget stop must not error: %v", lastPrompt, err)
		}
		if !strings.Contains(final, "content estimate") || !strings.Contains(final, "reached budget 1900") {
			t.Fatalf("in=%d: wrong stop message: %q", lastPrompt, final)
		}
		if len(model.seen) != 1 {
			t.Fatalf("in=%d: model called %d times, want 1 (initial content estimate fits; measured calibration stops the larger next request)", lastPrompt, len(model.seen))
		}
	}
}

func TestUsageLedgerAccumulates(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"done"}, usage: llm.Usage{In: 30, Out: 7, Reported: true}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	if _, _, err := r.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if r.UsedIn != 30 || r.UsedOut != 7 || r.UsageUnknown {
		t.Errorf("ledger in=%d out=%d unknown=%v", r.UsedIn, r.UsedOut, r.UsageUnknown)
	}
}

func TestUnknownUsageProceedsUnbounded(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"done"}} // zero Usage: unreported
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, MaxTokens: 0}
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if final != "done" {
		t.Fatalf("unknown usage must not stop the run: %q", final)
	}
	if !r.UsageUnknown || r.UsedIn != 0 || r.UsedOut != 0 {
		t.Errorf("ledger in=%d out=%d unknown=%v", r.UsedIn, r.UsedOut, r.UsageUnknown)
	}
}

func TestRepairLoopRecovers(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	model := &scriptModel{replies: []string{
		"```tool\ntool: echo\nwrong: x\n```",
		"```tool\ntool: echo\ntext: fixed\n```",
		"done",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	if _, _, err := r.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(calls) != 1 || calls[0] != "fixed" {
		t.Errorf("calls=%v", calls)
	}
	// The repair question must have reached the model.
	if len(model.seen) < 2 || !strings.Contains(model.seen[1][len(model.seen[1])-1].Content, `unsupported argument "wrong"`) {
		t.Error("repair question never shown to model")
	}
}

func TestUnknownToolKeepsGoing(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{
		"```tool\ntool: ghost\n```",
		"fine, no tools then",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if final != "fine, no tools then" {
		t.Errorf("final=%q", final)
	}
}

func TestEmptyBlockShowsSchema(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	model := &scriptModel{replies: []string{
		"```tool\ntool: echo\n```",
		"```tool\ntool: echo\ntext: after-schema\n```",
		"done",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	if _, _, err := r.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("schema request must not execute; calls=%v", calls)
	}
	last := model.seen[1][len(model.seen[1])-1].Content
	if !strings.Contains(last, "Schema for echo") || !strings.Contains(last, "text") {
		t.Errorf("schema not shown: %q", last)
	}
}

func TestMaxStepsStopsPlainly(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{
		"```tool\ntool: ghost\n```",
		"```tool\ntool: ghost\n```",
		"```tool\ntool: ghost\n```",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 2}
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !strings.Contains(final, "stopped after 2 steps") {
		t.Errorf("final=%q", final)
	}
	if len(model.seen) != 2 {
		t.Errorf("model called %d times, want 2", len(model.seen))
	}
}

// TestBtwNoteEntersTranscript pins the mid-run injection: one Btw
// note lands in the transcript as a user message before the model
// is consulted, and the run still completes.
func TestBtwNoteEntersTranscript(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"done: heard it"}}
	note := "watch the loop"
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, Btw: func() string {
		n := note
		note = ""
		return n
	}}
	final, transcript, err := r.Run(context.Background(), "keep going")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if final != "done: heard it" {
		t.Errorf("final=%q", final)
	}
	found := false
	for _, m := range transcript {
		if m.Role == "user" && m.Content == "USER COMMENT: watch the loop" {
			found = true
		}
	}
	if !found {
		t.Error("transcript lacks the actual USER COMMENT note")
	}
}

// TestBtwNoteLandsMidRun pins the timing: a note interjected after
// step 1 reaches the model before its second call — seen[0] lacks
// it, seen[1] carries it — and the run still completes.
func TestBtwNoteLandsMidRun(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	model := &scriptModel{replies: []string{
		"```tool\ntool: echo\ntext: hi\n```",
		"done: heard both",
	}}
	drains := 0
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, Btw: func() string {
		drains++
		if drains == 2 {
			return "slow down"
		}
		return ""
	}}
	final, _, err := r.Run(context.Background(), "say hi via echo")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if final != "done: heard both" {
		t.Errorf("final=%q", final)
	}
	if len(model.seen) < 2 {
		t.Fatalf("model called %d times, want 2", len(model.seen))
	}
	joined := func(msgs []llm.Message) string {
		s := ""
		for _, m := range msgs {
			s += m.Content + "\n"
		}
		return s
	}
	if strings.Contains(joined(model.seen[0]), "USER COMMENT:") {
		t.Error("step-1 call must not see the note")
	}
	if !strings.Contains(joined(model.seen[1]), "USER COMMENT: slow down") {
		t.Error("step-2 call must carry the interjected note")
	}
}

func TestRealBashThroughLoop(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(bash.New(config.BashConfig{DefaultTimeout: 5, MaxTimeout: 10, MaxBytes: 4096, MaxLines: 50}))
	model := &scriptModel{replies: []string{
		"```tool\ntool: bash\ncmd: echo loop-wired\n```",
		"it printed loop-wired",
	}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5}
	final, _, err := r.Run(context.Background(), "prove the loop executes real tools")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !strings.Contains(final, "loop-wired") {
		t.Errorf("final=%q", final)
	}
}

func TestActualBashReceiptsDistinguishModelClaims(t *testing.T) {
	for _, command := range []string{"", "printf failure; exit 7"} {
		t.Run(command, func(t *testing.T) {
			reg := tool.NewRegistry()
			reg.Register(bash.New(config.Defaults().Tools.Bash))
			replies := []string{"PASS all tests passed"}
			want := "0 settled (0 failed)"
			if command != "" {
				replies = append([]string{"```tool\ntool: bash\ncmd: " + command + "\n```"}, replies...)
				want = "1 settled (1 failed)"
			}
			r := &Runner{Model: &scriptModel{replies: replies}, Registry: reg, MaxSteps: 5}
			final, _, err := r.Run(context.Background(), "verify")
			if err != nil || final != "PASS all tests passed" || !strings.Contains(r.Evidence, want) || !strings.Contains(r.Evidence, "correctness/test count unverified") {
				t.Fatalf("facts=%q final=%q err=%v", r.Evidence, final, err)
			}
		})
	}
}
