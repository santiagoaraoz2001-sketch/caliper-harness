package run

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

func TestProjectContextCarriesOnlyAssociatedTaskNotesAndLazyLinks(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	ctx := plan.WithProject(t.Context(), root)
	p, err := plan.CreatePlanContext(ctx, s, "note context", "preserve distinct task decisions")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproachContext(ctx, s, p.ID, "notes")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTaskContext(ctx, s, a.ID, "current", "preserve the chosen layout")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := plan.CreateTaskContext(ctx, s, a.ID, "sibling", "unrelated work")
	if err != nil {
		t.Fatal(err)
	}
	refs := []memory.Reference{{Key: "caliper/guides/ui/typography", Section: "worked-specimen", SourceSection: "layout", Label: "Compare concrete type roles", Relation: "explains"}}
	note, err := memory.PutKnowledgeReferences(ctx, s, root, "author", "project/features/current", "## Layout\nUse a dense table with tabular numbers.", "", task.ID, "", refs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.PutKnowledge(ctx, s, root, "author", "project/features/sibling", "SIBLING_PRIVATE_CONTEXT", "", sibling.ID, ""); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	project := projectContext(cfg, s, root, fixtureTokenCounter)
	c := session.Continuation{Version: 1, Coding: &session.CodingState{TaskID: task.ID, Attempt: 1, Workspace: root}}
	if err := project(ctx, &c, task.ID); err != nil {
		t.Fatal(err)
	}
	packet := contextPacket(t, &c)
	if packet.TaskKnowledge != "selected" {
		t.Fatalf("task association not selected: %+v", packet)
	}
	found := false
	for _, item := range packet.Items {
		if item.Key == refs[0].Key {
			t.Fatal("deep target body was loaded without a selective request")
		}
		if item.Key == note.Key {
			found = true
			if item.ID != note.ID || item.TaskID != task.ID || item.Body != note.Body || item.Status != "supplied" || len(item.Links) != 1 || item.Links[0].Section != refs[0].Section {
				t.Fatalf("lost exact note/version/next hop: %+v", item)
			}
		}
	}
	raw, _ := json.Marshal(packet)
	if !found || strings.Contains(string(raw), "SIBLING_PRIVATE_CONTEXT") {
		t.Fatalf("association leaked or eagerly expanded: %s", raw)
	}
	updated, err := memory.PutKnowledgeReferences(ctx, s, root, "author", note.Key, "## Layout\nKeep the dense table; use signed amounts.", note.ID, task.ID, "", refs)
	if err != nil {
		t.Fatal(err)
	}
	if err := project(ctx, &c, task.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(contextPacket(t, &c))
	if strings.Contains(string(raw), note.ID) || !strings.Contains(string(raw), updated.ID) {
		t.Fatalf("task head was cached past replacement: %s", raw)
	}
	for _, state := range []session.CodingState{
		{TaskID: task.ID, Attempt: 1, Workspace: root}, // Retained history is not a live task selection.
		{TaskID: task.ID, Attempt: 0, Workspace: root},
		{TaskID: task.ID, Attempt: 1, Workspace: root, Closed: true},
		{TaskID: task.ID, Attempt: 1, Workspace: t.TempDir()},
	} {
		c.Coding = &state
		if err := project(ctx, &c, ""); err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(contextPacket(t, &c))
		if strings.Contains(string(raw), updated.ID) {
			t.Fatalf("unassociated turn retained task context: %s", raw)
		}
	}
}
