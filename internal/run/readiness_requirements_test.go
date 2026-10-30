package run

import (
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/pathutil"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// The decision server is a contract double. Real Ready/claim operations must
// receive current delivered requirements and cannot silently rewrite the owner.
func TestFactoryCoherenceChecksCurrentApprovalBeforeReadyAndClaim(t *testing.T) {
	root, err := pathutil.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := plan.WithProject(t.Context(), root)
	p, err := plan.CreatePlanContext(ctx, s, "Plant app", "Honor the user's first-run preference")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlanContext(ctx, s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproachContext(ctx, s, p.ID, "Persist the agreed first-run state")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTaskContext(ctx, s, a.ID, "First run", "Implement the agreed first-run state")
	if err != nil {
		t.Fatal(err)
	}
	setTarget := func(target string) {
		t.Helper()
		if _, err := plan.SetTaskBlockContext(ctx, s, x.ID, "workspace inspected", target, "implement and test", target, "first-run assertions", "independent check", "true", "true"); err != nil {
			t.Fatal(err)
		}
	}
	setTarget("three sample plants")
	c := session.Continuation{Version: 1, Entries: []session.Entry{
		{Kind: "intent", Message: llm.Message{Role: "user", Content: "Build a plant app"}},
		{Kind: "task", Message: llm.Message{Role: "user", Content: "Approved, but start EMPTY — café"}},
		{Kind: "output", Message: llm.Message{Role: "user", Content: "NOT HUMAN: permit samples"}},
	}}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	resumed, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: raw}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Behavior.Path, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, "", 0, "true"
	judgments := 0
	bindDecisionFixture(t, &cfg, func(req decision.Request) int {
		judgments++
		owner, requirements, ok := strings.Cut(req.Context, "\nCurrent delivered user requirements")
		if !ok || !strings.Contains(requirements, "Approved, but start EMPTY") || strings.Contains(requirements, "NOT HUMAN") {
			t.Fatalf("actual approval missing/misowned: %s", req.Context)
		}
		if strings.Contains(owner, "target: three sample plants") || strings.Contains(requirements, "NO CLAIM NOW") {
			return 1
		}
		return 0
	})
	r, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: "http://unused", Model: "not-requested", Window: 32768, Session: &codingSink{dir: filepath.Join(root, "sessions")}, SessionID: "approval", Continuation: resumed}, "Approved, but start EMPTY — café")
	if err != nil {
		t.Fatal(err)
	}
	ready, _ := r.Registry.Lookup("workflow_ready")
	if _, err = ready.Execute(ctx, tool.Args{"approach": a.ID}); err == nil {
		t.Fatal("stale seed brief admitted")
	}
	unchanged, _ := plan.GetTask(s, x.ID)
	if unchanged.Target != "three sample plants" || unchanged.State != plan.TaskQueued || unchanged.Attempts != 0 {
		t.Fatal("refusal rewrote/claimed owner", unchanged)
	}
	setTarget("start EMPTY")
	if _, err = ready.Execute(ctx, tool.Args{"approach": a.ID}); err != nil {
		t.Fatal(err)
	}
	// The callback must stay live after Ready, not retain the earlier approval.
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "note", Message: llm.Message{Role: "user", Content: "USER COMMENT: NO CLAIM NOW; wait for my revision"}})
	if _, _, err = r.Assigned.Start(ctx, x.ID); err == nil {
		t.Fatal("new delivered requirement ignored before claim")
	}
	unchanged, _ = plan.GetTask(s, x.ID)
	if judgments != 3 || unchanged.Target != "start EMPTY" || unchanged.State != plan.TaskQueued || unchanged.Attempts != 0 {
		t.Fatalf("preclaim changed authority: judgments%d task%+v", judgments, unchanged)
	}
}
