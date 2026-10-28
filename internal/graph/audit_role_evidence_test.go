package graph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestTaskEvidenceReadsOwnedArchiveReference(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, root := t.TempDir(), t.TempDir()
	a := evidenceAPI(t, s, dir, root)
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), a.CapturedProject()), s, "plan", "goal")
	if err != nil {
		t.Fatal(err)
	}
	app, err := plan.CreateApproach(s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, app.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []struct{ name, field, owner string }{{"execution", "ExecutionSession", "execution_pending"}, {"check", "CheckSession", "check_pending"}, {"reviewer", "ReviewerSession", "review_pending"}} {
		t.Run(phase.name, func(t *testing.T) {
			id := "owned-" + phase.name
			evidenceAppend(t, dir, id, "workspace", filepath.Join(t.TempDir(), "removed-worktree"))
			owner, _ := json.Marshal(map[string]any{"Task": task.ID, "Attempt": 1, "Phase": phase.owner})
			evidenceAppend(t, dir, id, "role_owner", string(owner))
			evidenceAppend(t, dir, id, "result", "ACTUAL_ARCHIVED_FAILURE exit7")
			record, _ := json.Marshal([]map[string]any{{"Kind": phase.owner, "Attempt": 1, phase.field: id}})
			if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log,t.attempts=1`, map[string]any{"id": task.ID, "log": string(record)}); err != nil {
				t.Fatal(err)
			}
			q := fmt.Sprintf(`{taskEvidence(taskId:%q,record:0,phase:%q,offset:2,limit:1){historical groups{reference records{kind text workspace}}}}`, task.ID, phase.name)
			got := evidenceData(t, a, q)
			if !strings.Contains(got, "ACTUAL_ARCHIVED_FAILURE") || !strings.Contains(got, `"historical":true`) {
				t.Fatal(got)
			}
			// A mismatched owner later in the archive invalidates even a narrow earlier page.
			evidenceAppend(t, dir, id, "role_owner", `{"Task":"other-task","Attempt":1,"Phase":"execution_pending"}`)
			if _, err := a.ExecBounded(t.Context(), q, "", nil, config.Defaults().Tools.GraphQL); err == nil {
				t.Fatal("mixed archive owner admitted")
			}
		})
	}
}
