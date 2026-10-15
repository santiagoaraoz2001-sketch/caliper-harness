package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestQualifiedProjectionNativeArchive(t *testing.T) {
	raw, err := os.ReadFile("testdata/qualified-native-task.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Task               plan.Task
		Checked, Qualified bool
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Task.AttemptLog) != 74920 || !fixture.Checked || !fixture.Qualified || fixture.Task.Qualified() {
		t.Fatal("native receipt fixture changed")
	}
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p, err := plan.CreatePlan(s, "fixture", "native receipt projection")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "fixture", "raw receipt component fixture, not a new execution")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Task.ID = task.ID
	if _, err = plan.SetTaskBlock(s, task.ID, "", "", "", "", "", "", fixture.Task.CheckCommand); err != nil {
		t.Fatal(err)
	}
	if err = plan.SetTaskAttempts(s, task.ID, fixture.Task.Attempts); err != nil {
		t.Fatal(err)
	}
	if err = plan.SetTaskOutcome(s, &fixture.Task); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.state='done'`, map[string]any{"id": task.ID}); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='complete'`, map[string]any{"id": a.ID}); err != nil {
		t.Fatal(err)
	}
	assert := func() {
		t.Helper()
		owning, err := plan.GetTask(s, task.ID)
		if err != nil || owning.Qualified() || owning.AttemptLog != fixture.Task.AttemptLog {
			t.Fatalf("owner/raw archive: %v %+v", err, owning)
		}
		api := New(s, "component", t.TempDir())
		for _, query := range []string{
			fmt.Sprintf(`{task(id:%q){id qualified}}`, task.ID),
			fmt.Sprintf(`{approach(id:%q){qualified tasks{id qualified}}}`, a.ID),
		} {
			out, err := api.ExecBounded(context.Background(), query, "", nil, config.Defaults().Tools.GraphQL)
			if err != nil {
				t.Fatalf("qualified projection cannot read historical owner receipt: %s %v", out, err)
			}
			var envelope struct{ Data map[string]any }
			if err = json.Unmarshal(out, &envelope); err != nil {
				t.Fatal(err)
			}
			if node, ok := envelope.Data["task"].(map[string]any); ok && node["qualified"] != false {
				t.Fatalf("task differs from owner: %s", out)
			}
			if node, ok := envelope.Data["approach"].(map[string]any); ok {
				if node["qualified"] != false || node["tasks"].([]any)[0].(map[string]any)["qualified"] != false {
					t.Fatalf("nested owner differs: %s", out)
				}
			}
			t.Logf("qualified query %d bytes", len(out))
		}
		if out, err := api.ExecBounded(context.Background(), fmt.Sprintf(`{task(id:%q){attemptLog}}`, task.ID), "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatalf("raw archive cap bypassed: %d bytes", len(out))
		}
	}
	assert()
	// Remove the additive column to reproduce an actual old-schema reopen.
	if err = s.Write(`ALTER TABLE Task DROP check_projection`, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	assert()
	// Resume remaining NULL rows without rewriting an already-derived record.
	legacyParent, err := plan.CreateApproach(s, p.ID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := plan.CreateTask(s, legacyParent.ID, "legacy", "no receipt")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.check_projection=NULL,t.attempt_log='[]',t.state='done',t.execution='completed',t.verification='passed',t.review='PASS',t.check_command='true'`, map[string]any{"id": legacy.ID}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Query(`MATCH(t:Task) WHERE t.id=$id RETURN t.check_projection AS projection`, map[string]any{"id": task.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	assert()
	after, err := s.Query(`MATCH(t:Task) WHERE t.id=$id RETURN t.check_projection AS projection`, map[string]any{"id": task.ID})
	if err != nil || before[0]["projection"] != after[0]["projection"] {
		t.Fatalf("non-NULL projection rewritten: %v", err)
	}
	owning, err := plan.GetTask(s, legacy.ID)
	if err != nil || owning.Qualified() {
		t.Fatalf("legacy prose fabricated qualification: %v %+v", err, owning)
	}
	out, err := New(s, "component", t.TempDir()).ExecBounded(context.Background(), fmt.Sprintf(`{task(id:%q){qualified}}`, legacy.ID), "", nil, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data struct{ Task struct{ Qualified bool } }
	}
	if err = json.Unmarshal(out, &got); err != nil || got.Data.Task.Qualified {
		t.Fatalf("legacy projection fabricated qualification: %s %v", out, err)
	}
}
