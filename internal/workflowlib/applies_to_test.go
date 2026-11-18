package workflowlib

import (
	"path/filepath"
	"strings"
	"testing"
)

func publishLeafBlock(t *testing.T, s *Store, id string) {
	t.Helper()
	b, err := s.CreateBlockDraft(id, "Leaf "+id)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err = s.SetBlockFields(id, b.Revision, map[string]string{
		"purpose": "do one bounded thing", "inputs": "task request",
		"impl_type": "main_model", "impl_instructions": "do the bounded thing and record the result",
		"outputs": "recorded result", "completion_evidence": "observed result",
		"failure_handling": "stay on this step", "examples": "task request=x => recorded result=y",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBlock(id, b.Revision); err != nil {
		t.Fatal(err)
	}
}

func publishWorkflow(t *testing.T, s *Store, id, steps string, extra map[string]string) error {
	t.Helper()
	w, err := s.CreateWorkflowDraft(id, "Workflow "+id)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{
		"purpose": "purpose " + id, "inputs": "task request", "steps": steps,
		"completion_requirements": "every step advanced with observed evidence",
		"revision_route":          "return to the owning step",
	}
	for k, v := range extra {
		fields[k] = v
	}
	w, _, err = s.SetWorkflowFields(id, w.Revision, fields)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PublishWorkflow(id, w.Revision)
	return err
}

func TestWorkflowAppliesToTagsAndFilter(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "lib.json"))
	if err != nil {
		t.Fatal(err)
	}
	publishLeafBlock(t, s, "leaf")
	if err := publishWorkflow(t, s, "ui-flow", "leaf@1", map[string]string{"applies_to": "UI-Design; frontend"}); err != nil {
		t.Fatal(err)
	}
	w, ok := s.GetWorkflow("ui-flow", 1)
	if !ok || len(w.AppliesTo) != 2 || w.AppliesTo[0] != "ui-design" || w.AppliesTo[1] != "frontend" {
		t.Fatalf("applies_to not normalized/stored: %+v", w.AppliesTo)
	}
	if got := s.WorkflowsByTag("ui-design"); len(got) != 1 || got[0].ID != "ui-flow" {
		t.Fatalf("tag filter missed workflow: %+v", got)
	}
	if got := s.WorkflowsByTag("3d"); len(got) != 0 {
		t.Fatalf("tag filter matched unrelated workflow: %+v", got)
	}
}

func TestWorkflowAppliesToRejectsInvalidTag(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "lib.json"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWorkflowDraft("bad-tags", "Bad tags")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetWorkflowFields("bad-tags", w.Revision, map[string]string{"applies_to": "Bad Tag!"}); err == nil {
		t.Fatal("invalid applies_to tag accepted")
	}
}

func TestPublishWorkflowRefusesDeepNesting(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "lib.json"))
	if err != nil {
		t.Fatal(err)
	}
	publishLeafBlock(t, s, "leaf")
	if err := publishWorkflow(t, s, "w1", "leaf@1", nil); err != nil {
		t.Fatal(err)
	}
	prev := "w1"
	for depth := 2; depth <= 5; depth++ {
		bid := "nest-" + prev
		b, err := s.CreateBlockDraft(bid, "Nest "+prev)
		if err != nil {
			t.Fatal(err)
		}
		b, _, err = s.SetBlockFields(bid, b.Revision, map[string]string{
			"purpose": "run nested workflow", "inputs": "task request",
			"impl_type": "workflow", "impl_workflow": prev + "@1",
			"outputs": "nested result", "completion_evidence": "nested execution complete",
			"failure_handling": "stay on this step", "examples": "task request=x => nested result=y",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PublishBlock(bid, b.Revision); err != nil {
			t.Fatal(err)
		}
		wid := "w" + string(rune('0'+depth))
		err = publishWorkflow(t, s, wid, bid+"@1", nil)
		if depth <= 4 && err != nil {
			t.Fatalf("depth %d must publish: %v", depth, err)
		}
		if depth == 5 && (err == nil || !strings.Contains(err.Error(), "nesting depth")) {
			t.Fatalf("depth 5 must refuse with nesting depth error, got %v", err)
		}
		prev = wid
	}
}

func TestEnsureSeedPackAddsDomainWorkflowsOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "lib.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSeedPack(s); err != nil {
		t.Fatal(err)
	}
	_, workflows := s.List()
	first := len(workflows)
	for _, id := range []string{"ui-element-design", "oss-ui-search-implement", "threejs-asset-animation", "game-playtest-report"} {
		w, ok := s.GetWorkflow(id, 1)
		if !ok || w.Status != StatusPublished || len(w.AppliesTo) == 0 || len(w.Steps) == 0 {
			t.Fatalf("seed workflow %s missing/unpublished/untagged: %+v", id, w)
		}
		if _, on := s.Activation(id); on {
			if act, _ := s.Activation(id); act.Enabled {
				t.Fatalf("seed workflow %s must not be auto-enabled", id)
			}
		}
	}
	if err := EnsureSeedPack(s); err != nil {
		t.Fatal(err)
	}
	_, workflows = s.List()
	if len(workflows) != first {
		t.Fatalf("seed pack not idempotent: %d -> %d", first, len(workflows))
	}
}
