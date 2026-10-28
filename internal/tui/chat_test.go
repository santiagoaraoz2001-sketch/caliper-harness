package tui

import (
	"context"
	"errors"
	"path/filepath"
	"specific-harness/internal/outcome"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tools/ask"
)

var errTest = errors.New("test")

func TestBuildPrompt(t *testing.T) {
	if got := buildPrompt("hi", nil); got != "hi" {
		t.Errorf("bare=%q", got)
	}
	fb := []Comment{{Source: "plans", Ref: "approachX-T1", Text: "compare"}, {Source: "chat", Text: "be brief"}}
	want := "User comments:\n- [approachX-T1] compare\n- be brief\n\ngo"
	if got := buildPrompt("go", fb); got != want {
		t.Errorf("got %q", got)
	}
}

func TestStripToolBlocks(t *testing.T) {
	if got := stripToolBlocks("just prose"); got != "just prose" {
		t.Errorf("plain=%q", got)
	}
	in := "saying hi\n```tool\ntool: bash\ncmd: x\n```\ntrailing"
	if got := stripToolBlocks(in); got != "saying hi\ntrailing" {
		t.Errorf("stripped=%q", got)
	}
	if got := stripToolBlocks("```tool\ntool: bash\n```"); got != "" {
		t.Errorf("pure block=%q", got)
	}
	if got := stripToolBlocks("prose\n```tool\ntool: bash"); got != "prose" {
		t.Errorf("unclosed=%q", got)
	}
}

func TestWrap(t *testing.T) {
	if got := Wrap("hello world", 5); len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("wrap=%v", got)
	}
	if got := Wrap("abcdefgh", 3); len(got) != 3 || got[2] != "gh" {
		t.Errorf("cut=%v", got)
	}
	if got := Wrap("a\n\nb", 10); len(got) != 3 || got[1] != "" {
		t.Errorf("para=%v", got)
	}
}

func TestCountMemories(t *testing.T) {
	if n := countMemories("Relevant memories:\n- [a] x\n- [b] y"); n != 2 {
		t.Errorf("n=%d", n)
	}
	if n := countMemories(""); n != 0 {
		t.Errorf("empty=%d", n)
	}
}

func TestCommentQueues(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/comment approachX-T1 compare x")
	if len(m.Comment) != 1 || m.Comment[0].Ref != "approachX-T1" || m.Comment[0].Text != "compare x" {
		t.Errorf("comment=%+v", m.Comment)
	}
	m = submit(m, "/comment hello")
	if len(m.Comment) != 2 || m.Comment[1].Ref != "" || m.Comment[1].Text != "hello" {
		t.Errorf("comment=%+v", m.Comment)
	}
	m = submit(m, "/comment always end replies with DONE")
	if len(m.Comment) != 3 || m.Comment[2].Ref != "" || m.Comment[2].Text != "always end replies with DONE" {
		t.Errorf("comment=%+v", m.Comment)
	}
	m = submit(m, "/comment T1 fix it")
	if m.Comment[3].Ref != "T1" || m.Comment[3].Text != "fix it" {
		t.Errorf("comment=%+v", m.Comment)
	}
	m = submit(m, "/comment")
	if lastAlert(m).Kind != "err" {
		t.Errorf("bare /comment must err: %+v", m.Alerts)
	}
}

// TestStreamSurvivesModelCopy pins the crash fix: bubbletea copies
// the Model by value on every message, so stream buffers must
// survive writes after a copy. Both copies stay alive here, so
// their addresses cannot coincide by stack reuse.
func TestStreamSurvivesModelCopy(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Seq = 1
	m.onToken(tokenMsg{seq: 1, delta: "a"})
	cp := m
	cp.onToken(tokenMsg{seq: 1, delta: "b"})
	if got := cp.Chat.Stream.String(); got != "ab" {
		t.Errorf("stream=%q", got)
	}
}

