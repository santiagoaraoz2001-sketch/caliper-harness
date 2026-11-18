package workflowlib

import (
	"path/filepath"
	"strings"
	"testing"
)

func publishBlock(t *testing.T, s *Store, id string) Block {
	t.Helper()
	b, err := s.CreateBlockDraft(id, "Block "+id)
	if err != nil {
		t.Fatal(err)
	}
	fields := []struct{ k, v string }{
		{"purpose", "Do one bounded thing"},
		{"inputs", "input"},
		{"impl_type", "tool"},
		{"impl_tool", "bash"},
		{"outputs", "result"},
		{"completion_evidence", "command output"},
		{"failure_handling", "return to revision with the failed output"},
		{"examples", "input=go test => result=pass"},
	}
	for _, f := range fields {
		b, _, err = s.SetBlockField(id, b.Revision, f.k, f.v)
		if err != nil {
			t.Fatalf("set %s: %v", f.k, err)
		}
	}
	b, err = s.PublishBlock(id, b.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWorkflowLibraryVersioningActivationAndPinnedExecution(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := publishBlock(t, s, "run-check")
	if b.Status != StatusPublished || b.Version != 1 {
		t.Fatalf("published block = %+v", b)
	}
	if _, _, err := s.SetBlockField("run-check", b.Revision, "purpose", "mutate published"); err == nil {
		t.Fatal("published block mutated in place")
	}
	draft, err := s.EditBlock("run-check")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Version != 2 || draft.Status != StatusDraft {
		t.Fatalf("edit draft = %+v", draft)
	}
	if _, _, err := s.SetBlockField("run-check", draft.Revision-1, "purpose", "stale"); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("stale revision accepted: %v", err)
	}

	w, err := s.CreateWorkflowDraft("ship-change", "Ship change")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ k, v string }{
		{"purpose", "Ship one verified change"},
		{"inputs", "change"},
		{"steps", "run-check@1"},
		{"completion_requirements", "check evidence recorded"},
		{"revision_route", "return to the owning step with failure evidence"},
	} {
		w, _, err = s.SetWorkflowField("ship-change", w.Revision, f.k, f.v)
		if err != nil {
			t.Fatalf("set workflow %s: %v", f.k, err)
		}
	}
	w, err = s.PublishWorkflow("ship-change", w.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartExecution("ship-change", map[string]string{"change": "x"}); err == nil {
		t.Fatal("inactive workflow started")
	}
	if err := s.SetActivation("ship-change", 1, true); err != nil {
		t.Fatal(err)
	}
	exec, err := s.StartExecution("ship-change", map[string]string{"change": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if exec.WorkflowVersion != 1 || len(exec.Steps) != 1 || exec.Steps[0].BlockVersion != 1 {
		t.Fatalf("execution not pinned: %+v", exec)
	}
	if _, err := s.AdvanceExecution(exec.ID, "", "out"); err == nil {
		t.Fatal("advance without evidence accepted")
	}
	exec, err = s.AdvanceExecution(exec.ID, "go test passed", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if exec.Status != StatusComplete {
		t.Fatalf("execution status = %q", exec.Status)
	}
	reopened, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	blocks, workflows := reopened.List()
	if len(blocks) < 2 || len(workflows) != 1 {
		t.Fatalf("reopened library blocks=%d workflows=%d", len(blocks), len(workflows))
	}
}
