package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools"
	"specific-harness/internal/tools/files"
)

func TestAdvisoryRecipeIsNotOwningProgress(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "actual plan", "actual goal")
	if err != nil {
		t.Fatal(err)
	}
	preparation := "read actual chosen plan"
	r := &Runner{Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
		actual, e := plan.GetPlan(s, p.ID)
		if e != nil {
			return nil, e
		}
		raw, e := json.Marshal(actual)
		return BookkeepingFacts{Unfinished: raw, Preparation: preparation}, e
	}}}
	r.enforcement.repair = "actual pending bookkeeping"
	ctx := context.Background()
	if !r.bookkeepingProgress(ctx, true) {
		t.Fatal("initial actual facts not seeded")
	}
	preparation = "different advisory target/recipe only"
	if r.bookkeepingProgress(ctx, true) {
		t.Fatal("advice-only change counted as owning progress")
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	if !r.bookkeepingProgress(ctx, true) {
		t.Fatal("actual owning state change disappeared")
	}
	if r.bookkeepingProgress(ctx, true) {
		t.Fatal("unchanged facts counted twice")
	}
}

func TestEnforcementReceivesProtectedUserRequestsAndActualFinal(t *testing.T) {
	const original = "Repair the requested behavior; record supported product knowledge. κ\r\n"
	const answer = "Provisional model claim: fixed; check remains unverified. β\n"
	for _, tc := range []struct {
		name, request string
		resume        bool
	}{{"fresh", original, false}, {"distinct resumed request", "Resume: preserve failures and explain the actual result. é\r\n", true}, {"same resumed request", original, true}} {
		t.Run(tc.name, func(t *testing.T) {
			request := tc.request
			var c session.Continuation
			if tc.resume {
				c.Version = 1
				c.Add("intent", "user", original, false)
				c.Add("task", "user", "obsolete explicit request", false)
				c.Add("summary", "user", "Unverified summary invents a different requirement", false)
				c.Add("evidence", "user", "Host guidance is not a new user request", false)
				raw, err := c.Encode()
				if err != nil {
					t.Fatal(err)
				}
				c, _, err = session.Restore([]session.Event{{Kind: "continuation", Text: raw}}, 32768)
				if err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "observed.txt"), []byte("actual observation κ\n"), 0600); err != nil {
				t.Fatal(err)
			}
			registry := tool.NewRegistry()
			workspace := &files.Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
			for _, d := range workspace.Definitions() {
				registry.Register(d)
			}
			model := &scriptModel{replies: []string{"```tool\ntool: read\npath: observed.txt\noffset: 1\nlimit: 1\n```", answer}}
			r := &Runner{Model: model, Registry: registry, MaxSteps: 2,
				Continuation: c, Session: &recordSink{}, Assigned: &AssignedOps{Reset: func() {}, Facts: func(context.Context) (any, error) {
					return map[string]string{"TaskPrompt": "model-authored narrower task", "State": "unclaimed"}, nil
				}}}
			r.Continuation.Workflow = &session.WorkflowScope{Store: "controlled-store", Kind: "root"}
			var seen []bool
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				t.Logf("captured decision context: %s", q.Context)
				var input struct {
					OriginalUserIntent, CurrentUserRequest, ProvisionalModelAnswer string
					NormalFinal                                                    bool
				}
				if err := json.Unmarshal([]byte(q.Context), &input); err != nil {
					t.Fatal(err)
				}
				current, final := "", ""
				if request != original {
					current = request
				}
				if input.NormalFinal {
					final = answer
				}
				if input.OriginalUserIntent != original || input.CurrentUserRequest != current || input.ProvisionalModelAnswer != final {
					t.Errorf("actual captured decision lost provenance: %+v", input)
				}
				seen = append(seen, input.NormalFinal)
				return decision.Verdict{Option: coherent, Confidence: .9}, nil
			}
			text, _, err := r.Run(context.Background(), request)
			if err != nil || text != answer || r.Outcome.Status != outcome.Stopped || len(model.seen) != 2 || len(seen) != 2 || seen[0] || !seen[1] {
				t.Fatalf("control changed: text=%q outcome=%+v generations=%d decisions=%v error=%v", text, r.Outcome, len(model.seen), seen, err)
			}
		})
	}
}

