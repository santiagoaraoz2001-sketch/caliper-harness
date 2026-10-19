package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/run"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/ask"
	workspaceFiles "specific-harness/internal/tools/files"
)

// Transcript line kinds.
const (
	lineUser      = "user"
	lineUserCont  = "usercont"
	lineAssistant = "assistant"
	lineTool      = "tool"
	lineResult    = "result"
	lineSys       = "sys"
	lineDiff      = "observed_diff"
)

// Line is one transcript row.
type Line struct {
	Kind string
	Text string
}

// Dialog is one modal prompt; the agent goroutine waits on Answer.
type Dialog struct {
	Workflow string // owning approach for workflow confirmation
	Kind     string // "auth" or "ask"
	Prompt   string
	Scroll   int         // authorization inspection only
	Answer   chan string // auth replies
	// Ask flow: chained questions with options. Empty Questions
	// keeps the legacy free-text screen.
	Questions  []ask.Question
	QIdx       int
	PickIdx    int
	Commenting bool
	Comment    string
	Answers    []ask.Answer
	AskDone    chan []ask.Answer
	FreeDraft  *string // shared composer borrowed only by an active free answer
}

// Comment is one queued user note, flushed with the next submit.
// Lines is a "[120-122, 140]" viewer reference; Quote holds the
// captured lines so the model sees exactly what was referenced.
type Comment struct {
	Source string
	Ref    string
	Lines  string
	Quote  string
	Text   string
}

// Chat is one conversation: transcript, stream, run state, history.
type Chat struct {
	Tools          string
	Lines          []Line
	TurnStarts     []int // Visible root-prompt positions, retained per tab; never model input.
	Recorded       map[int]session.Entry
	Thoughts       map[int][]recordedThought
	NativeRequests map[int][]llm.ToolCall
	ToolCallIDs    map[int]string
	DiffGroups     map[int]observedChangeGroup // Display only; never model/session input.
	// Stream buffers are pointers: bubbletea copies the Model by
	// value on every message, and a copied strings.Builder panics
	// on its next write. Shared pointers make copies safe.
	Stream        *strings.Builder
	FlushedAll    *strings.Builder
	Streaming     bool
	Running       bool
	Seq           int
	History       []llm.Message // compatibility input only; continuation is authoritative after a run
	Context       session.Continuation
	ID            string
	compactCancel context.CancelFunc
	Compacting    bool
	LastPrompt    string
	LastFull      string
	LastIn        int
	LastOut       int
	LastUnknown   bool
	LastCostKnown bool // settled new-run accounting; prior totals are unavailable after resume
	Recalled      int
	Dialog        *Dialog
	pinTop        int
	readOrigin    chatOrigin
	pinned        bool
	scrollDrag    bool
	scrollDragY   int
	scrollDragTop int
	stopFlag      bool
	pendingNew    bool
	cancel        context.CancelFunc
	btw           *btwBox
	carry         []string
	// Turns counts completed assistant turns for the tab retitler.
	Turns int
	// SessionIDs are this chat's run ids, oldest first: the Decisions
	// view reads their judgment events. Saved per tab with Chat.
	SessionIDs []string
}

// noteSession remembers a run id for the Decisions view, oldest
// first, capped so long chats stay bounded.
func (m *Model) noteSession(id string) {
	m.Chat.SessionIDs = append(m.Chat.SessionIDs, id)
	if len(m.Chat.SessionIDs) > m.Cfg.UI.MaxRunIDs {
		m.Chat.SessionIDs = m.Chat.SessionIDs[len(m.Chat.SessionIDs)-m.Cfg.UI.MaxRunIDs:]
	}
}

// btwBox is one run's interjection queue: /btw pushes from the event
// loop, the run goroutine pops between steps.
type btwBox struct {
	mu        sync.Mutex
	notes     []string
	interrupt context.CancelFunc
}

func (b *btwBox) push(s string) {
	b.mu.Lock()
	b.notes = append(b.notes, s)
	interrupt := b.interrupt
	b.mu.Unlock()
	if interrupt != nil {
		interrupt()
	}
}

func (b *btwBox) generating(interrupt context.CancelFunc) {
	b.mu.Lock()
	b.interrupt = interrupt
	pending := len(b.notes) > 0
	b.mu.Unlock()
	if pending && interrupt != nil {
		interrupt()
	}
}

func (b *btwBox) pending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.notes) > 0
}

func (b *btwBox) pop() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.notes) == 0 {
		return ""
	}
	n := b.notes[0]
	b.notes = b.notes[1:]
	return n
}

func (b *btwBox) drain() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.notes
	b.notes = nil
	return out
}

// Chat messages. seq drops stragglers across stop/new boundaries.
type tokenMsg struct {
	seq   int
	delta string
}

type eventMsg struct {
	seq        int
	kind, text string
}

type runDoneMsg struct {
	continuation session.Continuation
	outcome      outcome.Outcome
	seq          int
	final        string
	evidence     string
	in, out      int
	unknown      bool
	runErr       error
}

type showDialogMsg struct {
	dismiss      bool
	ctx          context.Context
	seq          int
	kind, prompt string
	workflow     string
	answer       chan string
	questions    []ask.Question
	askDone      chan []ask.Answer
}

type origin struct {
	id       string
	revision int
	key      string
}
type compactDoneMsg struct {
	ctx          context.Context
	persisted    bool
	origin       origin
	continuation session.Continuation
	summary      string
	in, out      int
	unknown      bool
	runErr       error
}

func (m Model) contextKey() string {
	return m.Server + "|" + m.ModelName + "|" + m.Cfg.Model.Runtime + "|" + llm.ToolProtocol(m.Cfg.Model.ToolProtocol)
}
func (m Model) originKey() string {
	p, _ := json.Marshal(m.Cfg.Params)
	c, _ := json.Marshal(m.Cfg.Context)
	return m.contextKey() + string(p) + string(c)
}
func (m Model) origin() origin { return origin{m.Chat.ID, m.Chat.Context.Revision, m.originKey()} }
func (m *Model) originChat(o origin) (*Chat, int) {
	if o.key != m.originKey() {
		return nil, -1
	}
	if m.Chat.ID == o.id && m.Chat.Context.Revision == o.revision {
		return &m.Chat, m.chatTabIndex()
	}
	for i := range m.Tabs {
		if i == m.chatTabIndex() || m.Tabs[i].feature != "" {
			continue
		}
		c := &m.Tabs[i].chat
		if c.ID == o.id && c.Context.Revision == o.revision {
			return c, i
		}
	}
	return nil, -1
}
func (m *Model) compactBusy(cancel bool) bool {
	busy := false
	visit := func(c *Chat) {
		if c.Compacting {
			busy = true
			if cancel && c.compactCancel != nil {
				c.stopFlag = true
				c.compactCancel()
			}
		}
	}
	visit(&m.Chat)
	for i := range m.Tabs {
		if i != m.chatTabIndex() && m.Tabs[i].feature == "" {
			visit(&m.Tabs[i].chat)
		}
	}
	return busy
}

