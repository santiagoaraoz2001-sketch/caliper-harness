package auth

import (
	"context"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"sync/atomic"
	"testing"
	"time"
)

// Actual shared Gate/Exec, isolated callbacks; no model/server involvement.
func TestLifecycleSharedHumanQuestionsSerialize(t *testing.T) {
	entered, release, second := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var calls atomic.Int32
	g := &Gate{Mode: config.AuthAll, AskFunc: func(ctx context.Context, prompt string) bool {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return true
		}
		second <- struct{}{}
		return false
	}}
	exec := g.Wrap(agent.Direct)
	def := tool.Definition{Name: "actual", Execute: func(context.Context, tool.Args) (tool.Result, error) { return tool.Result{}, nil }}
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(firstDone); exec(t.Context(), def, tool.Args{"task": "first"}) }()
	<-entered
	go func() { defer close(secondDone); exec(t.Context(), def, tool.Args{"task": "next"}) }()
	overlap := false
	select {
	case <-second:
		overlap = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-firstDone
	<-secondDone
	if overlap {
		t.Fatal("next task's human question overlapped the pending owning approval")
	}
}

func TestLifecycleQueuedCancelledApprovalNeverAsksOrExecutes(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, effects atomic.Int32
	g := &Gate{Mode: config.AuthAll, AskFunc: func(ctx context.Context, prompt string) bool {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return false
		}
		<-release
		return true
	}}
	def := tool.Definition{Name: "actual", Execute: func(context.Context, tool.Args) (tool.Result, error) { effects.Add(1); return tool.Result{}, nil }}
	exec := g.Wrap(agent.Direct)
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); exec(t.Context(), def, tool.Args{"task": "first"}) }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	secondDone := make(chan error, 1)
	go func() { _, err := exec(ctx, def, tool.Args{"task": "cancelled"}); secondDone <- err }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	settled := false
	select {
	case err := <-secondDone:
		settled = err != nil
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-firstDone
	if !settled {
		<-secondDone
	}
	if !settled || calls.Load() != 1 || effects.Load() != 0 {
		t.Fatalf("queued cancelled question crossed authority: settled=%v asks=%d effects=%d", settled, calls.Load(), effects.Load())
	}
}
