package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestSelectedToolViewPersistsBeforeNextRun(t *testing.T) {
	m := New(testCfg(), "test")
	m.SessionsPath = t.TempDir()
	m.Chat.SessionIDs = []string{"own"}
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "inspect", false)
	m = submit(m, "/tools full")
	events, err := session.Read(m.SessionsPath, "own")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := session.Restore(events, 32768)
	if err != nil || c.ToolView != "full" {
		t.Fatalf("selection was not durable: %+v %v", c, err)
	}
	resumed := New(testCfg(), "test")
	resumed.Chat.Context = c
	if !strings.Contains(resumed.footer(), "tools: full") || resumed.Chat.toolView() != "full" {
		t.Fatal("restored profile is not the displayed/factory profile")
	}
	resumed.Chat.Context.Workflow = &session.WorkflowScope{Store: "owned", Kind: "root"}
	if resumed.Chat.toolView() != "full" || resumed.Chat.Context.ToolView != "full" {
		t.Fatal("workflow overwrote user preference")
	}
	resumed.Chat.Context.Workflow = nil
	if resumed.Chat.toolView() != "full" {
		t.Fatal("workflow departure lost preference")
	}
	// The saved chat owns the selection; a new tab does not inherit it.
	m.newTab()
	if m.Chat.Context.ToolView != "" {
		t.Fatal("new tab borrowed saved profile")
	}
}
func TestToolViewPersistenceFailureDoesNotChangeSelection(t *testing.T) {
	m := New(testCfg(), "test")
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	m.SessionsPath = blocked
	m.Chat.SessionIDs = []string{"own"}
	m.Chat.Context = session.Continuation{Version: 1, ToolView: "coding"}
	m.Chat.Context.Add("intent", "user", "inspect", false)
	m = submit(m, "/tools full")
	if m.Chat.Context.ToolView != "coding" || m.Chat.Tools == "full" {
		t.Fatal("failed persistence silently applied selection")
	}
}

func TestApproachDisplayMakesControlsLiteral(t *testing.T) {
	raw := `{"coverage":[{"article_index":0,"article":"one\u001b[31m"}],"strategy":"go\ncarefully\u0007"}`
	if strings.Contains(approachLabel(raw), "\x1b") {
		t.Fatal("approach label permits terminal control")
	}
	d := &PlanDetail{Kind: rowApproach, Ref: "P1-A1", LA: &LoadedApproach{A: &plan.Approach{Title: raw}}}
	text := strings.Join(detailLogical(d), "\n")
	if strings.ContainsAny(text, "\x1b\a") || !strings.Contains(text, `\u001b`) || !strings.Contains(text, "carefully") {
		t.Fatalf("unsafe or lost presentation: %q", text)
	}
}

func TestWorkflowChoicePersistsAtomicallyAndLegacyMetadataDoesNotPromote(t *testing.T) {
	m := workflowModel(t)
	m.navigate(ViewChat, "")
	m.SessionsPath = t.TempDir()
	m.Chat.SessionIDs = []string{"selected"}
	m.Chat.Context = session.Continuation{Version: 1, ToolView: "coding"}
	m.Chat.Context.Add("intent", "user", "Inspect workflow", false)
	m = submit(m, "/workflow P1-A1-T1")
	events, err := session.Read(m.SessionsPath, "selected")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := session.Restore(events, 65536)
	if err != nil || c.ToolView != "workflow" || c.Workflow == nil || c.Workflow.ID != "task-one" {
		t.Fatalf("explicit selection not atomic: %+v %v", c, err)
	}
	resumed := New(testCfg(), "test")
	resumed.Chat.Context = c
	if resumed.Chat.toolView() != "workflow" || !strings.Contains(resumed.View(), "tools: workflow") {
		t.Fatal("explicit saved workflow did not reopen")
	}
	// Legacy selected node is retained but never promoted by a UI redraw/event.
	legacy := c.Clone()
	legacy.ToolView = ""
	m.Chat.Context = legacy
	m.Chat.Tools = ""
	m = submit(m, "/tools")
	if m.Chat.toolView() != "" || m.Chat.Tools != "" || m.Chat.Context.Workflow == nil || strings.Contains(m.footer(), "tools: workflow") {
		t.Fatal("legacy metadata escalated exposure or lost selection")
	}
	// Required persistence failing must change neither the explicit choice nor node.
	blocked := filepath.Join(t.TempDir(), "not-dir")
	if err = os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	m.SessionsPath = blocked
	m.Chat.Context = legacy
	m.Chat.Tools = ""
	m = submit(m, "/workflow P1-A2")
	if m.Chat.Tools != "" || m.Chat.Context.ToolView != "" || m.Chat.Context.Workflow.ID != "task-one" {
		t.Fatal("failed save partially changed selected owner/exposure")
	}
}

func TestToolExposurePreservesClaimAndCheckOwner(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Context = session.Continuation{Version: 1, ToolView: "workflow", Workflow: &session.WorkflowScope{Store: "store", Kind: "task", ID: "task"}, Assignment: &session.Assignment{Store: "store", Workspace: "workspace", PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 3}, Coding: &session.CodingState{TaskID: "task", Attempt: 3, Workspace: "workspace", Revision: 4, Command: "saved suite"}}
	m.Chat.Context.Add("intent", "user", "preserve owner", false)
	old := m.Chat.Context.Clone()
	m = submit(m, "/tools coding")
	if m.Chat.Context.Workflow != nil || m.Chat.Context.ToolView != "coding" || !reflect.DeepEqual(m.Chat.Context.Assignment, old.Assignment) || !reflect.DeepEqual(m.Chat.Context.Coding, old.Coding) {
		t.Fatal("exposure switch erased or fabricated claim/check owner")
	}
}
func TestWorkflowForeignProjectRefusedBeforeSelectionPersistence(t *testing.T) {
	m := workflowModel(t)
	m.navigate(ViewChat, "")
	m.SessionsPath = t.TempDir()
	m.Chat.SessionIDs = []string{"owner"}
	m.Chat.Context = session.Continuation{Version: 1, ToolView: "coding"}
	m.Chat.Context.Add("intent", "user", "owned", false)
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), t.TempDir()), s, "foreign", "goal")
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	before := m.Chat.Context.Clone()
	m = submit(m, "/workflow "+p.ID)
	if !reflect.DeepEqual(before, m.Chat.Context) || m.Chat.Tools != "" {
		t.Fatal("foreign project selection changed authority")
	}
	if _, err = os.Stat(filepath.Join(m.SessionsPath, "owner.jsonl")); !os.IsNotExist(err) {
		t.Fatal("refused foreign selection persisted")
	}
}
