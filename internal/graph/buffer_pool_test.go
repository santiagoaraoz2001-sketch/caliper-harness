package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestBufferPoolGraphPATReopen(t *testing.T) {
	for _, pool := range []uint64{64 << 20, 256 << 20} {
		t.Run(fmt.Sprint(pool), func(t *testing.T) {
			root, path := t.TempDir(), filepath.Join(t.TempDir(), "db")
			root, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			s, err := store.OpenWithBufferPool(path, pool)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			checks := 0
			api := NewWithReady(s, "test", t.TempDir(), func(ctx context.Context, id string) (*plan.Approach, error) {
				return plan.Ready(ctx, s, id, func(_ context.Context, p *plan.Plan, tasks []*plan.Task) error {
					checks++
					if len(tasks) != 1 || tasks[0].CheckCommand == "" || p.ProjectRoot != root {
						return fmt.Errorf("incomplete readiness facts")
					}
					return nil
				})
			})
			api, err = api.Scoped(root, "pool-test", true)
			if err != nil {
				t.Fatal(err)
			}
			exec := func(q string, vars map[string]any) Result {
				t.Helper()
				b, e := api.ExecBounded(context.Background(), q, "", vars, config.Defaults().Tools.GraphQL)
				if e != nil {
					t.Fatalf("%s: %s %v", q, b, e)
				}
				var r Result
				if e = json.Unmarshal(b, &r); e != nil {
					t.Fatal(e)
				}
				return r
			}
			raw, err := os.ReadFile("testdata/qualified-native-task.json")
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct{ Task plan.Task }
			if err = json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			values := freshTaskValues()
			values["checkCommand"] = fixture.Task.CheckCommand
			r := exec(prepareDocument, map[string]any{"input": values})
			var data struct {
				Fresh struct{ Plan, Approach, Task struct{ ID string } }
			}
			if err = json.Unmarshal(r.Data, &data); err != nil {
				t.Fatal(err)
			}
			p, a, x := data.Fresh.Plan.ID, data.Fresh.Approach.ID, data.Fresh.Task.ID
			exec(fmt.Sprintf(`mutation{transitionPlan(id:%q,to:"active"){id state} transitionApproach(id:%q,to:"ready"){id state}}`, p, a), nil)
			if checks != 1 {
				t.Fatal("actual owning Ready checker not entered", checks)
			}
			snap, err := plan.ReadinessSnapshot(s, a)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := plan.ClaimTaskContext(plan.WithProject(context.Background(), root), s, x, snap, fixture.Task.CheckCommand)
			if err != nil || claimed.Attempts != 1 {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			// Retained native receipt fixture tests storage/projection, not new execution.
			fixture.Task.ID = x
			if err = plan.SetTaskAttempts(s, x, fixture.Task.Attempts); err != nil {
				t.Fatal(err)
			}
			if err = plan.SetTaskOutcome(s, &fixture.Task); err != nil {
				t.Fatal(err)
			}
			if err = s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.state='done'`, map[string]any{"id": x}); err != nil {
				t.Fatal(err)
			}
			exec(`mutation{putKnowledge(key:"pool",body:"retained authored κ",expectedPreviousId:""){id body}}`, nil)
			s.Close()
			s, err = store.OpenWithBufferPool(path, pool)
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.GetTask(s, x)
			if err != nil || got.Checked() || got.Qualified() || got.AttemptLog != fixture.Task.AttemptLog {
				t.Fatalf("retained historical receipt: %v %+v", err, got)
			}
			api = New(s, "test", t.TempDir())
			api, err = api.Scoped(root, "pool-test", true)
			if err != nil {
				t.Fatal(err)
			}
			r = exec(`query{knowledge(key:"pool"){id body}}`, nil)
			if !strings.Contains(string(r.Data), "retained authored κ") {
				t.Fatalf("knowledge lost: %s", r.Data)
			}
			r = exec(fmt.Sprintf(`query{task(id:%q){id qualified} approach(id:%q){id tasks{id qualified}}}`, x, a), nil)
			var readback struct {
				Task     struct{ Qualified bool }
				Approach struct{ Tasks []struct{ Qualified bool } }
			}
			if err = json.Unmarshal(r.Data, &readback); err != nil || readback.Task.Qualified || len(readback.Approach.Tasks) != 1 || readback.Approach.Tasks[0].Qualified {
				t.Fatalf("historical receipt became qualified: %s %v", r.Data, err)
			}
		})
	}
}
