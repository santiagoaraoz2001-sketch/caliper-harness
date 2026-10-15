package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"specific-harness/internal/session"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func TestPendingPATRepairSurvivesNewReadAndCoherentVerdict(t *testing.T) {
	ctx := context.Background()
	facts := BookkeepingFacts{Unfinished: []byte(`{"task":"actual-task","state":"queued"}`), Preparation: "repair actual-task, then request Ready"}
	r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "controlled-store", Kind: "root"}}, Threshold: .7, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	r.enforcement.latest = &observed{Tool: "read", Args: tool.Args{"path": "owner.go"}, Result: tool.Result{Text: "actual source page one"}}
	judgments := 0
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		judgments++
		option := coherent
		if judgments == 1 {
			option = enforceOptions[1]
		}
		return decision.Verdict{Option: option, Confidence: .9}, nil
	}
	log := func(string, string) error { return nil }
	first, err := r.enforce(ctx, false, "", log)
	if err != nil || !strings.Contains(first, "actual-task") {
		t.Fatalf("missing initial concrete advice: %q %v", first, err)
	}
	r.enforcement.latest = &observed{Tool: "read", Args: tool.Args{"path": "other.go"}, Result: tool.Result{Text: "new actual source page"}}
	second, err := r.enforce(ctx, false, "", log)
	if err != nil || second != "" || r.enforcement.prepare {
		t.Fatalf("Continue reissued a prior task nudge: %q %v", second, err)
	}
	facts.Unfinished = []byte(`{"task":"actual-task","state":"ready"}`)
	third, err := r.enforce(ctx, false, "", log)
	if err != nil || third != "" || r.enforcement.prepare || judgments != 3 {
		t.Fatalf("actual task progress cannot settle advice: %q %v judgments=%d", third, err, judgments)
	}
}

func TestProgressCheckpointSeparatesStateFromReceiptProvenance(t *testing.T) {
	page := "owner.go sha256=" + fmt.Sprintf("%x", sha256.Sum256([]byte("source"))) + "\nactual source\n[1 lines shown from line 9; shown_range=9-9; EOF=false; truncated=true; metadata not source lines]"
	facts := BookkeepingFacts{Unfinished: []byte(`{"task":"selected","state":"queued"}`), Preparation: "ready_task(task=selected)", Checkpoint: ProgressCheckpoint{Phase: "PREPARE", PATStep: "task-queued", SelectedID: "selected", MissingFact: "unknown"}}
	r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "controlled-store", Kind: "root"}}, Prompts: prompts.Builtin(), MaxSteps: 25, MaxRunSeconds: 1200, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	r.progressCheckpoint(7, time.Now().Add(-65*time.Second))
	r.enforcement.latest = &observed{Tool: "read", Args: tool.Args{"path": "owner.go", "offset": int64(9)}, Result: tool.Result{Text: page, Truncated: true}}
	r.enforcement.discovery = r.enforcement.latest
	judgments := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		var input struct {
			Checkpoint ProgressCheckpoint
			Latest     *loopObservation
		}
		if err := json.Unmarshal([]byte(q.Context), &input); err != nil {
			t.Fatal(err)
		}
		p := input.Checkpoint
		if p.Phase != "PREPARE" || p.PATStep != "task-queued" || p.SelectedID != "selected" || p.MissingFact != "unknown" || p.RunStep != 7 || p.StepLimit != 25 || p.ElapsedSeconds < 65 || p.NextAction != facts.Preparation {
			t.Fatalf("owner checkpoint lost: %+v", p)
		}
		if p.RecentSource == nil || p.RecentSource.Range != "9-9" || p.RecentSource.FileSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("source"))) || p.RecentSource.ReceiptSHA256 != input.Latest.OutputSHA256 || strings.Contains(q.Context, "actual source") {
			t.Fatalf("provenance/source body boundary lost: %s", q.Context)
		}
		return decision.Verdict{Option: coherent, Confidence: .9}, nil
	}
	if _, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	r.progressCheckpoint(8, time.Now().Add(-80*time.Second))
	if _, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil || judgments != 1 {
		t.Fatalf("clock alone retriggered a judgment: %d %v", judgments, err)
	}
	if r.enforcement.latest.Result.Text != page {
		t.Fatal("compact judge view changed the original source result")
	}
}