// fanoutLog appends to every log, returning the first error.
type fanoutLog struct {
	logs []agent.SessionLog
}

func (f fanoutLog) Append(ctx context.Context, id, kind, text string) error {
	var first error
	for _, l := range f.logs {
		if err := l.Append(ctx, id, kind, text); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// progLog forwards session events to the event loop.
type progLog struct {
	send func(tea.Msg)
	seq  int
}

func (p progLog) Append(_ context.Context, _ string, kind, text string) error {
	p.send(eventMsg{seq: p.seq, kind: kind, text: text})
	return nil
}

// buildPrompt fuses queued comments ahead of the raw message.
func buildPrompt(raw string, fb []Comment) string {
	if len(fb) == 0 {
		return raw
	}
	var b strings.Builder
	b.WriteString("User comments:\n")
	for _, f := range fb {
		var head strings.Builder
		head.WriteString("-")
		if f.Ref != "" {
			head.WriteString(" [" + f.Ref + "]")
		}
		if f.Lines != "" {
			head.WriteString(" " + f.Lines)
		}
		if f.Text != "" {
			head.WriteString(" " + f.Text)
		}
		b.WriteString(head.String() + "\n")
		if f.Quote != "" {
			for _, q := range strings.Split(f.Quote, "\n") {
				b.WriteString("> " + q + "\n")
			}
		}
	}
	b.WriteString("\n" + raw)
	return b.String()
}

// stripToolBlocks drops fenced tool spans from streamed replies; the
// tool events already show the calls, and the session file keeps all.
func stripToolBlocks(s string) string {
	var out []string
	in, malformed, hadSlot := false, false, false
	literal, sourceFence := "", ""
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		opener := t == "```tool" || strings.HasPrefix(t, "```tool ") || strings.HasPrefix(t, "```tool\t")
		if !in {
			fence, info := tool.MarkdownFence(l)
			if sourceFence != "" {
				out = append(out, l)
				if len(fence) >= len(sourceFence) && fence[0] == sourceFence[0] && info == "" {
					sourceFence = ""
				}
				continue
			}
			if opener {
				in, hadSlot = true, false
			} else {
				out = append(out, l)
				sourceFence = fence
			}
			continue
		}
		if malformed {
			continue // No reliable boundary: hide the tail, keep raw session evidence.
		}
		if literal != "" {
			if tool.LiteralEnd(l, literal) {
				literal = ""
			}
			continue
		}
		if t == "```" {
			in = false
			continue
		}
		if opener {
			malformed = true
			continue
		}
		if label, marked := tool.LiteralStart(l); marked {
			literal, malformed, hadSlot = label, label == "", true
		} else {
			slot, ambiguous := tool.DisplayBodyLine(l)
			malformed = ambiguous && (slot || hadSlot)
			hadSlot = hadSlot || slot
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Wrap folds s to width w, cutting overlong tokens mid-word.
func Wrap(s string, w int) []string {
	w = max(1, w)
	var rows []string
	for _, line := range strings.Split(ansi.Wrap(s, w, ""), "\n") {
		if ansi.StringWidth(line) > w {
			rows = append(rows, strings.Split(ansi.Hardwrap(line, w, true), "\n")...)
		} else {
			rows = append(rows, line)
		}
	}
	return rows
}

// countMemories counts "- [kind]" rows in a recall block.
func countMemories(text string) int {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "- [") {
			n++
		}
	}
	return n
}

func (c *Chat) appendLine(kind, text string) {
	c.Lines = append(c.Lines, Line{kind, text})
}

func (c *Chat) unpin() {
	c.pinned = false
	c.pinTop = 0
	c.readOrigin = chatOrigin{}
}

// page scrolls the transcript over total rows; reaching the bottom
// re-follows. Total comes from the caller: render output cannot feed
// back into retained state (View copies the model).
func (c *Chat) page(d, h, total int) {
	if total <= h {
		c.unpin()
		return
	}
	if !c.pinned {
		c.pinned = true
		c.pinTop = total - h
	}
	c.pinTop += d * (h / 2)
	if c.pinTop+h >= total {
		c.unpin()
		return
	}
	if c.pinTop < 0 {
		c.pinTop = 0
	}
}

// startRun launches one agent run in the background: dialog-backed
// auth, discarded stdout, tokens to the loop, session fanned out.
func (m *Model) startRun(raw string) {
	if m.servingPending() {
		return
	}
	if m.Chat.Running || m.Wf.Busy != "" || m.compactBusy(false) {
		m.alert("info", "a run or compaction is already in flight — /btw to interject, /stop to take over")
		m.Input.SetValue(raw)
		m.Input.SetCursor(len(raw))
		return
	}
	if m.ModelName == "" {
		m.alert("err", "no model set — set model.name in config")
		return
	}
	if m.Chat.ID == "" {
		m.Chat.ID = session.NewID()
	}
	prompt := buildPrompt(raw, m.Comment)
	m.Comment = nil
	m.Chat.Recalled = 0
	m.Chat.LastPrompt = raw
	m.Chat.LastFull = prompt
	m.Chat.appendTurn(raw)
	m.Chat.Context.Measured = false
	m.Chat.Stream.Reset()
	m.Chat.FlushedAll.Reset()
	m.Chat.Running, m.Chat.Streaming = true, true
	m.Chat.stopFlag = false
	box := &btwBox{notes: m.Chat.carry}
	m.Chat.carry = nil
	m.Chat.btw = box
	m.Chat.Seq++
	m.Chat.unpin()
	seq := m.Chat.Seq
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.Chat.Running = false
		m.alert("err", fmt.Sprintf("cannot open store: %v", err))
		return
	}
	id := session.NewID()
	m.noteSession(id)
	sessLog := graph.NewSessionLog(graph.New(s, m.Version, m.SessionsPath))
	send := func(msg tea.Msg) {
		if m.Shared.Prog != nil {
			m.Shared.Prog.Send(msg)
		}
	}
	fanout := fanoutLog{[]agent.SessionLog{sessLog, progLog{send: send, seq: seq}}}
	ctx, cancel := context.WithCancel(context.Background())
	m.Chat.cancel = cancel
	dialogs := make(chan struct{}, 1)
	r, notes, err := run.New(run.Deps{
		Tools: m.Chat.Tools, Cfg: m.Cfg, Server: m.Server, Model: m.ModelName,
		MaxSteps: m.Cfg.Agent.MaxSteps, Window: m.St.CtxWindow,
		Store: s, Session: fanout, SessionID: id,
		AskYesNo: m.askConfirm(seq, "", dialogs), Ask: m.askDialog(seq, dialogs),
		In: strings.NewReader(""), Out: io.Discard, ReplyOut: io.Discard,
		OnToken:      func(d string) { send(tokenMsg{seq: seq, delta: d}) },
		History:      m.Chat.History,
		Continuation: m.Chat.Context,
		Btw:          box.pop,
	}, prompt)
	if err != nil {
		s.Close()
		m.Chat.Running = false
		m.alert("err", err.Error())
		return
	}
	for _, n := range notes {
		if strings.HasPrefix(n, "warning:") {
			m.alert("warn", strings.TrimPrefix(n, "warning: "))
		} else {
			m.alert("info", strings.TrimPrefix(n, "note: "))
		}
	}
	r.GenerationInterrupt = box.generating
	r.UserCommentPending = box.pending
	go func() {
		final, _, runErr := r.Run(ctx, prompt)
		s.Close()
		cancel()
		send(runDoneMsg{seq: seq, outcome: r.Outcome, final: final, evidence: r.Evidence, in: r.UsedIn, out: r.UsedOut, unknown: r.UsageUnknown, continuation: r.Continuation, runErr: runErr})
	}()
}

// askYesNo blocks the agent goroutine on a modal y/n dialog.
func (m *Model) askYesNo(seq int) func(context.Context, string) bool {
	return m.askConfirm(seq, "", make(chan struct{}, 1))
}
func (m *Model) askConfirm(seq int, workflow string, dialogs chan struct{}) func(context.Context, string) bool {
	return func(ctx context.Context, prompt string) bool {
		if m.Shared.Prog == nil {
			return false
		}
		ch := make(chan string, 1)
		msg := showDialogMsg{ctx: ctx, seq: seq, workflow: workflow, kind: "auth", prompt: prompt, answer: ch}
		select {
		case dialogs <- struct{}{}:
		case <-ctx.Done():
			return false
		}
		defer m.releaseDialog(ctx, dialogs, msg)
		if ctx.Err() != nil {
			return false
		}
		m.Shared.Prog.Send(msg)
		select {
		case a := <-ch:
			a = strings.ToLower(strings.TrimSpace(a))
			return ctx.Err() == nil && (a == "y" || a == "yes")
		case <-ctx.Done():
			return false
		}
	}
}

// askDialog blocks the agent goroutine on the chained ask dialog.
func (m *Model) askDialog(seq int, dialogs chan struct{}) ask.Querier {
	return func(ctx context.Context, qs []ask.Question) ([]ask.Answer, error) {
		if m.Shared.Prog == nil || len(qs) == 0 {
			return nil, ctx.Err()
		}
		ch := make(chan []ask.Answer, 1)
		msg := showDialogMsg{ctx: ctx, seq: seq, kind: "ask", prompt: qs[0].Text, questions: qs, askDone: ch}
		select {
		case dialogs <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer m.releaseDialog(ctx, dialogs, msg)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		m.Shared.Prog.Send(msg)
		select {
		case a := <-ch:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return a, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (m *Model) releaseDialog(ctx context.Context, dialogs chan struct{}, msg showDialogMsg) {
	if ctx.Err() != nil {
		msg.dismiss = true
		m.Shared.Prog.Send(msg)
	}
	<-dialogs
}

// answerDialog routes one reply to the waiting agent: auth answers
// at once; ask records one free-text answer and advances.
func (m *Model) answerDialog(text string) {
	d := m.Chat.Dialog
	if d.Kind == "ask" {
		m.advanceAsk(ask.Answer{Picked: text})
		return
	}
	m.Chat.Dialog = nil
	d.Answer <- text
}

// advanceAsk records one answer, finishing the chain when done.
func (m *Model) advanceAsk(a ask.Answer) {
	d := m.Chat.Dialog
	d.Answers = append(d.Answers, a)
	d.QIdx++
	if d.QIdx >= len(d.Questions) {
		m.syncAskInput(false)
		m.Chat.Dialog = nil
		d.AskDone <- d.Answers
		return
	}
	d.PickIdx, d.Commenting, d.Comment = 0, false, ""
	m.syncAskInput(m.askFreeText())
}

// syncAskInput isolates a free answer, then returns the borrowed draft.
func (m *Model) syncAskInput(free bool) {
	d := m.Chat.Dialog
	if free && d.FreeDraft == nil {
		draft := m.Input.Value()
		d.FreeDraft = &draft
		m.Input.Reset()
	} else if !free && d.FreeDraft != nil {
		m.Input.SetValue(*d.FreeDraft)
		m.Input.SetCursor(len(m.Input.Value()))
		d.FreeDraft = nil
	}
}

// declineDialog answers a dialog negatively (Esc, stop, stale run).
func (m *Model) declineDialog() {
	d := m.Chat.Dialog
	m.syncAskInput(false)
	m.Chat.Dialog = nil
	if d.Kind == "auth" {
		d.Answer <- "n"
	} else {
		d.AskDone <- nil
	}
}

// askFreeText reports whether the open dialog is an ask dialog on a
// free-text screen: the input line stays live there.
func (m *Model) askFreeText() bool {
	d := m.Chat.Dialog
	if d == nil || d.Kind != "ask" {
		return false
	}
	if len(d.Questions) == 0 {
		return true
	}
	return d.QIdx < len(d.Questions) && len(d.Questions[d.QIdx].Options) == 0
}

// askOptions reports whether the dialog shows an option list.
func (m *Model) askOptions() bool {
	d := m.Chat.Dialog
	return d != nil && d.Kind == "ask" && len(d.Questions) > 0 &&
		d.QIdx < len(d.Questions) && len(d.Questions[d.QIdx].Options) > 0
}

// flushStream banks the buffered reply minus tool spans.
func (m *Model) flushStream() {
	buf := m.Chat.Stream.String()
	m.Chat.FlushedAll.WriteString(buf)
	m.Chat.Stream.Reset()
	if text := stripToolBlocks(buf); text != "" {
		if m.Chat.readOrigin.Stream {
			m.Chat.readOrigin.Line = len(m.Chat.Lines)
			m.Chat.readOrigin.Stream = false
		}
		m.Chat.appendLine(lineAssistant, text)
	}
}

// onToken appends one delta to the live buffer (stale runs drop).
func (m *Model) onToken(msg tokenMsg) {
	if msg.seq != m.Chat.Seq || !m.Chat.Running {
		return
	}
	if msg.delta != "" {
		m.observeWorker("ready")
	}
	m.Chat.Stream.WriteString(msg.delta)
}

// onEvent renders one session event (stale runs drop). Replies and
// dones arrive via the stream and runDone; recall becomes a chip.
func (m *Model) onEvent(msg eventMsg) {
	if msg.seq != m.Chat.Seq || !m.Chat.Running {
		return
	}
	switch msg.kind {
	case "context":
		var reading session.Continuation
		if json.Unmarshal([]byte(msg.text), &reading) == nil {
			m.Chat.Context.Prompt, m.Chat.Context.Measured = reading.Prompt, reading.Measured
			m.Chat.Context.ModelKey = reading.ModelKey
		}
	case "task_settled":
		m.alert("info", msg.text)
	case "tool":
		m.flushStream()
		m.Chat.appendLine(lineTool, msg.text)
		at := len(m.Chat.Lines) - 1
		anchor := -1
		for idx := range m.Chat.NativeRequests {
			if idx <= at && idx > anchor {
				anchor = idx
			}
		}
		for _, call := range m.Chat.NativeRequests[anchor] {
			if call.Name+" "+call.Arguments != msg.text {
				continue
			}
			used := false
			for idx, id := range m.Chat.ToolCallIDs {
				if idx >= anchor && id == call.ID {
					used = true
				}
			}
			if !used {
				if m.Chat.ToolCallIDs == nil {
					m.Chat.ToolCallIDs = make(map[int]string)
				}
				m.Chat.ToolCallIDs[at] = call.ID
				break
			}
		}
		m.lastTool, m.lastToolAt = msg.text, time.Now()
	case "btw":
		m.flushStream()
		m.Chat.appendLine(lineUser, "btw: "+truncate(msg.text, 200))
	case "native_result":
		var entry session.Entry
		if json.Unmarshal([]byte(msg.text), &entry) == nil {
			m.Chat.appendLine(lineResult, entry.Message.Content)
			if m.Chat.Recorded == nil {
				m.Chat.Recorded = make(map[int]session.Entry)
			}
			m.Chat.Recorded[len(m.Chat.Lines)-1] = entry
		}
	case "reply":
		m.flushStream()
	case "native_reply":
		m.flushStream()
		var entry session.Entry
		if json.Unmarshal([]byte(msg.text), &entry) == nil {
			at := len(m.Chat.Lines)
			if at > 0 && m.Chat.Lines[at-1].Kind == lineAssistant && m.Chat.Lines[at-1].Text == stripToolBlocks(entry.Message.Content) {
				at--
			}
			var thoughts []recordedThought
			for _, v := range []recordedThought{{"Thinking", entry.Message.Thinking}, {"Reasoning", entry.Message.Reasoning}, {"Reasoning content", entry.Message.ReasoningContent}} {
				if v.Text != "" {
					thoughts = append(thoughts, v)
				}
			}
			if len(thoughts) > 0 {
				if m.Chat.Thoughts == nil {
					m.Chat.Thoughts = make(map[int][]recordedThought)
				}
				for _, v := range thoughts {
					duplicate := false
					for _, old := range m.Chat.Thoughts[at] {
						if old == v {
							duplicate = true
						}
					}
					if !duplicate {
						m.Chat.Thoughts[at] = append(m.Chat.Thoughts[at], v)
					}
				}
			}
			if len(entry.Message.ToolCalls) > 0 {
				if m.Chat.NativeRequests == nil {
					m.Chat.NativeRequests = make(map[int][]llm.ToolCall)
				}
				for _, call := range entry.Message.ToolCalls {
					duplicate := false
					for _, old := range m.Chat.NativeRequests[at] {
						if old == call {
							duplicate = true
						}
					}
					if !duplicate {
						m.Chat.NativeRequests[at] = append(m.Chat.NativeRequests[at], call)
					}
				}
			}
		}
	case "reasoning":
	case "result":
		m.Chat.appendLine(lineResult, msg.text)
	case "error":
		m.Chat.appendLine(lineSys, msg.text)
	case "prompt", "done", "behavior", "evidence", "coding", "continuation":
	case "recall":
		m.Chat.Recalled = countMemories(msg.text)
	default:
		m.Chat.appendLine(lineSys, truncate(msg.text, 120))
	}
}

// onRunDone closes a run: bank the stream, append history (misses
// only duplicate finals), feed the ctx bar, maybe retitle the tab.
func (m *Model) onRunDone(msg runDoneMsg) tea.Cmd {
	if msg.seq != m.Chat.Seq || !m.Chat.Running {
		return nil
	}
	m.Chat.Running, m.Chat.Streaming = false, false
	if msg.runErr != nil && !m.Chat.stopFlag && msg.outcome.Status != outcome.Stopped {
		m.observeWorker("issue")
	}
	m.Chat.Context = msg.continuation
	if msg.continuation.Workflow != nil {
		m.Chat.Tools = "workflow"
	}
	if msg.continuation.Coding != nil {
		m.Chat.appendLine(lineSys, fmt.Sprintf("check: %s (revision %d)", func() string {
			if msg.continuation.Coding.Passed() {
				return "declared check passed; coverage unknown"
			}
			return "unverified; rerun required"
		}(), msg.continuation.Coding.Revision))
	}
	if m.Chat.Dialog != nil {
		m.declineDialog()
	}
	m.flushStream()
	m.drainBtw()
	var title tea.Cmd
	if m.Chat.stopFlag {
		m.Chat.appendLine(lineSys, "stopped.")
	} else {
		if msg.outcome.Status != "" && msg.outcome.Status != outcome.Completed {
			m.Chat.appendLine(lineSys, fmt.Sprintf("run %s: %s", msg.outcome.Status, msg.outcome.Reason))
		}
		if msg.runErr != nil {
			if msg.outcome.Status == outcome.Stopped {
				// The owning terminal status remains Stopped; retain returned detail.
				if !strings.Contains(msg.outcome.Reason, msg.runErr.Error()) {
					m.Chat.appendLine(lineSys, "run detail: "+truncate(msg.runErr.Error(), 200))
				}
				m.alert("info", truncate(msg.runErr.Error(), 200))
			} else {
				m.alert("err", truncate(msg.runErr.Error(), 200))
				m.Chat.appendLine(lineSys, "run failed: "+truncate(msg.runErr.Error(), 200))
			}
		}
		if msg.final != "" && !strings.Contains(m.Chat.FlushedAll.String(), msg.final) {
			if text := stripToolBlocks(msg.final); text != "" {
				m.Chat.appendLine(lineAssistant, text)
			}
		}
		if msg.continuation.Version == 0 {
			m.Chat.History = append(m.Chat.History, llm.Message{Role: "user", Content: m.Chat.LastFull}, llm.Message{Role: "assistant", Content: msg.final})
		}
		m.Chat.Turns++
		if !m.quitPending && !m.Chat.pendingNew {
			title = m.titleTurn(msg.final)
		}
	}
	if msg.evidence != "" {
		m.Chat.appendLine(lineSys, msg.evidence)
		if msg.continuation.Version == 0 {
			m.Chat.History = append(m.Chat.History, llm.Message{Role: "user", Content: msg.evidence})
		}
	}
	if msg.continuation.Version == 0 && msg.outcome.Status != outcome.Completed && strings.Contains(msg.outcome.Reason, "declared check") {
		m.Chat.History = append(m.Chat.History, llm.Message{Role: "user", Content: "Harness outcome: " + msg.outcome.Reason})
	}
	if len(m.Chat.History) > m.Cfg.UI.MaxLegacyMessages {
		m.Chat.History = m.Chat.History[len(m.Chat.History)-m.Cfg.UI.MaxLegacyMessages:]
	}
	m.Chat.LastIn, m.Chat.LastOut, m.Chat.LastUnknown = msg.in, msg.out, msg.unknown
	m.Chat.LastCostKnown = true
	m.Chat.Context = msg.continuation
	if msg.continuation.Version > 0 {
		m.Chat.History = nil
	}
	m.St.CtxUsed = msg.continuation.Prompt
	m.St.CtxKnown = msg.continuation.Measured
	if m.quitPending {
		return m.requestQuit()
	}
	if m.Chat.pendingNew {
		m.newChat()
		return nil
	}
	return title
}

// titleMsg carries one finished tab retitle.
type titleMsg struct {
	title  string
	origin origin
}

// titleTurn asks the worker to name the chat every N completed turns.
// Silent by design: naming never interrupts, failures keep the old name.
func (m *Model) titleTurn(final string) tea.Cmd {
	every := m.Cfg.Tabs.TitleEvery
	if every <= 0 || final == "" || m.Chat.Turns%every != 0 {
		return nil
	}
	pc, err := m.Cfg.LoadPrompts()
	if err != nil {
		m.alert("err", err.Error())
		return nil
	}
	server, model, prompt, runtime, owner := m.Server, m.ModelName, m.Chat.LastFull, m.Cfg.Model.Runtime, m.origin()
	params := run.ModelParams(m.Cfg.Params)
	titleTimeout, plainTimeout := time.Duration(m.Cfg.Tabs.TitleTimeoutSeconds)*time.Second, time.Duration(m.Cfg.Model.PlainRequestTimeoutSeconds)*time.Second
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), titleTimeout)
		defer cancel()
		reply, _, _, err := llm.New(server, model).WithPlainTimeout(plainTimeout).WithRuntime(runtime).WithParams(params).Chat(ctx, []llm.Message{
			{Role: "system", Content: pc.Text("title.system")},
			{Role: "user", Content: truncate(prompt, 500) + "\n---\n" + truncate(final, 500)},
		})
		if err != nil {
			return nil
		}
		return titleMsg{title: reply, origin: owner}
	}
}

// sanitizeTitle keeps the model's naming honest: first line,
// trimmed, short. Blank stays blank (caller keeps the old name).
func sanitizeTitle(s string) string {
	s = oneLine(strings.TrimSpace(s))
	s = strings.Trim(s, `"'*.`)
	return truncate(strings.TrimSpace(s), 28)
}

// onTitle lands a retitle on the chat tab that earned it.
func (m *Model) onTitle(msg titleMsg) {
	if title := sanitizeTitle(msg.title); title != "" {
		if _, i := m.originChat(msg.origin); i >= 0 {
			m.Tabs[i].Title = title
		}
	}
}

// stopChat cancels the flight, declines any dialog, banks partial
// output, and invalidates stragglers.
func (m *Model) stopChat() {
	if !m.Chat.Running {
		if m.compactBusy(true) {
			m.alert("info", "stopping compaction — waiting for settlement")
			return
		}
		if m.Wf.Busy != "" && m.Wf.cancel != nil {
			if m.Chat.Dialog != nil && m.Chat.Dialog.Workflow == m.Wf.Busy {
				m.declineDialog()
			}
			m.Wf.cancel()
			m.alert("info", "stopping workflow — waiting for owned work to settle")
			return
		}
		m.alert("info", "no run in flight")
		return
	}
	m.Chat.stopFlag = true
	if m.Chat.cancel != nil {
		m.Chat.cancel()
	}
	if m.Chat.Dialog != nil {
		m.declineDialog()
	}
	m.alert("info", "stopping — waiting for owned work to settle")
}

// drainBtw carries unheard notes to the next run.
func (m *Model) drainBtw() {
	if m.Chat.btw == nil {
		return
	}
	if left := m.Chat.btw.drain(); len(left) > 0 {
		m.Chat.carry = append(m.Chat.carry, left...)
		m.Chat.appendLine(lineSys, fmt.Sprintf("carried %d unheard note(s) to the next run", len(left)))
	}
	m.Chat.btw = nil
}

// btwChat pauses only generation; running effects settle before the next response.
func (m *Model) btwChat(args []string) {
	if len(args) == 0 {
		m.alert("err", "usage: /btw <text>")
		return
	}
	if !m.Chat.Running || m.Chat.btw == nil {
		m.alert("err", "no run in flight — just type in chat")
		return
	}
	m.Chat.btw.push(strings.Join(args, " "))
	m.alert("info", "noted — pauses generation and delivers a user comment after current effects settle")
}

// newChat stops quietly and starts a fresh conversation.
func (m *Model) newChat() {
	if m.Chat.Compacting {
		m.Chat.pendingNew = true
		m.Chat.stopFlag = true
		if m.Chat.compactCancel != nil {
			m.Chat.compactCancel()
		}
		return
	}
	if m.Chat.Running {
		m.Chat.pendingNew = true
		m.stopChat()
		return
	}
	seq := m.Chat.Seq + 1
	m.drainBtw()
	carry := m.Chat.carry
	m.Chat = Chat{ID: session.NewID(), Seq: seq, Stream: &strings.Builder{}, FlushedAll: &strings.Builder{}}
	m.Chat.carry = carry
	m.Alerts = nil
}

// retryChat resubmits the last raw prompt.
func (m *Model) retryChat() {
	if m.Chat.Running {
		m.alert("info", "a run is already in flight — /btw to interject, /stop to take over")
		return
	}
	if m.Chat.LastPrompt == "" {
		m.alert("info", "nothing to retry")
		return
	}
	m.startRun(m.Chat.LastPrompt)
}

// clearChat clears the view and alerts; history (context) stays.
func (m *Model) clearChat() {
	m.Chat.Lines = nil
	m.Chat.Recorded = nil
	m.Chat.Thoughts = nil
	m.Chat.NativeRequests = nil
	m.Chat.ToolCallIDs = nil
	m.Chat.TurnStarts = nil
	m.Chat.DiffGroups = nil
	m.Chat.unpin()
	m.Alerts = nil
}

// contextChat reports the last run's usage.
func (m *Model) contextChat() {
	if !m.Chat.Running && !m.Chat.Context.Measured && len(m.Chat.Context.Entries) == 0 && len(m.Chat.History) == 0 && !m.Chat.LastCostKnown && m.Chat.LastIn == 0 && m.Chat.LastOut == 0 && !m.Chat.LastUnknown {
		m.alert("info", "no run yet")
		return
	}
	reading := "unknown"
	if m.Chat.Context.Measured && m.Chat.Context.ModelKey == m.contextKey() {
		reading = kfmt(m.Chat.Context.Prompt)
	}
	cost := "prior run cost unavailable after resume"
	if m.Chat.LastCostKnown {
		cost = fmt.Sprintf("cumulative last-run cost: %s in, %s out", kfmt(m.Chat.LastIn), kfmt(m.Chat.LastOut))
	} else if m.Chat.Running {
		cost = "current run cost available after settlement"
	} else if m.Chat.LastIn != 0 || m.Chat.LastOut != 0 || m.Chat.LastUnknown {
		cost += fmt.Sprintf("; new compaction cost since resume: %s in, %s out", kfmt(m.Chat.LastIn), kfmt(m.Chat.LastOut))
	}
	s := fmt.Sprintf("last measured request prompt: %s / %s; %s", reading, kfmt(m.St.CtxWindow), cost)
	if m.Chat.LastUnknown {
		s += " (some exchanges unreported)"
	}
	m.alert("info", s)
}

// recallChat searches memories into the transcript.
func (m *Model) recallChat(args []string) {
	q := strings.Join(args, " ")
	if q == "" {
		m.alert("err", "usage: /recall <query>")
		return
	}
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", fmt.Sprintf("cannot open store: %v", err))
		return
	}
	block, err := memory.Query(s, "", q, m.Cfg.Recall.TopN, m.Cfg.Recall.MaxChars, m.Cfg.Recall.MinWord)
	s.Close()
	if err != nil {
		m.alert("err", fmt.Sprintf("recall failed: %v", err))
		return
	}
	if block == "" {
		m.alert("info", fmt.Sprintf("no memories match %q", q))
		return
	}
	m.Chat.appendLine(lineSys, block)
	m.Chat.unpin()
	m.showChat()
}

// rememberChat saves one note memory.
func (m *Model) rememberChat(args []string) {
	text := strings.Join(args, " ")
	if text == "" {
		m.alert("err", "usage: /remember <text>")
		return
	}
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", fmt.Sprintf("cannot open store: %v", err))
		return
	}
	_, err = memory.Add(s, "note", text, "")
	s.Close()
	if err != nil {
		m.alert("err", fmt.Sprintf("remember failed: %v", err))
		return
	}
	m.Chat.appendLine(lineSys, "remembered.")
	m.Chat.unpin()
	m.showChat()
}