func TestCompactDoneAlerts(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.History = []llm.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"}, {Role: "assistant", Content: "d"}}
	state := session.Continuation{Version: 1}
	state.Add("intent", "user", "a", false)
	state.Add("summary", "user", "Prior context summary:\ns", false)
	um, _ := m.Update(compactDoneMsg{persisted: true, summary: "s", origin: m.origin(), continuation: state})
	m = um.(Model)
	if len(m.Chat.Context.Entries) != 2 || len(m.Chat.History) != 0 || len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "compacted") {
		t.Errorf("history=%d alerts=%+v", len(m.Chat.History), m.Alerts)
	}
}

func TestNewThenCompact(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.History = []llm.Message{{Role: "user", Content: "a"}}
	m = submit(m, "/new")
	m = submit(m, "/compact")
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "nothing to compact") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestNewClearsAlerts(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/bogus")
	m = submit(m, "/new")
	if len(m.Alerts) != 0 {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestClearClearsAlerts(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/bogus")
	m = submit(m, "/clear")
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "context retained") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestChipsShow(t *testing.T) {
	m := New(testCfg(), "test")
	m.Comment = append(m.Comment, Comment{Text: "x"})
	m.Chat.Recalled = 3
	v := m.View()
	if !strings.Contains(v, "1 comment pending") || !strings.Contains(v, "recalled 3") {
		t.Errorf("chips missing:\n%s", v)
	}
}

func TestAskDialogAnswer(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan []ask.Answer, 1)
	qs := []ask.Question{{Text: "run bash?"}}
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "run bash?", questions: qs, askDone: ch})
	m = um.(Model)
	if m.Chat.Dialog == nil {
		t.Fatal("dialog must show")
	}
	if !strings.Contains(m.View(), "run bash?") {
		t.Error("dialog box must show the prompt")
	}
	m.Input.SetValue("yes")
	m = enter(m)
	as := <-ch
	if len(as) != 1 || as[0].Picked != "yes" {
		t.Errorf("answers=%+v", as)
	}
	if m.Chat.Dialog != nil {
		t.Error("dialog must clear")
	}
}

func TestAuthKeysAnswer(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "run?", answer: ch})
	m = um.(Model)
	m = key(m, "y")
	if got := <-ch; got != "y" {
		t.Errorf("answer=%q", got)
	}
	if m.Chat.Dialog != nil {
		t.Error("dialog must clear")
	}
	ch2 := make(chan string, 1)
	um, _ = m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "run?", answer: ch2})
	m = um.(Model)
	m = key(m, "n")
	if got := <-ch2; got != "n" {
		t.Errorf("answer=%q", got)
	}
}

func TestAuthTypingPreserved(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	m.Chat.Running = true
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "run?", answer: ch})
	m = um.(Model)
	m.Input.SetValue("hello")
	m = enter(m)
	if m.Chat.Dialog == nil {
		t.Error("auth dialog must not consume typed input")
	}
	if m.Input.Value() != "hello" {
		t.Errorf("draft=%q", m.Input.Value())
	}
	select {
	case got := <-ch:
		t.Errorf("unexpected answer %q", got)
	default:
	}
}

func TestHistoryNav(t *testing.T) {
	m := New(testCfg(), "test")
	m = submit(m, "first")
	m = submit(m, "second")
	m = key(m, "shift+up")
	if m.Input.Value() != "second" {
		t.Fatalf("up1=%q", m.Input.Value())
	}
	m = key(m, "shift+up")
	if m.Input.Value() != "first" {
		t.Fatalf("up2=%q", m.Input.Value())
	}
	m = key(m, "shift+up")
	if m.Input.Value() != "first" {
		t.Fatalf("up-stop=%q", m.Input.Value())
	}
	m = key(m, "shift+down")
	if m.Input.Value() != "second" {
		t.Fatalf("down1=%q", m.Input.Value())
	}
	m = key(m, "shift+down")
	if m.Input.Value() != "" {
		t.Fatalf("down-draft=%q", m.Input.Value())
	}
	m.Input.SetValue("draft")
	m = key(m, "shift+up")
	m = key(m, "shift+down")
	m = key(m, "shift+down")
	if m.Input.Value() != "draft" {
		t.Errorf("draft=%q", m.Input.Value())
	}
	m.Input.SetValue("bare")
	m = key(m, "up")
	m = key(m, "down")
	if m.Input.Value() != "bare" {
		t.Errorf("bare arrows must not navigate: %q", m.Input.Value())
	}
}

