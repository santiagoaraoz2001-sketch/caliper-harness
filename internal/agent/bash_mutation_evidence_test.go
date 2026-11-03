package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Files changed through bash bypass read-grounding; completion evidence must
// name them as ungrounded instead of only reporting an opaque receipt count.
func TestBashMutatedFilesNamedInCompletionEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "kept.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &session.CodingState{TaskID: "ordinary", Workspace: root, Revision: 1, Receipt: &session.CheckReceipt{Status: "passed"}}
	r := &Runner{Workspace: root, CodingRequired: true, Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "bash-mutation-evidence", Continuation: session.Continuation{Coding: c}}
	exec := r.CodingExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		if err := os.WriteFile(filepath.Join(root, "kept.txt"), []byte("after"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "made.txt"), []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		return tool.Result{Text: "done"}, nil
	})
	if _, err := exec(t.Context(), tool.Definition{Name: "bash"}, tool.Args{"cmd": "scripted"}); err != nil {
		t.Fatal(err)
	}
	evidence := r.completionEvidence()
	if !strings.Contains(evidence, "kept.txt") || !strings.Contains(evidence, "made.txt") || !strings.Contains(evidence, "ungrounded") {
		t.Fatalf("bash-mutated files not named as ungrounded: %q", evidence)
	}
}