// Actual registered discovery/Kuzu facts/watcher; no model or judge inference.
func TestUnclaimedDiscoveryProgressWhileRepairPending(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	for name, text := range map[string]string{"seen.txt": "seen source\n", "new.txt": "new source\n", "paged.txt": "first\nsecond\n", "claimed.txt": "claimed source\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "actual discovery task", "ground the authored plan in source")
	if err != nil {
		t.Fatal(err)
	}
	advice := "pending PAT recipe"
	r := &Runner{Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
		actual, e := plan.GetPlan(s, p.ID)
		if e != nil {
			return nil, e
		}
		raw, e := json.Marshal(actual)
		return BookkeepingFacts{Unfinished: raw, Preparation: advice}, e
	}}}
	r.enforcement.repair = "pending PAT repair"
	if !r.bookkeepingProgress(ctx, true) {
		t.Fatal("initial owning facts not seeded")
	}
	reg := tool.NewRegistry()
	cfg := config.Defaults()
	tools.RegisterAt(reg, cfg, strings.NewReader(""), io.Discard, nil, &files.Workspace{Path: dir, Caps: cfg.Tools.Bash})
	exec := r.assignmentExec(Direct)
	call := func(callCtx context.Context, name string, slots tool.Args) (bool, string) {
		t.Helper()
		d, ok := reg.Lookup(name)
		if !ok {
			t.Fatal(name)
		}
		raw, _ := json.Marshal(slots)
		a, refusal := tool.ValidateJSON(d, string(raw))
		if refusal != "" {
			t.Fatal(refusal)
		}
		r.enforcement.current = nil // Same per-call boundary as native and text dispatch.
		_, sig, productive := r.invokeValidated(callCtx, d, a, exec)
		return r.bookkeepingProgress(callCtx, productive), sig
	}
	r.enforcement.repair = ""
	if ok, _ := call(ctx, "read", tool.Args{"path": "seen.txt"}); !ok {
		t.Fatal("successful initial read lost")
	}
	r.enforcement.repair = "pending PAT repair"
	if ok, _ := call(ctx, "read", tool.Args{"path": "seen.txt"}); ok {
		t.Fatal("previous source became new after advice")
	}
	var watch watcher
	for i := 0; i < 4; i++ {
		ok, sig := call(ctx, "find_file", tool.Args{"pattern": "absent*"})
		if ok || watch.observe(sig, ok, 0, 5) != "" {
			t.Fatal("empty query falsely reset/tripped stall")
		}
	}
	ok, sig := call(ctx, "find_file", tool.Args{"pattern": "new.txt"})
	if !ok || watch.observe(sig, ok, 0, 5) != "" || watch.nonprod != 0 {
		t.Fatal("new actual Find was not observed progress while PAT repair remained pending")
	}
	foundText := r.enforcement.current.Result.Text
	if ok, _ := call(ctx, "find_file", tool.Args{"pattern": "new*"}); ok || r.enforcement.current.Result.Text != foundText {
		t.Fatal("argument novelty counted without result novelty")
	}
	if ok, _ := call(ctx, "read", tool.Args{"path": "new.txt"}); !ok {
		t.Fatal("actual Find to Read lost new source progress")
	}
	if r.enforcement.repair != "pending PAT repair" || r.assignedClaim || r.Continuation.Assignment != nil {
		t.Fatal("discovery cleared repair or fabricated claim")
	}
	watch.reset()
	for i := 0; i < 5; i++ {
		name, slots := "find_file", tool.Args{"pattern": "new.txt"}
		if i%2 == 1 {
			name, slots = "read", tool.Args{"path": "new.txt"}
		}
		ok, sig := call(ctx, name, slots)
		trip := watch.observe(sig, ok, 0, 5)
		if ok || (trip != "") != (i == 4) {
			t.Fatalf("alternating rereads fabricated progress: %d %v %q", i, ok, trip)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("changed actual source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := call(ctx, "read", tool.Args{"path": "new.txt"}); !ok {
		t.Fatal("actual new result with identical arguments was lost")
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := call(ctx, "read", tool.Args{"path": "new.txt"}); ok {
		t.Fatal("returning to an already observed source fabricated freshness")
	}
	if ok, _ := call(ctx, "find_dir", tool.Args{"path": ".", "pattern": "absent*"}); ok || !r.enforcement.current.Result.NoMatches {
		t.Fatal("zero directory page counted as fresh discovery")
	}
	if ok, _ := call(ctx, "read", tool.Args{"path": "missing.txt"}); ok || r.enforcement.current.Error == "" {
		t.Fatal("read error counted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if ok, _ := call(cancelled, "read", tool.Args{"path": "claimed.txt"}); ok {
		t.Fatal("cancelled read counted")
	}
	if ok, _ := call(ctx, "read", tool.Args{"path": "paged.txt", "limit": 1}); !ok || !r.enforcement.current.Result.Truncated {
		t.Fatal("bounded actual truncated discovery lost")
	}
	if ok, _ := call(ctx, "read", tool.Args{"path": "paged.txt", "limit": 1}); ok {
		t.Fatal("same truncated result counted again")
	}
	r.assignedClaim = true
	if ok, _ := call(ctx, "read", tool.Args{"path": "claimed.txt"}); !ok {
		t.Fatal("actual claimed source discovery lost progress")
	}
	r.assignedClaim = false
	for _, note := range []observed{
		{Tool: "read"}, {Tool: "read", Result: tool.Result{Text: "failed", Failed: true}},
		{Tool: "read", Result: tool.Result{Text: "blocked", Blocked: true}}, {Tool: "read", Result: tool.Result{Text: "partial"}, Error: "actual error"},
	} {
		r.enforcement.current = &note
		if r.bookkeepingProgress(ctx, true) {
			t.Fatalf("invalid discovery credited: %+v", note)
		}
	}
	r.enforcement.current = &observed{Tool: "read", Result: tool.Result{Text: "nonproductive actual receipt"}}
	if r.bookkeepingProgress(ctx, false) {
		t.Fatal("nonproductive receipt counted")
	}
	r.enforcement.current = nil
	advice = "changed host recipe only"
	if r.bookkeepingProgress(ctx, true) {
		t.Fatal("host guidance changed owning facts")
	}
	if _, err := plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	if !r.bookkeepingProgress(ctx, true) {
		t.Fatal("actual Graph progress lost")
	}
}