func TestDrafts(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("one")
	m = key(m, "shift+right")
	if m.Draft != 1 || m.Input.Value() != "" {
		t.Fatalf("draft=%d input=%q", m.Draft, m.Input.Value())
	}
	m.Input.SetValue("two")
	m = key(m, "shift+right")
	m = key(m, "shift+left")
	if m.Draft != 1 || m.Input.Value() != "two" {
		t.Fatalf("draft=%d input=%q", m.Draft, m.Input.Value())
	}
	m = key(m, "shift+left")
	if m.Draft != 0 || m.Input.Value() != "one" {
		t.Fatalf("draft=%d input=%q", m.Draft, m.Input.Value())
	}
	m.Input.SetValue("ab")
	m = key(m, "left")
	m = key(m, "right")
	if m.Input.Value() != "ab" {
		t.Errorf("bare arrows must edit, not swap: %q", m.Input.Value())
	}
	m = submit(m, "/draft 2")
	if !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "unknown command") {
		t.Errorf("drafts must not switch via slash: %+v", m.Alerts)
	}
}

func TestDialogGreysInput(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	if v := m.View(); strings.Contains(v, "dialog open") {
		t.Error("no dialog: marker must be absent")
	}
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "run?", answer: ch})
	m = um.(Model)
	if v := m.View(); !strings.Contains(v, "dialog open") {
		t.Error("open dialog must grey the input")
	}
}

func TestDialogHints(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "run?", answer: ch})
	m = um.(Model)
	if v := m.View(); !strings.Contains(v, "y approve") || !strings.Contains(v, "n deny") {
		t.Error("auth dialog must show its keys")
	}
	um, _ = m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "q?", answer: ch})
	m = um.(Model)
	if v := m.View(); !strings.Contains(v, "enter sends") {
		t.Error("ask dialog must show its keys")
	}
	m.Chat.Dialog = nil
	m.Chat.Running = true
	if v := m.View(); !strings.Contains(v, "esc stops") {
		t.Error("running title must show esc stops")
	}
}

func TestAskOptionsChain(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan []ask.Answer, 1)
	qs := []ask.Question{
		{Text: "pick one", Options: []string{"a", "b", "None of the above"}},
		{Text: "pick two", Options: []string{"x", "y"}},
	}
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "pick one", questions: qs, askDone: ch})
	m = um.(Model)
	if v := m.View(); !strings.Contains(v, "Question 1/2") || !strings.Contains(v, "↑↓ pick") {
		t.Fatal("options screen must render")
	}
	m = key(m, "down")
	m = enter(m)
	if m.Chat.Dialog == nil || m.Chat.Dialog.QIdx != 1 {
		t.Fatalf("must advance to q2: %+v", m.Chat.Dialog)
	}
	m = enter(m)
	as := <-ch
	if len(as) != 2 || as[0].Picked != "b" || as[1].Picked != "x" {
		t.Errorf("answers=%+v", as)
	}
	if m.Chat.Dialog != nil {
		t.Error("dialog must clear at chain end")
	}
}

func TestAskComment(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan []ask.Answer, 1)
	qs := []ask.Question{{Text: "q", Options: []string{"a", "None of the above"}}}
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "q", questions: qs, askDone: ch})
	m = um.(Model)
	m = key(m, "tab")
	m = key(m, "h")
	m = key(m, "i")
	m = key(m, "backspace")
	if v := m.View(); !strings.Contains(v, "└─ comment") {
		t.Error("comment row must sit under the pick")
	}
	m = key(m, "down")
	if !m.Chat.Dialog.Commenting {
		t.Error("arrows must keep commenting")
	}
	if m.Chat.Dialog.Comment != "h" || m.Chat.Dialog.PickIdx != 1 {
		t.Errorf("comment must carry across picks: %+v", m.Chat.Dialog)
	}
	m = enter(m)
	as := <-ch
	if len(as) != 1 || as[0].Picked != "None of the above" || as[0].Comment != "h" {
		t.Errorf("answers=%+v", as)
	}
	if m.Input.Value() != "" {
		t.Errorf("comment must not leak to input: %q", m.Input.Value())
	}
}

