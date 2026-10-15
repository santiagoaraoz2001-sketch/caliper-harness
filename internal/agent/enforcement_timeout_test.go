package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

// A timed-out PAT/product enforcer judgment is advisory: it must be recorded,
// not retried for the same evidence, and never settle or stop the executor.
func TestEnforcementJudgeTimeoutDegradesToNoAdvice(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "observed.txt"), []byte("actual observation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	workspace := &files.Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	for _, d := range workspace.Definitions() {
		registry.Register(d)
	}
	model := &scriptModel{replies: []string{"```tool\ntool: read\npath: observed.txt\noffset: 1\nlimit: 1\n```", "final answer"}}
	sink := &recordSink{}
	r := &Runner{Model: model, Registry: registry, MaxSteps: 2,
		Session: sink, SessionID: "enforcement-timeout",
		Assigned: &AssignedOps{Reset: func() {}, Facts: func(context.Context) (any, error) {
			return map[string]string{"State": "claimed"}, nil
		}}}
	r.Continuation.Workflow = &session.WorkflowScope{Store: "controlled-store", Kind: "root"}
	enforcerAttempts := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		if len(q.Options) == len(enforceOptions) {
			enforcerAttempts++
			return decision.Verdict{}, errors.New("decision judgment at http://127.0.0.1:9 timed out: context deadline exceeded")
		}
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}
	text, _, err := r.Run(context.Background(), "repair the requested behavior")
	if err != nil || text != "final answer" || r.Outcome.Status == outcome.Failed {
		t.Fatalf("enforcer timeout stopped executor: text=%q outcome=%+v err=%v", text, r.Outcome, err)
	}
	// Two distinct evidence packets (post-tool and final) are each judged
	// once; the digest recorded on failure suppresses judging either again.
	if enforcerAttempts != 2 {
		t.Fatalf("unexpected enforcer attempts: %d", enforcerAttempts)
	}
	failures, guidance := 0, 0
	for i, kind := range sink.kinds {
		switch kind {
		case "judgment-error":
			failures++
			if !strings.Contains(sink.texts[i], "pat-product-enforcer") || !strings.Contains(sink.texts[i], "timed out") {
				t.Fatal("enforcer failure not preserved:", sink.texts[i])
			}
		case "guidance":
			guidance++
		}
	}
	if failures != enforcerAttempts || guidance != 0 {
		t.Fatalf("timeout evidence wrong: judgment-errors=%d guidance=%d", failures, guidance)
	}
}
