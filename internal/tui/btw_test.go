package tui

import (
	"context"
	"fmt"
	"specific-harness/internal/session"
	"strings"
	"sync"
	"testing"
)

// TestBtwBoxQueues pins the interjection queue: FIFO pops, empty
// pops blank, drain takes the remainder.
func TestBtwBoxQueues(t *testing.T) {
	b := &btwBox{}
	if got := b.pop(); got != "" {
		t.Errorf("empty pop=%q, want blank", got)
	}
	b.push("first")
	b.push("second")
	if got := b.pop(); got != "first" {
		t.Errorf("pop=%q, want first", got)
	}
	b.push("third")
	if got := b.drain(); len(got) != 2 || got[0] != "second" || got[1] != "third" {
		t.Errorf("drain=%v, want [second third]", got)
	}
	if got := b.pop(); got != "" {
		t.Errorf("drained pop=%q, want blank", got)
	}
}

func TestBtwBoxSignalsOnlyRegisteredGenerationAndRetainsQueuedNote(t *testing.T) {
	b := &btwBox{}
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	child, generationCancel := context.WithCancel(parent)
	b.generating(generationCancel)
	b.push("actual user comment")
	if child.Err() == nil || parent.Err() != nil || !b.pending() || b.pop() != "actual user comment" {
		t.Fatal("interjection failed to pause only generation with a retained comment")
	}
	b.generating(nil)
	b.push("during an effect")
	if parent.Err() != nil || !b.pending() {
		t.Fatal("queued effect-time comment affected owning cancellation")
	}
	child2, cancel2 := context.WithCancel(parent)
	defer cancel2()
	b.generating(cancel2)
	if child2.Err() == nil || b.pop() != "during an effect" {
		t.Fatal("pending note was lost across generation registration")
	}
}

// TestBtwIdleRejected pins the idle guard: /btw with no run in
// flight errors and queues nothing.
func TestBtwIdleRejected(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/btw hello")
	if len(m.Alerts) != 1 || m.Alerts[0].Kind != "err" || !strings.Contains(m.Alerts[0].Text, "no run in flight") {
		t.Errorf("alerts=%+v, want idle err", m.Alerts)
	}
	if len(m.Chat.carry) != 0 {
		t.Errorf("carry=%v, want empty", m.Chat.carry)
	}
}

// TestBtwQueuesWhileRunning pins the live path: /btw during a run
// queues the note without touching Running.
func TestBtwQueuesWhileRunning(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.btw = &btwBox{owner: session.NewID(), archive: t.TempDir()}
	m = submit(m, "/btw watch the loop")
	if got := m.Chat.btw.pop(); got != "watch the loop" {
		t.Errorf("queued=%q, want note", got)
	}
	if !m.Chat.Running {
		t.Error("Running must stay true after /btw")
	}
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "noted") {
		t.Errorf("alerts=%+v, want noted", m.Alerts)
	}
}

// TestBtwBareUsage pins the usage guard even mid-run.
func TestBtwBareUsage(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.btw = &btwBox{owner: session.NewID(), archive: t.TempDir()}
	m = submit(m, "/btw")
	if len(m.Alerts) != 1 || m.Alerts[0].Kind != "err" || !strings.Contains(m.Alerts[0].Text, "usage") {
		t.Errorf("alerts=%+v, want usage err", m.Alerts)
	}
}

// TestDrainBtwCarries pins the carry-over: unheard notes survive the
// run and a sys line says so.
func TestDrainBtwCarries(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.btw = &btwBox{notes: []session.UserNote{{Text: "a"}, {Text: "b"}}}
	m.drainBtw()
	if m.Chat.btw != nil {
		t.Error("box must clear after drain")
	}
	if len(m.Chat.carry) != 2 || m.Chat.carry[0].Text != "a" || m.Chat.carry[1].Text != "b" {
		t.Errorf("carry=%v, want [a b]", m.Chat.carry)
	}
	if len(m.Chat.Lines) != 1 || m.Chat.Lines[0].Kind != lineSys || !strings.Contains(m.Chat.Lines[0].Text, "carried 2") {
		t.Errorf("lines=%+v, want carried-2 sys line", m.Chat.Lines)
	}
}

// TestBtwCarrySurvivesNew pins that /new keeps unheard notes for the
// next run instead of dropping them.
func TestBtwCarrySurvivesNew(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.carry = []session.UserNote{{Text: "straggler"}}
	m = submit(m, "/new")
	if len(m.Chat.carry) != 1 || m.Chat.carry[0].Text != "straggler" {
		t.Errorf("carry=%v, want [straggler]", m.Chat.carry)
	}
}

// TestBtwBoxConcurrent hammers the queue from goroutines: the event
// loop pushes while the run goroutine pops, so the mutex must hold
// under -race and no note may be lost or duplicated.
func TestBtwBoxConcurrent(t *testing.T) {
	b := &btwBox{}
	const writers = 4
	const perWriter = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				b.push(fmt.Sprintf("w%d-%d", w, i))
			}
		}(w)
	}
	wg.Wait()
	got := map[string]int{}
	for {
		n := b.pop()
		if n == "" {
			break
		}
		got[n]++
	}
	if len(got) != writers*perWriter {
		t.Errorf("got %d distinct notes, want %d", len(got), writers*perWriter)
	}
	for n, c := range got {
		if c != 1 {
			t.Errorf("note %q delivered %d times", n, c)
		}
	}
}

// TestBtwEventLine pins the session-log relay: a "btw" event renders
// as a user "btw: ..." line in the transcript.
func TestBtwEventLine(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	um, _ := m.Update(eventMsg{seq: m.Chat.Seq, kind: "btw", text: "slow down"})
	m = um.(Model)
	if len(m.Chat.Lines) != 1 || m.Chat.Lines[0].Kind != lineUser || m.Chat.Lines[0].Text != "btw: slow down" {
		t.Errorf("lines=%+v, want one user btw line", m.Chat.Lines)
	}
}
