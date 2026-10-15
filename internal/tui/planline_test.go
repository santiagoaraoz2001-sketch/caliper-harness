package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func seedPlans(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := plan.CreatePlan(s, "first", "g"); err != nil {
		t.Fatal(err)
	}
	second, err := plan.CreatePlan(s, "second", "g")
	if err != nil {
		t.Fatal(err)
	}
	ap1, err := plan.CreateApproach(s, second.ID, "ap1")
	if err != nil {
		t.Fatal(err)
	}
	t1, err := plan.CreateTask(s, ap1.ID, "t1", "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, t1.ID, plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, t1.ID, plan.TaskDone); err != nil {
		t.Fatal(err)
	}
	recordTUICompleted(t, s, t1.ID)
	if _, err := plan.CreateTask(s, ap1.ID, "t2", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.CreateApproach(s, second.ID, "ap2"); err != nil {
		t.Fatal(err)
	}
	ap3, err := plan.CreateApproach(s, second.ID, "ap3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.CreateTask(s, ap3.ID, "t3", "p"); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanStatus(t *testing.T) {
	title, appr, ok := planStatus(seedPlans(t), 0)
	if !ok {
		t.Fatal("seeded store must read")
	}
	if title != "second" {
		t.Errorf("title=%q, want the latest plan", title)
	}
	if want := []string{"ap1 1/2 qualified", "ap2 0/0 qualified"}; !reflect.DeepEqual(appr, want) {
		t.Errorf("approaches=%v, want %v", appr, want)
	}
}

func TestPlanStatusEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	title, appr, ok := planStatus(path, 0)
	if !ok || title != "" || appr != nil {
		t.Errorf("empty store: %q %v %v", title, appr, ok)
	}
}

func TestPlanStatusBadPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := planStatus(file, 0); ok {
		t.Error("file-as-dir must report unreadable")
	}
}

func TestPlanStatusLegacyCompleteIsUnqualified(t *testing.T) {
	path := seedPlans(t)
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach) WHERE a.title='ap1' SET a.state='complete'`, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach)-[:HAS_TASK]->(x:Task) WHERE a.title='ap1' SET x.execution=NULL,x.verification=NULL`, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, approaches, ok := planStatus(path, 0)
	if !ok || len(approaches) == 0 || approaches[0] != "ap1 0/2 qualified (complete, unverified)" {
		t.Fatalf("legacy planline=%v ok=%v", approaches, ok)
	}
}
