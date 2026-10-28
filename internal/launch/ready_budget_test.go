package launch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
)

func TestReadinessBudgetMatchesActualRequest(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	d.CoherenceMaxChars = 3000
	var got decision.Request
	d.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		got = q
		return decision.Verdict{Option: q.Options[0], Confidence: 1}, nil
	}
	size, maximum, over, err := ReadinessBudget(d, aid)
	if err != nil || maximum != 3000 || over != 0 {
		t.Fatalf("budget=%d/%d +%d %v", size, maximum, over, err)
	}
	if err = ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if size != len(raw) {
		t.Fatalf("reported=%d actual=%d", size, len(raw))
	}
	// One byte over the exact boundary refuses before any judgment; no fields are dropped.
	calls := 0
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{}, nil
	}
	d.CoherenceMaxChars = size - 1
	next, maximum, over, err := ReadinessBudget(d, aid)
	if err != nil || next != size || maximum != size-1 || over != 1 || calls != 0 {
		t.Fatalf("nonjudging diagnostic: %d/%d +%d %v calls=%d", next, maximum, over, err, calls)
	}
	if err = ReadyCheck(context.Background(), d, aid); err == nil || !strings.Contains(err.Error(), "no judgment ran") || calls != 0 {
		t.Fatalf("boundary: %v calls=%d", err, calls)
	}
	d.CoherenceMaxChars = 0
	_, _, over, err = ReadinessBudget(d, aid)
	if err != nil || over != 0 {
		t.Fatal("unbounded budget", over, err)
	}
}

func TestReadinessEditableOptionsAndCompleteBytes(t *testing.T) {
	_, aid, d := gateDeps(t, 1)
	yaml := prompts.Builtin().YAML()
	yaml = strings.Replace(yaml, servesGoal, "all requested tasks are bounded and serve the goal", 1)
	yaml = strings.Replace(yaml, offGoal, "at least one task needs scope or behavior revision", 1)
	yaml = strings.Replace(yaml, "Are all tasks goal-serving", "Are évery task goal-serving", 1)
	file := filepath.Join(t.TempDir(), "prompts.yaml")
	if err := os.WriteFile(file, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	d.Prompts, err = prompts.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	p := &plan.Plan{Goal: "quoted \"goal\" é"}
	tasks := []*plan.Task{{Title: "task", Prompt: "line\nsecond", Current: "current", Target: "target", Sketch: "sketch", Acceptance: "accept", Tests: "tests", Verify: "verify", AcceptanceCommand: "test behavior", CheckCommand: "project check"}, {Title: "sibling", Prompt: "last sibling"}}
	var got decision.Request
	d.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		got = q
		return decision.Verdict{Option: q.Options[1], Confidence: 1}, nil
	}
	if err = checkCoherence(context.Background(), d, aid, p, tasks); err == nil {
		t.Fatal("edited second option must still refuse")
	}
	a, err := plan.GetApproach(d.Store, aid)
	if err != nil {
		t.Fatal(err)
	}
	req, size, err := coherenceRequest(d, p, a, tasks)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if size != len(raw) || req.Instructions != got.Instructions || got.Options[1] != d.Prompts.Text("decision.readiness.revise") {
		t.Fatalf("request mismatch: %s", raw)
	}
	for _, v := range []string{"current", "target", "sketch", "accept", "tests", "verify", "test behavior", "project check", "last sibling"} {
		if !strings.Contains(got.Context, v) {
			t.Fatal("lost field", v)
		}
	}
	if size <= len(got.Context) {
		t.Fatal("did not charge options/instructions/JSON escaping")
	}
}
