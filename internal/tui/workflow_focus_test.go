package tui

import (
	"encoding/json"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestWorkflowExplicitEntry(t *testing.T) {
	m := plansModel(t)
	m.navigate(ViewChat, "")
	m = submit(m, "/workflow P1-A1-T1")
	raw, _ := json.Marshal(m.Chat.Context)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	scope, _ := value["workflow"].(map[string]any)
	if m.Chat.Tools != "workflow" || scope["id"] != "task-one" || scope["kind"] != "task" {
		t.Fatalf("explicit node not selected: tools=%q context=%s", m.Chat.Tools, raw)
	}
	if strings.Contains(m.Chat.Lines[len(m.Chat.Lines)-1].Text, "task-two") {
		t.Fatal("scope header dumped sibling identity")
	}
	m.Chat.Running = true
	m = submit(m, "/workflow")
	next, _ := json.Marshal(m.Chat.Context)
	if string(raw) != string(next) {
		t.Fatal("busy entry changed authority")
	}
	m.Chat.Running = false
	m = submit(m, "/tools coding")
	next, _ = json.Marshal(m.Chat.Context)
	value = nil // Unmarshal into a reused map retains absent keys.
	_ = json.Unmarshal(next, &value)
	if m.Chat.Tools != "coding" || value["workflow"] != nil {
		t.Fatalf("explicit exit retained scope: %s", next)
	}
}

func TestWorkflowRestoredLabelsTabsAndSharedBusy(t *testing.T) {
	m := plansModel(t)
	m.navigate(ViewChat, "")
	m = submit(m, "/workflow P1-A1-T1")
	saved := m.Chat.Context.Clone()
	saved.Version = 1
	saved.Add("intent", "user", "persisted owning task", false)
	raw, e := saved.Snapshot().Encode()
	if e != nil {
		t.Fatal(e)
	}
	restored, _, e := session.Restore([]session.Event{{Kind: "continuation", Text: raw}}, 32768)
	if e != nil {
		t.Fatal(e)
	}
	m.Chat.Context = restored
	m.Chat.Tools = "" // same initial restoration shape used by the CLI.
	if !strings.Contains(m.View(), "tools: workflow") {
		t.Fatal("restored footer reports coding")
	}
	m = submit(m, "/tools")
	if !strings.Contains(m.Chat.Lines[len(m.Chat.Lines)-1].Text, "selected-node legal operations") {
		t.Fatal("restored /tools falsely lists coding")
	}
	before, _ := json.Marshal(m.Chat.Context)
	m.Wf.Busy = "owned"
	m = submit(m, "/workflow root")
	after, _ := json.Marshal(m.Chat.Context)
	if string(before) != string(after) {
		t.Fatal("shared busy changed focus")
	}
	m.Wf.Busy = ""
	m.newTab()
	if m.Chat.Context.Workflow != nil {
		t.Fatal("new tab borrowed focus")
	}
	m.switchTab(0)
	if m.Chat.Context.Workflow == nil || m.Chat.Context.Workflow.ID != "task-one" {
		t.Fatal("tab did not restore own focus")
	}
	m.Chat.Running = true
	m.Chat.Seq = 5
	other := saved.Clone()
	other.Workflow.ID = "task-two"
	next, _ := m.Update(runDoneMsg{seq: 4, continuation: other})
	m = next.(Model)
	if m.Chat.Context.Workflow.ID != "task-one" {
		t.Fatal("stale run changed focus")
	}
}