func TestAskEscDeclines(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan []ask.Answer, 1)
	qs := []ask.Question{{Text: "q", Options: []string{"a"}}}
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "q", questions: qs, askDone: ch})
	m = um.(Model)
	m = key(m, "esc")
	select {
	case as := <-ch:
		if as != nil {
			t.Errorf("cancelled answers=%v", as)
		}
	default:
		t.Error("esc must resolve the chain")
	}
	if m.Chat.Dialog != nil {
		t.Error("dialog must clear")
	}
	ch2 := make(chan []ask.Answer, 1)
	um, _ = m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "q", questions: qs, askDone: ch2})
	m = um.(Model)
	m = key(m, "tab")
	m = key(m, "esc")
	if m.Chat.Dialog == nil {
		t.Fatal("comment-mode esc must keep the dialog")
	}
	if m.Chat.Dialog.Commenting {
		t.Error("comment-mode esc must exit commenting")
	}
}

func TestAskStaleResolves(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan []ask.Answer, 1)
	qs := []ask.Question{{Text: "q"}}
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq + 99, kind: "ask", prompt: "q", questions: qs, askDone: ch})
	m = um.(Model)
	if m.Chat.Dialog != nil {
		t.Error("stale dialog must not show")
	}
	select {
	case as := <-ch:
		if as != nil {
			t.Errorf("stale answers=%v", as)
		}
	default:
		t.Error("stale must resolve nil")
	}
}

func TestDialogStaleDeclines(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq + 99, kind: "auth", prompt: "q", answer: ch})
	m = um.(Model)
	if m.Chat.Dialog != nil {
		t.Error("stale dialog must not show")
	}
	if got := <-ch; got != "n" {
		t.Errorf("stale=%q", got)
	}
}

func TestEscDeclinesDialog(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	m.Chat.Dialog = &Dialog{Kind: "auth", Prompt: "q", Answer: ch}
	m = key(m, "esc")
	if got := <-ch; got != "n" {
		t.Errorf("esc=%q", got)
	}
	if m.Chat.Dialog != nil {
		t.Error("dialog must clear")
	}
}

func TestToolEventFlushes(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Stream.WriteString("saying hi\n```tool\ntool: bash\ncmd: x\n```")
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq, kind: "tool", text: "bash cmd=x"})
	m = um.(Model)
	if len(m.Chat.Lines) != 2 || m.Chat.Lines[0].Kind != lineAssistant || m.Chat.Lines[0].Text != "saying hi" ||
		m.Chat.Lines[1].Kind != lineTool {
		t.Errorf("lines=%+v", m.Chat.Lines)
	}
}

func TestResultEvent(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq, kind: "result", text: "out"})
	m = um.(Model)
	if len(m.Chat.Lines) != 1 || m.Chat.Lines[0].Kind != lineResult {
		t.Errorf("lines=%+v", m.Chat.Lines)
	}
}

func TestReplySkipped(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq, kind: "reply", text: "hi"})
	m = um.(Model)
	if len(m.Chat.Lines) != 0 {
		t.Errorf("reply must skip: %+v", m.Chat.Lines)
	}
}

func TestRecallChip(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq, kind: "recall", text: "Relevant memories:\n- [a] x\n- [b] y"})
	m = um.(Model)
	if m.Chat.Recalled != 2 || len(m.Chat.Lines) != 0 {
		t.Errorf("recalled=%d lines=%+v", m.Chat.Recalled, m.Chat.Lines)
	}
}

func TestUnknownEventSys(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq, kind: "judgment", text: "x"})
	m = um.(Model)
	if len(m.Chat.Lines) != 1 || m.Chat.Lines[0].Kind != lineSys {
		t.Errorf("lines=%+v", m.Chat.Lines)
	}
}

func TestStaleEventDropped(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq + 99, kind: "tool", text: "x"})
	m = um.(Model)
	if len(m.Chat.Lines) != 0 {
		t.Errorf("stale must drop: %+v", m.Chat.Lines)
	}
}

