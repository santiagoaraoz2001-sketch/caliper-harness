package plan

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/store"
)

func TestReadinessPresentationBindsApproachScope(t *testing.T) {
	s, p, a, _ := atomicFixture(t)
	tasks, err := ApproachTasks(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ReadinessBriefSnapshot(s, a.ID, p, a, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(tx *store.Tx) error {
		return tx.Write(`MATCH (a:Approach) WHERE a.id=$id SET a.title=$title`, map[string]any{"id": a.ID, "title": `{"strategy":"new scope","guidance":[]}`})
	}); err != nil {
		t.Fatal(err)
	}
	after, err := ReadinessSnapshot(s, a.ID)
	if err != nil || after == before {
		t.Fatalf("changed strategy retained approval identity: %v", err)
	}
	if _, err := ReadinessBriefSnapshot(s, a.ID, p, a, tasks); err == nil || !strings.Contains(err.Error(), "approach changed") {
		t.Fatalf("stale displayed scope relabeled: %v", err)
	}
	current, err := GetApproach(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadinessBriefSnapshot(s, a.ID, p, current, tasks); err != nil || got != after {
		t.Fatalf("fresh displayed scope refused: %v", err)
	}
}

func TestReadinessValidatesEveryDeclaredGuidanceOwner(t *testing.T) {
	for _, owner := range []string{"plan", "approach", "task"} {
		for _, body := range []string{
			`{"guidance":null}`,
			`{"guidance":"design"}`,
			`{"guidance":[{"key":"design","version":"1"}]}`,
		} {
			t.Run(owner+"/"+body, func(t *testing.T) {
				s, p, a, task := atomicFixture(t)
				// A more specific empty selection cannot conceal an invalid ancestor.
				if err := s.Update(func(tx *store.Tx) error {
					if err := tx.Write(`MATCH (t:Task) WHERE t.id=$id SET t.sketch=$body`, map[string]any{"id": task.ID, "body": `{"guidance":[]}`}); err != nil {
						return err
					}
					switch owner {
					case "plan":
						return tx.Write(`MATCH (p:Plan) WHERE p.id=$id SET p.goal=$body`, map[string]any{"id": p.ID, "body": body})
					case "approach":
						return tx.Write(`MATCH (a:Approach) WHERE a.id=$id SET a.title=$body`, map[string]any{"id": a.ID, "body": body})
					default:
						return tx.Write(`MATCH (t:Task) WHERE t.id=$id SET t.sketch=$body`, map[string]any{"id": task.ID, "body": body})
					}
				}); err != nil {
					t.Fatal(err)
				}
				judged := false
				_, err := Ready(t.Context(), s, a.ID, func(context.Context, *Plan, []*Task) error { judged = true; return nil })
				if err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), "guidance") || judged {
					t.Fatalf("malformed %s reached judgment: called=%v error=%v", owner, judged, err)
				}
				actual, err := GetApproach(s, a.ID)
				if err != nil || actual.State != ApproachDraft {
					t.Fatalf("refusal changed approach state: %+v %v", actual, err)
				}
			})
		}
	}
}

func TestReadinessAcceptsLegacyAndExplicitGuidance(t *testing.T) {
	for _, body := range []string{"legacy task method", `{"guidance":[]}`, `{"guidance":[{"key":"design","version":"1","section":"hierarchy"}]}`} {
		t.Run(body, func(t *testing.T) {
			s, _, a, task := atomicFixture(t)
			if err := s.Update(func(tx *store.Tx) error {
				return tx.Write(`MATCH (t:Task) WHERE t.id=$id SET t.sketch=$body`, map[string]any{"id": task.ID, "body": body})
			}); err != nil {
				t.Fatal(err)
			}
			judged := false
			if _, err := Ready(t.Context(), s, a.ID, func(context.Context, *Plan, []*Task) error { judged = true; return nil }); err != nil || !judged {
				t.Fatalf("valid declared guidance or legacy prose refused: called=%v error=%v", judged, err)
			}
		})
	}
}
