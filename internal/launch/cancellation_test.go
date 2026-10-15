package launch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
)

type cancellationModel struct {
	started chan struct{}
	calls   atomic.Int32
}

func (m *cancellationModel) Chat(ctx context.Context, _ []llm.Message) (string, string, llm.Usage, error) {
	if m.calls.Add(1) == 1 {
		close(m.started)
	}
	<-ctx.Done()
	return "retained interrupted response", "scripted-component", llm.Usage{}, ctx.Err()
}

func TestCancellationConcurrentAdmissionSettlesWithoutRetry(t *testing.T) {
	s, aid, ids, path := setupOutcome(t, 3)
	m := &cancellationModel{started: make(chan struct{})}
	d := depsFor(s, m)
	d.Sub.Mode, d.Sub.MaxSubagents, d.Wf.MaxRetries = config.SubagentsConcurrent, 1, 3
	d.NewModel = func() agent.Model { return m }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Launch(ctx, d, aid); done <- err }()
	select {
	case <-m.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first task never admitted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("semaphore/worker did not settle")
	}
	s = reopenOutcomeStore(t, s, path)
	attempts := 0
	for _, id := range ids {
		x, err := plan.GetTask(s, id)
		if err != nil {
			t.Fatal(err)
		}
		attempts += x.Attempts
		if x.State != plan.TaskCancelled || x.Execution != outcome.Cancelled || x.Verification == outcome.Passed {
			t.Fatalf("false completion: %+v", x)
		}
	}
	if attempts != 1 || m.calls.Load() != 1 {
		t.Fatalf("late admission/retry: attempts=%d calls=%d", attempts, m.calls.Load())
	}
}