func TestRunDoneAppends(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Stream.WriteString("hello")
	m.Chat.LastFull = "full prompt"
	um, _ := m.Update(runDoneMsg{seq: m.Chat.Seq, final: "hello", in: 10, out: 5})
	m = um.(Model)
	if m.Chat.Running || len(m.Chat.Lines) != 1 || len(m.Chat.History) != 2 {
		t.Errorf("running=%v lines=%+v history=%d", m.Chat.Running, m.Chat.Lines, len(m.Chat.History))
	}
	if m.St.CtxUsed != 0 || m.St.CtxKnown {
		t.Errorf("ctx=%d", m.St.CtxUsed)
	}
}

func TestRunDoneStoppedMsg(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Stream.WriteString("xx")
	um, _ := m.Update(runDoneMsg{seq: m.Chat.Seq, final: "stopped: stuck"})
	m = um.(Model)
	if len(m.Chat.Lines) != 2 {
		t.Errorf("unstreamed final must append: %+v", m.Chat.Lines)
	}
}

func TestRunDoneStaleDropped(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(runDoneMsg{seq: m.Chat.Seq + 1, final: "x"})
	m = um.(Model)
	if !m.Chat.Running || len(m.Chat.History) != 0 {
		t.Errorf("stale must drop: running=%v history=%d", m.Chat.Running, len(m.Chat.History))
	}
}

func TestRunDoneError(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(runDoneMsg{seq: m.Chat.Seq, runErr: errTest})
	m = um.(Model)
	if len(m.Alerts) != 1 || len(m.Chat.Lines) != 1 {
		t.Errorf("alerts=%+v lines=%+v", m.Alerts, m.Chat.Lines)
	}
}

