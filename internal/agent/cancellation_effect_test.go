package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"testing"
	"time"
)

// Scripted test stimulus only; actual registered Bash execution is never mocked.
type cancellationStimulus struct {
	reply  string
	cancel context.CancelFunc
	calls  int
}

func (m *cancellationStimulus) Chat(ctx context.Context, _ []llm.Message) (string, string, llm.Usage, error) {
	m.calls++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	return m.reply, "no-inference-test-stimulus", llm.Usage{}, nil
}
func cancellationObserve(t *testing.T, name string, m map[string]any) {
	t.Helper()
	b, _ := json.Marshal(m)
	t.Log(name + ": " + string(b))
}
func cancellationRunner(model *cancellationStimulus) *Runner {
	reg := tool.NewRegistry()
	reg.Register(bash.New(config.Defaults().Tools.Bash))
	return &Runner{Model: model, Registry: reg, MaxSteps: 1, Out: io.Discard}
}
func TestR04ModelBoundaryRefusesLateEffect(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "after-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &cancellationStimulus{reply: "```tool\ntool: bash\ncmd: printf LATE > '" + marker + "'\n```", cancel: cancel}
	r := cancellationRunner(model)
	_, _, err := r.Run(ctx, "boundary test")
	if err == nil || r.Outcome.ExitCode() != 130 {
		t.Fatalf("cancellation did not settle: %v, %+v", err, r.Outcome)
	}
	b, readErr := os.ReadFile(marker)
	cancellationObserve(t, "model-boundary", map[string]any{"context_cancelled": ctx.Err() != nil, "marker_exists": readErr == nil, "marker_bytes": string(b), "model_calls": model.calls, "run_error": err != nil, "inference": false})
	if readErr == nil {
		t.Error("tool started after context cancellation and created LATE marker")
	}
}
func TestR04AdmittedBashHonorsCancel(t *testing.T) {
	root := t.TempDir()
	started := filepath.Join(root, "started")
	late := filepath.Join(root, "late")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &cancellationStimulus{reply: "```tool\ntool: bash\ncmd: printf START > '" + started + "'; sleep 0.5; printf LATE > '" + late + "'\n```"}
	r := cancellationRunner(model)
	done := make(chan struct{})
	go func() { r.Run(ctx, "admitted test"); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("own command never admitted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	at := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("owned command exceeded bounded probe")
	}
	b, err := os.ReadFile(late)
	cancellationObserve(t, "admitted-bash", map[string]any{"marker_exists": err == nil, "marker_bytes": string(b), "settled_after_cancel_ms": time.Since(at).Milliseconds(), "model_calls": model.calls, "inference": false})
	if err == nil {
		t.Error("already admitted Bash kept writing after cancellation")
	}
}
func TestR04TimeoutContainsShellChild(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "child-late")
	pidfile := filepath.Join(root, "child-pid")
	cfg := config.Defaults().Tools.Bash
	cfg.DefaultTimeout = 1
	def := bash.New(cfg)
	cmd := "sh -c \"sleep 2; printf CHILD_LATE > '" + marker + "'\" & printf '%s' $! > '" + pidfile + "'; wait"
	start := time.Now()
	res, err := def.Execute(context.Background(), tool.Args{"cmd": cmd, "timeout": int64(1)})
	elapsed := time.Since(start)
	if remaining := time.Until(start.Add(2200 * time.Millisecond)); remaining > 0 {
		time.Sleep(remaining)
	}
	b, readErr := os.ReadFile(marker)
	pid, _ := os.ReadFile(pidfile)
	cancellationObserve(t, "shell-child-timeout", map[string]any{"child_pid": string(pid), "marker_exists": readErr == nil, "marker_bytes": string(b), "elapsed_ms": elapsed.Milliseconds(), "timeout_seconds": 1, "error": err != nil, "result_text": res.Text, "inference": false, "cleanup": "natural command settlement; no manual process kills"})
	if readErr == nil {
		t.Error("owned child wrote after shell timeout")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("timeout settlement waited for surviving child: %v", elapsed)
	}
}
func TestR04UncancelledBashControl(t *testing.T) {
	cfg := config.Defaults().Tools.Bash
	res, err := bash.New(cfg).Execute(context.Background(), tool.Args{"cmd": "printf CONTROL", "timeout": int64(2)})
	if err != nil || res.Failed || res.Text != "CONTROL" {
		t.Fatal("probe control failed")
	}
}
