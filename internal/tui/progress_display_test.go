package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/session"
)

func TestProgressPresentationPreservesEvidence(t *testing.T) {
	m := New(testCfg(), "presentation")
	m.SessionsPath = t.TempDir()
	m.noteSession("captured")
	m.Chat.Running, m.Chat.Seq = true, 1
	events := []session.Event{
		{Kind: "judgment-input", Text: `{"LatestSettled":{"CallID":"read-1","Tool":"read","Changed":false}}`},
		{Kind: "call_resolution", Text: `{"ToolCallID":"read-1","OriginalName":"read","Name":"read","Arguments":{"path":"game.js"}}`},
		{Kind: "bound_call", Text: `graphql {"_project_root":"/private/project","query":"mutation..."} inclusive evidence`},
		{Kind: "judgment", Text: `source=discovery-checkpoint | pick=continue productive work | conf=0.879 | resolution=continued-work | options=continue productive work; repair the latest rejected call | context={"LatestSettled":{"CallID":"read-1"}}`},
		{Kind: "judgment-error", Text: "Progress advice unavailable: decision service busy; continuing unchanged."},
		{Kind: "result", Text: "tool error: file not found"},
	}
	for _, e := range events {
		if err := session.Append(m.SessionsPath, "captured", e); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(m.SessionsPath, "captured.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		m.onEvent(eventMsg{seq: 1, kind: e.Kind, text: e.Text})
	}
	var text strings.Builder
	for _, line := range m.Chat.Lines {
		text.WriteString(line.Text + "\n")
	}
	for _, want := range []string{"Judge: continue productive work", "confidence 0.88", "continued-work", "/decisions", "Progress advice unavailable", "tool error: file not found"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("missing operator information %q: %s", want, text.String())
		}
	}
	for _, internal := range []string{"LatestSettled", "OriginalName", "_project_root", "options="} {
		if strings.Contains(text.String(), internal) {
			t.Fatalf("internal packet leaked into chat: %s", text.String())
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("presentation changed recorded evidence", err)
	}
	m = loadDecisions(m)
	if len(m.Decisions.Rows) != 1 || m.Decisions.Rows[0].Source != "discovery-checkpoint" || m.Decisions.Rows[0].Conf != .879 {
		t.Fatalf("full decision lost from its existing view: %+v", m.Decisions)
	}
	if !m.Chat.Running || len(m.Chat.Context.Entries) != 0 || m.Chat.Context.Revision != 0 {
		t.Fatal("display changed the active run or model context")
	}
}
