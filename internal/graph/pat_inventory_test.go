package graph

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// Replay the saved native briefs, not padded synthetic text. The final mutation
// succeeded in the run; the following progress-facts query exhausted its budget.
func TestCapturedPATInventoryFitsDefaultBudget(t *testing.T) {
	raw, err := os.ReadFile("testdata/laguna_pat_briefs.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Plan, Approach map[string]string
		Tasks          []map[string]string
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, fixture.Plan["title"], fixture.Plan["goal"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, "active"); err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, fixture.Approach["title"])
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range fixture.Tasks {
		task, err := plan.CreateTask(s, a.ID, v["title"], v["prompt"])
		if err != nil {
			t.Fatal(err)
		}
		check, behavior := v["check"], v["behavior"]
		if _, err = plan.SetTaskBlockCommandsContext(context.Background(), s, task.ID, v["current"], v["target"], v["sketch"], v["acceptance"], v["tests"], v["verify"], &check, &behavior); err != nil {
			t.Fatal(err)
		}
	}
	api := New(s, "captured-pat", t.TempDir())
	// Identical selection to run.Assigned.Facts, including both inventory roots.
	fields := `{id title goal state approaches{id title state needsReady tasks{id title prompt state current target sketch acceptance tests verify checkCommand acceptanceCommand}}}`
	query := `query { draft:plans(state:"draft") ` + fields + ` active:plans(state:"active") ` + fields + ` }`
	old := config.Defaults().Tools.GraphQL
	old.MaxResultBytes = 65536
	if _, err = api.ExecBounded(context.Background(), query, "", nil, old); err == nil || !strings.Contains(err.Error(), "selected source exceeded") {
		t.Fatalf("captured old-limit failure not reproduced: %v", err)
	}
	out, err := api.ExecBounded(context.Background(), query, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatalf("default budget rejects captured planning: %v", err)
	}
	var result struct {
		Data struct {
			Active []struct {
				Approaches []struct {
					Tasks []struct{ Verify, Sketch string }
				}
			}
		}
	}
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Active) != 1 || len(result.Data.Active[0].Approaches) != 1 || len(result.Data.Active[0].Approaches[0].Tasks) != 2 {
		t.Fatalf("missing inventory: %s", out)
	}
	for i, task := range result.Data.Active[0].Approaches[0].Tasks {
		if task.Verify != fixture.Tasks[i]["verify"] || task.Sketch != fixture.Tasks[i]["sketch"] {
			t.Fatal("authored brief was altered or omitted")
		}
	}
	t.Logf("captured inventory returned %d bytes, with both complete task sketches and review criteria", len(out))
}