func TestStopIdleAlerts(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/stop")
	if len(m.Alerts) != 1 {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestStopRunning(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	seq := m.Chat.Seq
	m = submit(m, "/stop")
	if !m.Chat.Running || m.Chat.Seq != seq || len(m.Chat.Lines) != 0 || !m.Chat.stopFlag {
		t.Errorf("running=%v seq=%d lines=%+v", m.Chat.Running, m.Chat.Seq, m.Chat.Lines)
	}
}

func TestNewResets(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.appendLine(lineUser, "x")
	m.Chat.History = []llm.Message{{Role: "user", Content: "x"}}
	seq := m.Chat.Seq
	m = submit(m, "/new")
	if len(m.Chat.Lines) != 0 || len(m.Chat.History) != 0 || m.Chat.Seq != seq+1 {
		t.Errorf("lines=%d history=%d seq=%d", len(m.Chat.Lines), len(m.Chat.History), m.Chat.Seq)
	}
}

func TestClearKeepsHistory(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.appendLine(lineUser, "x")
	m.Chat.History = []llm.Message{{Role: "user", Content: "x"}}
	m = submit(m, "/clear")
	if len(m.Chat.Lines) != 0 || len(m.Chat.History) != 1 {
		t.Errorf("lines=%d history=%d", len(m.Chat.Lines), len(m.Chat.History))
	}
}

func TestRetryEmpty(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/retry")
	if len(m.Alerts) != 1 {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestRetryNoModel(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.LastPrompt = "again"
	m = submit(m, "/retry")
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "no model") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestContextStates(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/context")
	if !strings.Contains(m.Alerts[0].Text, "no run yet") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
	m.Chat.LastIn, m.Chat.LastOut = 10, 5
	m = submit(m, "/context")
	if !strings.Contains(m.Alerts[1].Text, "10 in") || !strings.Contains(m.Alerts[1].Text, "5 out") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func tempStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := memory.Add(s, "note", "refund approved", "s1"); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecallFinds(t *testing.T) {
	m := New(testCfg(), "test")
	m.StorePath = tempStore(t)
	m = submit(m, "/recall refund")
	found := false
	for _, l := range m.Chat.Lines {
		if strings.Contains(l.Text, "refund approved") {
			found = true
		}
	}
	if !found {
		t.Errorf("lines=%+v", m.Chat.Lines)
	}
}

func TestRecallNoMatch(t *testing.T) {
	m := New(testCfg(), "test")
	m.StorePath = tempStore(t)
	m = submit(m, "/recall zzz-nope")
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "no memories match") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestRememberSaves(t *testing.T) {
	m := New(testCfg(), "test")
	m.StorePath = tempStore(t)
	m = submit(m, "/remember --global hello world")
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := memory.List(s, "", "hello", 0)
	if err != nil || len(got) != 1 {
		t.Errorf("memories=%v err=%v", got, err)
	}
}

func TestCompactEmpty(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/compact")
	if len(m.Alerts) != 1 {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestAskTransportsNilProg(t *testing.T) {
	m := New(testCfg(), "test")
	if m.askYesNo(0)(context.Background(), "q") {
		t.Error("nil prog must deny")
	}
	if a, err := m.askDialog(0, make(chan struct{}, 1))(context.Background(), []ask.Question{{Text: "q"}}); a != nil || err != nil {
		t.Errorf("nil prog ask=%v,%v", a, err)
	}
}

type fakeLog struct {
	appends *[]string
	err     error
}

func (f fakeLog) Append(_ context.Context, _ string, kind, text string) error {
	*f.appends = append(*f.appends, kind+":"+text)
	return f.err
}

func TestFanout(t *testing.T) {
	var a, b []string
	f := fanoutLog{[]agent.SessionLog{fakeLog{&a, nil}, fakeLog{&b, nil}}}
	if err := f.Append(context.Background(), "s", "k", "t"); err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 {
		t.Errorf("a=%v b=%v", a, b)
	}
	f2 := fanoutLog{[]agent.SessionLog{fakeLog{&a, errTest}}}
	if err := f2.Append(context.Background(), "s", "k", "t"); err != errTest {
		t.Errorf("err=%v", err)
	}
}

func TestRenderChatRows(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.appendLine(lineUser, "hi")
	m.Chat.appendLine(lineTool, "bash x")
	v := m.renderChat(40, 10)
	if !strings.Contains(v, "hi") || !strings.Contains(v, "Run requested") || !strings.Contains(v, "bash x") || strings.Count(v, "•") != 2 {
		t.Errorf("render:\n%s", v)
	}
}

func TestUserPromptSingleMark(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.appendLine(lineUser, "one\ntwo")
	v := m.renderChat(40, 10)
	if n := strings.Count(v, "•"); n != 1 {
		t.Errorf("two-line prompt has %d mark headers, want 1:\n%s", n, v)
	}
	if !strings.Contains(v, "one") || !strings.Contains(v, "two") {
		t.Errorf("prompt lines missing:\n%s", v)
	}
}

func TestRenderChatEmpty(t *testing.T) {
	m := New(testCfg(), "test")
	if v := m.renderChat(40, 10); !strings.Contains(v, "Ask anything") {
		t.Errorf("render:\n%s", v)
	}
}

func TestRenderResultRetainsScrollableOutput(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.appendLine(lineResult, "1\n2\n3\n4\n5")
	v := m.renderChat(40, 10)
	for _, want := range []string{"│ 1", "│ 2", "│ 3", "│ 4", "│ 5"} {
		if !strings.Contains(v, want) {
			t.Errorf("lost recorded result %s: %s", want, v)
		}
	}
}

func TestRunDoneRetainsCheckFactsApartFromModel(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.LastFull = "verify"
	evidence := "Model-reported response. Bash receipts: 1 settled (1 failed); correctness unverified."
	m.onRunDone(runDoneMsg{seq: m.Chat.Seq, final: "PASS", evidence: evidence})
	last := m.Chat.History[len(m.Chat.History)-1]
	if last.Role != "user" || last.Content != evidence {
		t.Fatalf("fact hidden: %+v", m.Chat.History)
	}
	m.Chat.Running = true
	m.onRunDone(runDoneMsg{seq: m.Chat.Seq, final: "PASS", outcome: outcome.Outcome{Status: outcome.Failed, Reason: "required workflow unresolved: declared check exited1"}})
	last = m.Chat.History[len(m.Chat.History)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "declared check exited1") {
		t.Fatalf("known failure hidden: %+v", m.Chat.History)
	}
}
