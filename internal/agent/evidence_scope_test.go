package agent

import (
	"context"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

type evidenceArchiveSink struct{ dir string }

func (s evidenceArchiveSink) Append(_ context.Context, id, kind, text string) error {
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

func TestEvidenceDiscoveryRunCapturesHostWorkspaceOnce(t *testing.T) {
	dir, workspace := t.TempDir(), t.TempDir()
	r := &Runner{Model: &scriptModel{replies: []string{"Discovery only; no commands or coding check."}}, Registry: tool.NewRegistry(), Workspace: workspace, Session: evidenceArchiveSink{dir}, SessionID: "discovery", MaxSteps: 1, MaxTokens: 32768}
	if _, _, err := r.Run(t.Context(), "inspect an idea without coding"); err != nil {
		t.Fatal(err)
	}
	events, err := session.Read(dir, "discovery")
	if err != nil {
		t.Fatal(err)
	}
	workspaceEvents := 0
	for _, e := range events {
		if e.Kind == "workspace" {
			workspaceEvents++
			if e.Text != workspace {
				t.Fatal("workspace was inferred from model prose")
			}
		}
		if e.Kind == "coding" {
			t.Fatal("discovery fixture unexpectedly created a coding receipt")
		}
	}
	if workspaceEvents != 1 || r.Continuation.Coding != nil {
		t.Fatalf("host association missing/duplicated in discovery-only archive: %d coding=%+v", workspaceEvents, r.Continuation.Coding)
	}
}
