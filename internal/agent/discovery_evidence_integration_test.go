package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

// Real file-tool pages exercise the wire metadata contract instead of teaching
// the coverage parser its own hand-written header/footer in a fixture.
func TestDiscoveryEvidenceFromActualFilePages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := &files.Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	var read tool.Definition
	for _, def := range w.Definitions() {
		if def.Name == "read" {
			read = def
		}
	}
	if read.Execute == nil {
		t.Fatal("real read tool unavailable")
	}
	history := discoveryHistory{}
	page := func(offset, limit int64) tool.Result {
		t.Helper()
		args := tool.Args{"path": "sample.txt", "offset": offset, "limit": limit}
		result, err := read.Execute(t.Context(), args)
		if err != nil {
			t.Fatal(err)
		}
		history.record(&observed{Tool: "read", Args: args, Result: result}, 8192)
		return result
	}
	if result := page(1, 3); !result.Truncated {
		t.Fatal("first real page should be incomplete")
	}
	page(5, 3)
	evidence := history.evidence()
	if len(evidence.Sources) != 1 || evidence.Sources[0].CompleteRead {
		t.Fatalf("undelivered fourth line counted complete: %+v", evidence)
	}
	page(4, 1)
	evidence = history.evidence()
	if len(evidence.Sources) != 1 || !evidence.Sources[0].CompleteRead || *evidence.Sources[0].EOFLine != 7 {
		t.Fatalf("actual page union failed: %+v", evidence)
	}
	oldHash := evidence.Sources[0].FileSHA256
	// External changes have no tool-effect receipt; the next actual read hash
	// must still invalidate old coverage before it can be reused.
	if err := os.WriteFile(path, []byte("changed\ntwo\nthree\nfour\nfive\nsix\nseven\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page(5, 3)
	evidence = history.evidence()
	if evidence.Sources[0].CompleteRead || evidence.Sources[0].FileSHA256 == oldHash || evidence.Sources[0].Reads != 1 {
		t.Fatalf("new file version inherited old complete read: %+v", evidence)
	}
	// An overlong line cannot be delivered at all under this real output cap.
	// Metadata saying EOF for other pages must not fill that missing line.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 500)+"\nlast\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w.Caps.MaxBytes = 240
	result := page(1, 1)
	if !result.Truncated || !strings.Contains(result.Text, "shown_range=none") {
		t.Fatalf("fixture failed to clip the actual long line: %+v", result)
	}
	page(2, 1)
	if evidence = history.evidence(); len(evidence.Sources) != 1 || evidence.Sources[0].CompleteRead {
		t.Fatalf("clipped bytes were treated as delivered: %+v", evidence)
	}
}
