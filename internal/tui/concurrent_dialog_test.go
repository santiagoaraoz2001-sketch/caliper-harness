package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/tools/ask"
)

type dialogFrame struct{ prompt, draft, view string }
type dialogProbe struct {
	Model
	frames chan dialogFrame
}

func (p dialogProbe) Init() tea.Cmd { return nil }
func (p dialogProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := p.Model.Update(msg)
	p.Model = next.(Model)
	f := dialogFrame{draft: p.Input.Value(), view: p.View()}
	if p.Chat.Dialog != nil {
		f.prompt = p.Chat.Dialog.Prompt
	}
	p.frames <- f
	return p, cmd
}
func dialogProgram(t *testing.T) (*Model, *tea.Program, chan dialogFrame, context.Context) {
	t.Helper()
	m := New(testCfg(), "component")
	runCtx, cancel := context.WithCancel(context.Background())
	m.Chat.Running, m.Chat.Seq, m.Chat.cancel = true, 1, cancel
	m.Input.SetValue("retained draft")
	frames := make(chan dialogFrame, 64)
	prog := tea.NewProgram(dialogProbe{m, frames}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	m.Shared.Prog = prog
	done := make(chan error, 1)
	go func() { _, err := prog.Run(); done <- err }()
	prog.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	<-frames
	t.Cleanup(func() {
		cancel()
		prog.Quit()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("program failed to settle")
		}
	})
	return &m, prog, frames, runCtx
}
func dialogShown(t *testing.T, frames chan dialogFrame, prompt string) dialogFrame {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f := <-frames:
			if f.prompt == prompt {
				if prompt != "" && !strings.Contains(f.view, prompt) {
					t.Fatal("prompt missing from View")
				}
				return f
			}
		case <-deadline:
			t.Fatalf("dialog %q not delivered", prompt)
		}
	}
}
func dialogBool(t *testing.T, ch chan bool, want bool) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("answer %v want%v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("producer did not settle")
	}
}
func TestConcurrentDialogConfirmations(t *testing.T) {
	m, prog, frames, ctx := dialogProgram(t)
	confirm := m.askYesNo(m.Chat.Seq)
	first, second := make(chan bool, 1), make(chan bool, 1)
	go func() { first <- confirm(ctx, "Alpha check") }()
	dialogShown(t, frames, "Alpha check")
	started := make(chan struct{})
	go func() { close(started); second <- confirm(ctx, "Beta check") }()
	<-started
	// The activity throbber legitimately repaints while approval is pending.
	// Assert ownership and draft stability, not absence of renderer messages.
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
waiting:
	for {
		select {
		case f := <-frames:
			if f.prompt != "Alpha check" || f.draft != "retained draft" {
				t.Fatalf("pending dialog overwritten: %+v", f)
			}
		case <-first:
			t.Fatal("Alpha settled without user approval")
		case <-second:
			t.Fatal("Beta settled before its dialog")
		case <-deadline.C:
			break waiting
		}
	}
	prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	dialogBool(t, first, true)
	dialogShown(t, frames, "Beta check")
	select {
	case <-second:
		t.Fatal("Alpha answer crossed to Beta")
	default:
	}
	prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	dialogBool(t, second, false)
}
func TestConcurrentDialogAuthAskAndCancellation(t *testing.T) {
	m, prog, frames, ctx := dialogProgram(t)
	gate := make(chan struct{}, 1)
	confirm := m.askConfirm(m.Chat.Seq, "", gate)
	query := m.askDialog(m.Chat.Seq, gate)
	first := make(chan bool, 1)
	go func() { first <- confirm(ctx, "approved action") }()
	dialogShown(t, frames, "approved action")
	type response struct {
		answers []ask.Answer
		err     error
	}
	answers := make(chan response, 1)
	go func() { a, e := query(ctx, []ask.Question{{Text: "Raw answer?"}}); answers <- response{a, e} }()
	prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	dialogBool(t, first, true)
	f := dialogShown(t, frames, "Raw answer?")
	if f.draft != "" {
		t.Fatal("Ask did not borrow draft")
	}
	raw := "one\n雪"
	prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(raw), Paste: true})
	prog.Send(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case a := <-answers:
		if a.err != nil || len(a.answers) != 1 || a.answers[0].Picked != raw {
			t.Fatalf("raw answer %+v", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ask stalled")
	}
	if f := dialogShown(t, frames, ""); f.draft != "retained draft" {
		t.Fatalf("draft=%q", f.draft)
	}
	activeCtx, cancelActive := context.WithCancel(ctx)
	defer cancelActive()
	active := make(chan response, 1)
	go func() {
		a, e := query(activeCtx, []ask.Question{{Text: "Cancel raw answer?"}})
		active <- response{a, e}
	}()
	dialogShown(t, frames, "Cancel raw answer?")
	waitingCtx, cancelWaiting := context.WithCancel(ctx)
	wait := make(chan bool, 1)
	go func() { wait <- confirm(waitingCtx, "Must not show") }()
	cancelWaiting()
	dialogBool(t, wait, false)
	// An unrelated channel cannot retire the current Ask.
	foreign := showDialogMsg{kind: "ask", askDone: make(chan []ask.Answer, 1), dismiss: true}
	prog.Send(foreign)
	if f := <-frames; f.prompt != "Cancel raw answer?" {
		t.Fatal("foreign dismissal cleared owner")
	}
	next := make(chan bool, 1)
	go func() { next <- confirm(ctx, "After canceled Ask") }()
	cancelActive()
	select {
	case a := <-active:
		if a.err != context.Canceled {
			t.Fatalf("cancel=%v", a.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active Ask stalled")
	}
	f = dialogShown(t, frames, "After canceled Ask")
	if f.draft != "retained draft" {
		t.Fatal("canceled Ask lost borrowed draft")
	}
	prog.Send(tea.KeyMsg{Type: tea.KeyEsc})
	dialogBool(t, next, false)
}
func TestConcurrentDialogStopQuitRelease(t *testing.T) {
	for _, quit := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "quit"}[quit], func(t *testing.T) {
			m, prog, frames, ctx := dialogProgram(t)
			confirm := m.askYesNo(m.Chat.Seq)
			first, second := make(chan bool, 1), make(chan bool, 1)
			go func() { first <- confirm(ctx, "Current approval") }()
			dialogShown(t, frames, "Current approval")
			go func() { second <- confirm(ctx, "Waiting approval") }()
			if quit {
				prog.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
			} else {
				prog.Send(tea.KeyMsg{Type: tea.KeyCtrlU})
				if f := <-frames; f.draft != "" {
					t.Fatalf("stop fixture did not clear prior draft: %q", f.draft)
				}
				prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/stop")})
				prog.Send(tea.KeyMsg{Type: tea.KeyEnter})
			}
			dialogBool(t, first, false)
			dialogBool(t, second, false)
			if ctx.Err() != context.Canceled {
				t.Fatal("run context not canceled")
			}
			prog.Send(runDoneMsg{seq: m.Chat.Seq})
		})
	}
}
