package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/tools/ask"
)

func labelsAsk(m Model, questions []ask.Question) (Model, chan []ask.Answer) {
	done := make(chan []ask.Answer, 1)
	next, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: questions[0].Text, questions: questions, askDone: done})
	return next.(Model), done
}

func labelsView(t *testing.T, m Model, title, question, stale string) {
	t.Helper()
	view := dialogView(m)
	if !strings.Contains(view, title) || !strings.Contains(view, question) || (stale != "" && strings.Contains(view, stale)) {
		t.Errorf("current question missing or stale label retained: %q", view)
	}
}

func TestAskLabelsMixedActualUpdates(t *testing.T) {
	qs := []ask.Question{{Text: "Choose language?", Options: []string{"Go", "Python"}}, {Text: "Project codeword?"}, {Text: "Choose finish?", Options: []string{"Ship", "Wait"}}}
	m, done := labelsAsk(New(testCfg(), "test"), qs)
	labelsView(t, m, "Question 1/3", qs[0].Text, "")
	m = key(m, "down")
	m = key(m, "tab")
	m = key(m, "α comment")
	m = enter(m)
	labelsView(t, m, "Question 2/3", qs[1].Text, qs[0].Text)
	if m.Chat.Dialog.PickIdx != 0 || m.Chat.Dialog.Commenting || m.Chat.Dialog.Comment != "" {
		t.Fatal("option/comment state not reset between questions")
	}
	m = key(m, "codeword β")
	m = enter(m)
	labelsView(t, m, "Question 3/3", qs[2].Text, qs[1].Text)
	m = enter(m)
	want := []ask.Answer{{Picked: "Python", Comment: "α comment"}, {Picked: "codeword β"}, {Picked: "Ship"}}
	if got := <-done; !reflect.DeepEqual(got, want) || m.Chat.Dialog != nil || m.Input.Value() != "" {
		t.Fatalf("answers/settlement got %#v, want %#v", got, want)
	}
}

func TestAskLabelsFreeActualUpdates(t *testing.T) {
	qs := []ask.Question{{Text: "First identifier?"}, {Text: "Second identifier?"}}
	m, done := labelsAsk(New(testCfg(), "test"), qs)
	labelsView(t, m, "Question 1/2", qs[0].Text, "")
	raw := " \tfirst α\n\nlast  "
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(raw), Paste: true})
	m = enter(next.(Model))
	labelsView(t, m, "Question 2/2", qs[1].Text, qs[0].Text)
	m = key(m, "second β")
	m = enter(m)
	if got := <-done; !reflect.DeepEqual(got, []ask.Answer{{Picked: raw}, {Picked: "second β"}}) {
		t.Fatalf("free answers changed/routed incorrectly: %#v", got)
	}
}

func TestAskLabelsCancelNewAndDraftBoundary(t *testing.T) {
	m, done := labelsAsk(New(testCfg(), "test"), []ask.Question{{Text: "Old option?", Options: []string{"Old"}}})
	m = key(m, "tab")
	m = key(m, "old comment")
	m = key(m, "esc") // Leaves comment editing, not the dialog.
	if m.Chat.Dialog == nil || m.Chat.Dialog.Commenting {
		t.Fatal("comment Esc did not leave editing only")
	}
	m = key(m, "esc")
	if got := <-done; got != nil || m.Chat.Dialog != nil {
		t.Fatal("cancel did not decline dialog")
	}
	m, done = labelsAsk(m, []ask.Question{{Text: "Fresh question?", Options: []string{"Fresh"}}})
	labelsView(t, m, "Question 1/1", "Fresh question?", "Old option?")
	if d := m.Chat.Dialog; d.QIdx != 0 || d.PickIdx != 0 || d.Comment != "" || len(d.Answers) != 0 {
		t.Fatal("new dialog retained old state")
	}
	m = enter(m)
	if got := <-done; !reflect.DeepEqual(got, []ask.Answer{{Picked: "Fresh"}}) {
		t.Fatalf("fresh dialog answer = %#v", got)
	}
	// Cancelling either question kind must preserve the preexisting draft.
	for _, options := range [][]string{nil, {"Pick"}} {
		m.Input.SetValue("existing draft α")
		m, done = labelsAsk(m, []ask.Question{{Text: "Cancelled question?", Options: options}})
		m = key(m, "esc")
		if got := <-done; got != nil || m.Input.Value() != "existing draft α" {
			t.Fatal("cancel destroyed shared composer draft")
		}
	}
}

func TestAskDraftCancelAndStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "esc", true: "stop"}[stop], func(t *testing.T) {
			m := New(testCfg(), "test")
			draft := " \tprior α\n\nlast  "
			m.Input.SetValue(draft)
			m, done := labelsAsk(m, []ask.Question{{Text: "Cancelled answer?"}})
			if m.Input.Value() != "" {
				t.Errorf("free Ask did not isolate prior draft: %q", m.Input.Value())
			}
			m = key(m, "STALE cancelled β")
			if stop {
				m.Chat.Running = true
				m.Input.SetValue("/stop")
				m = enter(m)
			} else {
				m = key(m, "esc")
			}
			if got := <-done; got != nil || m.Chat.Dialog != nil || m.Input.Value() != draft {
				t.Errorf("cancel/stop did not restore prior draft: %q", m.Input.Value())
			}
			// A new real run owns a new sequence, after stop settlement.
			m.Chat.Running, m.Chat.stopFlag = false, false
			m.Chat.Seq++
			m, done = labelsAsk(m, []ask.Question{{Text: "Fresh answer?"}})
			m = key(m, "FRESH γ")
			m = enter(m)
			if got := <-done; !reflect.DeepEqual(got, []ask.Answer{{Picked: "FRESH γ"}}) || m.Input.Value() != draft {
				t.Errorf("fresh answer contaminated or original draft lost: %#v; draft %q", got, m.Input.Value())
			}
		})
	}
}

func TestAskDraftTransitionsAndShortcuts(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("prior draft α")
	m.Drafts = [3]string{"stored zero", "stored one", "stored two"}
	m.Hist, m.HistDraft = []string{"historic"}, "history draft"
	m, done := labelsAsk(m, []ask.Question{{Text: "Option first?", Options: []string{"A"}}, {Text: "Free one?"}, {Text: "Free two?"}, {Text: "Option last?", Options: []string{"B"}}})
	if m.Input.Value() != "prior draft α" {
		t.Fatal("option Ask changed shared draft")
	}
	m = key(m, " extended")
	m = enter(m) // Option → free borrows the draft edited on options.
	draft := "prior draft α extended"
	if m.Input.Value() != "" {
		t.Errorf("option→free did not expose blank answer: %q", m.Input.Value())
	}
	m = key(m, "answer β")
	for _, typ := range []tea.KeyType{tea.KeyShiftLeft, tea.KeyShiftRight, tea.KeyShiftUp, tea.KeyShiftDown} {
		before, draftIdx, histIdx := m.Input.Value(), m.Draft, m.HistIdx
		next, _ := m.Update(tea.KeyMsg{Type: typ})
		m = next.(Model)
		if m.Input.Value() != before || m.Draft != draftIdx || m.HistIdx != histIdx || m.Drafts != [3]string{"stored zero", "stored one", "stored two"} || m.HistDraft != "history draft" {
			t.Errorf("free answer shortcut %v changed composer/storage", typ)
		}
	}
	m = enter(m) // Free → free keeps the original borrow, not an empty draft.
	if m.Input.Value() != "" {
		t.Error("second free question retained first answer")
	}
	m = key(m, "answer γ")
	m = enter(m) // Free → option restores before completing the chain.
	if m.Input.Value() != draft {
		t.Errorf("free→option did not restore draft: %q", m.Input.Value())
	}
	m = enter(m)
	if got := <-done; !reflect.DeepEqual(got, []ask.Answer{{Picked: "A"}, {Picked: "answer β"}, {Picked: "answer γ"}, {Picked: "B"}}) {
		t.Errorf("transition answers wrong: %#v", got)
	}
	m = key(m, "shift+right")
	if m.Draft != 1 || m.Drafts[0] != draft || m.Input.Value() != "stored one" {
		t.Fatal("ordinary draft shortcut did not resume")
	}
	m = key(m, "shift+up")
	if m.Input.Value() != "historic" {
		t.Fatal("ordinary history shortcut did not resume")
	}
}

func TestAskLabelsLegacyAndAuthFallback(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Dialog = &Dialog{Kind: "ask", Prompt: "Legacy question?"}
	labelsView(t, m, "Question", "Legacy question?", "Question 1/")
	answer := make(chan string, 1)
	next, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "Approve action?", answer: answer})
	m = next.(Model)
	labelsView(t, m, "Auth", "Approve action?", "Legacy question?")
	m.Input.SetValue("auth draft")
	m = key(m, "n")
	if got := <-answer; got != "n" || m.Chat.Dialog != nil || m.Input.Value() != "auth draft" {
		t.Fatal("auth fallback/answer/draft changed")
	}
}
