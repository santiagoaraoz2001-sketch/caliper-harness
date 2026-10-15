package plan

import (
	"encoding/json"
	"path/filepath"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestEvidenceAttemptNativeSlicesAndRewrite(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := WithProject(t.Context(), t.TempDir())
	p, err := CreatePlanContext(ctx, s, "p", "g")
	if err != nil {
		t.Fatal(err)
	}
	a, err := CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	task, err := CreateTask(s, a.ID, "t", "p")
	if err != nil {
		t.Fatal(err)
	}
	first := json.RawMessage(`{"Kind":"execution","Attempt":1,"Output":"α🙂\n\t"}`)
	second := json.RawMessage(`{"Kind":"check","Attempt":2,"Output":"FAILED"}`)
	log := "[" + string(first) + "," + string(second) + "]"
	seed := func(body string) {
		t.Helper()
		if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log`, map[string]any{"id": task.ID, "log": body}); err != nil {
			t.Fatal(err)
		}
	}
	seed(log)
	seen := []string{}
	count, err := VisitAttemptRecords(ctx, s, task.ID, 16, func(index int, raw json.RawMessage) error {
		if index != len(seen) {
			t.Fatalf("unstable offset %d", index)
		}
		seen = append(seen, string(raw))
		return nil
	})
	if err != nil || count != 2 || seen[0] != string(first) || seen[1] != string(second) {
		t.Fatalf("native Unicode scalar slices changed exact record bytes: %q count=%d err=%v", seen, count, err)
	}
	_, err = VisitAttemptRecords(ctx, s, task.ID, 16, func(index int, raw json.RawMessage) error {
		if index == 0 {
			seed(strings.Replace(log, "FAILED", "PASSED", 1)) // Same length replacement, different bytes.
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("native chunks mixed across same-length source rewrite: %v", err)
	}
}
