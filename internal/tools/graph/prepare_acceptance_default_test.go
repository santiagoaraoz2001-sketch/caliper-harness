package graph

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// Omitting acceptance_command binds the check command as the behavioral
// acceptance instead of forcing a second, restated copy.
func TestPreparationAcceptanceDefaultsToCheckCommand(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	limits := config.Defaults().Tools.GraphQL
	owner, root := api.New(s, "test", t.TempDir()), t.TempDir()
	g, e := New(owner, limits, root, "binding", s.Identity())
	if e != nil {
		t.Fatal(e)
	}
	d := Preparation(g, s, limits, "")[0]
	supplied := tool.Args{"plan_title": "repair", "plan_goal": "correct output", "approach_title": "small fix", "task_title": "repair", "task_prompt": "repair source", "current": "observed problem", "target": "correct output", "sketch": "small change", "acceptance": "suite passes", "tests": "owning test", "verify": "inspect source", "check_command": "test -d ."}
	raw, _ := json.Marshal(supplied)
	args, repair := tool.ValidateJSON(d, string(raw))
	if repair != "" {
		t.Fatal(repair)
	}
	bound, args, e := d.Bind(context.Background(), args)
	if e != nil {
		t.Fatal(e)
	}
	res, e := bound.Execute(context.Background(), args)
	if e != nil {
		t.Fatalf("prepare without acceptance_command: %v (%s)", e, res.Text)
	}
	var receipt api.Result
	if err := json.Unmarshal([]byte(res.Text), &receipt); err != nil || len(receipt.Effects) != 4 {
		t.Fatalf("receipt: %s %v", res.Text, err)
	}
	actual, err := plan.GetTask(s, receipt.Effects[3].ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.CheckCommand != "test -d ." || actual.AcceptanceCommand != "test -d ." {
		t.Fatalf("commands check=%q acceptance=%q, want both bound to check command", actual.CheckCommand, actual.AcceptanceCommand)
	}
}