// compactChat owns one bounded call; navigation does not transfer its ownership.
func (m *Model) compactChat() tea.Cmd {
	if m.servingPending() {
		return nil
	}
	if m.Wf.Busy != "" {
		m.alert("info", "a workflow owns the model slot — wait or stop it first")
		return nil
	}
	if m.Chat.Running || m.compactBusy(false) {
		m.alert("info", "a run or compaction is already in flight")
		return nil
	}
	cfg := m.Cfg
	pc, err := cfg.LoadPrompts()
	if err != nil {
		m.alert("err", err.Error())
		return nil
	}
	c := m.Chat.Context.Clone()
	if c.Version == 0 && len(m.Chat.History) == 0 {
		m.alert("info", "nothing to compact")
		return nil
	}
	if c.Version == 0 {
		var err error
		c, _, err = session.RestoreWithPrompts(historyEvents(m.Chat.History), m.St.CtxWindow, pc)
		if err != nil {
			m.alert("err", err.Error())
			return nil
		}
	}
	if m.St.CtxWindow <= 0 {
		m.alert("err", "set context.window before compacting")
		return nil
	}
	owner := m.origin()
	path, dir, version := m.StorePath, m.SessionsPath, m.Version
	id := session.NewID()
	if n := len(m.Chat.SessionIDs); n > 0 {
		id = m.Chat.SessionIDs[n-1]
	} else {
		m.noteSession(id)
	}
	server, model, runtime := m.Server, m.ModelName, cfg.Model.Runtime
	params := run.ModelParams(cfg.Params)
	dj := decision.New(cfg.Decision.Server, cfg.Decision.Model, cfg.Decision.Device, time.Duration(cfg.Decision.TimeoutSeconds)*time.Second).WithSelection(decision.SelectionFor(cfg.Decision)).WithPrompts(pc).WithInputLimit(cfg.Decision.MaxInputTokens).WithParams(run.ModelParams(cfg.Params))
	r := agent.Runner{Prompts: pc, ModelKey: m.contextKey(), Model: llm.New(server, model).WithPlainTimeout(time.Duration(cfg.Model.PlainRequestTimeoutSeconds) * time.Second).WithRuntime(runtime).WithParams(params), MaxTokens: m.St.CtxWindow, TemplatesDir: cfg.TemplatesDir(), Compaction: agent.Compaction{Summary: cfg.Context.Summary, Step: cfg.Context.SummaryStep, Max: cfg.Context.SummaryMax, WordsPerToken: cfg.Context.SummaryWordsPerToken}, Judge: dj.Judge, Threshold: cfg.Decision.RiskThreshold, JudgmentContextChars: cfg.Decision.JudgmentContextChars}
	r.ThinkingLoop = run.ThinkingLoopPolicy(cfg)
	var ctx context.Context
	var cancel context.CancelFunc
	if m.Cfg.Agent.MaxRunSeconds > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(m.Cfg.Agent.MaxRunSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	m.Chat.compactCancel = cancel
	m.Chat.Compacting = true
	m.Chat.stopFlag = false
	return func() tea.Msg {
		s, err := store.OpenWithBufferPool(path, uint64(cfg.Store.BufferPoolBytes))
		if err != nil {
			return compactDoneMsg{ctx: ctx, origin: owner, continuation: c, runErr: err}
		}
		defer s.Close()
		r.Session, r.SessionID = graph.NewSessionLog(graph.New(s, version, dir)), id
		next, sum, ran, err := r.Compact(ctx, c)
		if err == nil && !ran {
			err = fmt.Errorf("nothing eligible to compact")
		}
		return compactDoneMsg{ctx: ctx, origin: owner, continuation: next, summary: sum, in: r.UsedIn, out: r.UsedOut, unknown: r.UsageUnknown, runErr: err}
	}
}
func (m *Model) onCompactDone(msg compactDoneMsg) tea.Cmd {
	// Even a stale config result must release its originating busy token.
	o := msg.origin
	o.key = m.originKey()
	c, _ := m.originChat(o)
	target, _ := m.originChat(msg.origin)
	if msg.ctx != nil && msg.ctx.Err() != nil && msg.runErr == nil {
		msg.runErr = msg.ctx.Err()
	}
	if c != nil && !msg.persisted {
		c.LastIn += msg.in
		c.LastOut += msg.out
		c.LastUnknown = c.LastUnknown || msg.unknown
	}
	if c != nil && c.stopFlag && msg.runErr == nil {
		msg.runErr = context.Canceled
	}
	if target == nil && msg.runErr == nil {
		msg.runErr = fmt.Errorf("compaction origin/revision/config changed — context intact")
	}
	if msg.runErr == nil && !msg.persisted {
		encoded, err := msg.continuation.Encode()
		if err != nil {
			msg.runErr = err
		} else {
			path, dir, version := m.StorePath, m.SessionsPath, m.Version
			cfg := m.Cfg
			id := session.NewID()
			if n := len(target.SessionIDs); n > 0 {
				id = target.SessionIDs[n-1]
			} else {
				target.SessionIDs = append(target.SessionIDs, id)
			}
			ctx := msg.ctx
			return func() tea.Msg {
				s, err := store.OpenWithBufferPool(path, uint64(cfg.Store.BufferPoolBytes))
				if err == nil {
					log := graph.NewSessionLog(graph.New(s, version, dir))
					err = log.Append(ctx, id, "compact", msg.summary)
					if err == nil {
						err = log.Append(ctx, id, "continuation", encoded)
					}
					s.Close()
				}
				msg.persisted = true
				msg.runErr = err
				return msg
			}
		}
	}
	if c != nil {
		c.Compacting = false
		if c.compactCancel != nil {
			c.compactCancel()
		}
		c.compactCancel = nil
	}
	if target != nil && msg.runErr == nil {
		target.Context = msg.continuation
		target.History = nil
		target.appendLine(lineSys, "history compacted; prompt size unknown until next measured request")
		m.alert("info", "context compacted; last measured prompt unknown")
	}
	if msg.runErr != nil {
		m.alert("err", msg.runErr.Error())
	}
	if c == &m.Chat && m.Chat.pendingNew {
		m.newChat()
	}
	return nil
}
func historyEvents(h []llm.Message) []session.Event {
	events := make([]session.Event, 0, len(h))
	for _, msg := range h {
		if len(msg.ToolCalls) > 0 || msg.Role == "tool" {
			kind := "native_reply"
			provenance := "reply"
			if msg.Role == "tool" {
				kind = "native_result"
				provenance = "output"
			}
			raw, _ := json.Marshal(session.Entry{Message: msg.Clone(), Kind: provenance})
			events = append(events, session.Event{Kind: kind, Text: string(raw)})
			continue
		}
		kind := "prompt"
		if msg.Role == "assistant" {
			kind = "reply"
		}
		events = append(events, session.Event{Kind: kind, Text: msg.Content})
	}
	return events
}

// isRef spots task refs (T1, T1.1, approachX-T1) and file paths
// (sub/a.txt, a.txt). A dash or slash always marks a ref; otherwise
// leading letters plus digits, or a dotted name with meat on both
// sides of the dot.
func isRef(s string) bool {
	if strings.ContainsAny(s, "-/") {
		return true
	}
	if isDottedName(s) {
		return true
	}
	i := 0
	for i < len(s) && isASCIILetter(s[i]) {
		i++
	}
	j, digits := i, 0
	for j < len(s) && (isASCIIDigit(s[j]) || s[j] == '.') {
		if isASCIIDigit(s[j]) {
			digits++
		}
		j++
	}
	return i > 0 && digits > 0 && j == len(s)
}

// isDottedName spots file-like names: a.txt, main.go. Exactly one
// dot with alphanumerics both sides — "e.g." doesn't count.
func isDottedName(s string) bool {
	a, b, ok := strings.Cut(s, ".")
	if !ok || strings.Contains(b, ".") {
		return false
	}
	word := func(p string) bool {
		if p == "" {
			return false
		}
		for i := 0; i < len(p); i++ {
			if !isASCIILetter(p[i]) && !isASCIIDigit(p[i]) {
				return false
			}
		}
		return true
	}
	return word(a) && word(b)
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// commentPrefill jumps to chat with /comment prefilled for ref.
func (m *Model) commentPrefill(ref string) {
	m.showChat()
	m.Input.SetValue("/comment " + ref + " ")
	m.Input.SetCursor(len(m.Input.Value()))
	m.PickerHide = true
	m.PickerIdx = 0
}

// activeFile is the open file viewer behind /comment lines and
// /goto: its ref plus numbered logical lines. Plans dive, diff file
// body, and files explorer share this door.
func (m *Model) activeFile() (ref string, logical []string, ok bool) {
	switch m.Tabs[m.Active].at.view {
	case ViewPlans:
		if d := m.Plans.Detail; d != nil {
			return d.Ref, detailLogical(d), true
		}
	case ViewDiff, ViewChanges:
		if vw := m.Diff.Viewer; vw != nil {
			return vw.Ev.Path, viewerBody(vw.Ev), true
		}
	case ViewFiles:
		if vw := m.Files.Viewer; vw != nil {
			return vw.Title, vw.Logical, true
		}
	}
	return "", nil, false
}

// commentChat queues one note for the next submit:
// /comment [ref] [lines] <text>. The first word is the ref only
// when it looks like one; a "[1-3, 5]" word quotes numbered viewer
// lines from the open file viewer (any of the three). Plain words
// stay text. Ref-only comments work from anywhere.
func (m *Model) commentChat(args []string) {
	if len(args) == 0 {
		m.alert("err", "usage: /comment [ref] [lines] <text>")
		return
	}
	taskID, taskAttempt := "", 0
	f := Comment{Source: string(m.Tabs[m.Active].at.view)}
	rest := args
	badRange := func(s string) {
		m.alert("err", fmt.Sprintf("bad line range %q — use [1-3, 5]", s))
	}
	if looksLikeSpec(rest[0]) {
		if !isLinesSpec(rest[0]) {
			badRange(rest[0])
			return
		}
		f.Lines = rest[0]
		rest = rest[1:]
	} else if isRef(rest[0]) && len(rest) > 1 {
		f.Ref = rest[0]
		rest = rest[1:]
		if len(rest) > 0 && looksLikeSpec(rest[0]) {
			if !isLinesSpec(rest[0]) {
				badRange(rest[0])
				return
			}
			f.Lines = rest[0]
			rest = rest[1:]
		}
	}
	if f.Ref == "" && f.Lines != "" && m.plansActive() && m.Plans.Detail != nil {
		f.Ref = m.Plans.Detail.Ref
	}
	if f.Ref != "" {
		_, uuidErr := uuid.Parse(f.Ref)
		planRef := isPlanRef(f.Ref)
		if _, err := m.Plans.resolve(f.Ref); err == nil || planRef || uuidErr == nil {
			lp, err := loadPlans(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
			if err != nil {
				m.alert("err", err.Error())
				return
			}
			st := PlansState{Plans: lp}
			st.rebuild("")
			r, err := st.resolve(f.Ref)
			if err != nil {
				m.alert("err", err.Error())
				return
			}
			if m.Chat.Running && r.Kind == "task" {
				taskID, taskAttempt = r.ID, r.Tries
			}
			f.Ref = r.Ref
		}
	}
	f.Text = strings.Join(rest, " ")
	if f.Lines != "" {
		ref, logical, ok := m.activeFile()
		if !ok {
			m.alert("err", "line refs need an open file viewer — open a plan dive, diff file, or files entry first")
			return
		}
		if f.Ref == "" {
			f.Ref = ref
		} else if f.Ref != ref {
			m.alert("err", fmt.Sprintf("line refs quote %s — open %s to quote it", ref, f.Ref))
			return
		}
		q, err := Quote(logical, f.Lines)
		if err != nil {
			m.alert("err", err.Error())
			return
		}
		f.Quote = q
	}
	if taskID != "" {
		s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
		if err != nil {
			m.alert("err", err.Error())
			return
		}
		defer s.Close()
		workspace, err := workspaceFiles.Capture(m.Cfg.Tools.Bash)
		if err != nil {
			m.alert("err", err.Error())
			return
		}
		if len(m.Chat.SessionIDs) == 0 {
			m.alert("err", "running task comment needs the actual run session")
			return
		}
		raw, _ := json.Marshal(f)
		_, err = plan.AppendTaskComment(plan.WithProject(context.Background(), workspace.Path), s, taskID, taskAttempt, "user", m.Chat.SessionIDs[len(m.Chat.SessionIDs)-1], string(raw))
		if err != nil {
			m.alert("err", err.Error())
			return
		}
		m.alert("info", "task comment recorded for the current attempt; delivered at its next request")
		return
	}
	m.Comment = append(m.Comment, f)
}

// gotoChat jumps the open file viewer to one line: /goto 135 puts
// line 135 at the top. Viewer-scoped — anywhere else it says so.
func (m *Model) gotoChat(args []string) {
	if len(args) != 1 {
		m.alert("err", "usage: /goto <line>")
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || n < 1 {
		m.alert("err", fmt.Sprintf("bad line %q — use /goto 135", args[0]))
		return
	}
	ref, logical, ok := m.activeFile()
	if !ok {
		m.alert("err", "goto needs an open file viewer — open a plan dive, diff file, or files entry first")
		return
	}
	if len(logical) == 0 {
		m.alert("err", "no lines in this viewer")
		return
	}
	actual := min(n, len(logical))
	m.alert("info", fmt.Sprintf("%s: line %d", ref, actual))
	layout, scroll, avail := m.viewerLayout(m.Tabs[m.Active].at.view)
	if avail < 1 {
		m.alert("err", "terminal too short for file content")
		return
	}
	*scroll = layout.gotoLine(actual, avail)
}

// showChat navigates the active tab to chat (transcript writers).
func (m *Model) showChat() {
	if m.Tabs[m.Active].at.view != ViewChat {
		m.navigate(ViewChat, "")
	}
}

// chatRow is one wrapped, unstyled transcript row.
type chatRow struct {
	kind, text string
	origin     chatOrigin
	label      string
}

// workflowChat changes only the idle owning chat; browsing never selects scope.
func (m *Model) workflowChat(args []string) {
	if m.Chat.Running || m.Wf.Busy != "" || m.compactBusy(false) || m.servingPending() {
		m.alert("err", "select workflow when idle")
		return
	}
	if len(args) > 1 {
		m.alert("err", "use /workflow [root|qualified-ref|UUID]")
		return
	}
	ref := "root"
	if len(args) == 1 {
		ref = args[0]
	}
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", err.Error())
		return
	}
	defer s.Close()
	scope, err := launch.SelectScope(s, ref)
	if err != nil {
		m.alert("err", err.Error())
		return
	}
	n, err := launch.ScopeNode(s, scope)
	if err != nil {
		m.alert("err", err.Error())
		return
	}
	m.Chat.Context.Workflow = scope
	m.Chat.Tools = "workflow"
	m.Chat.Context.Invalidate()
	m.Chat.appendLine(lineSys, fmt.Sprintf("workflow: %s %s UUID=%q state=%q ancestors=%q children=%d", n.Kind, n.Ref, n.ID, n.State, n.Ancestors, n.Children))
}
