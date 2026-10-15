package run

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/store"
	"specific-harness/internal/tools/files"
)

func TestOutputPruningConfiguredWiringAndLegacyNotice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("context:\n  output_prune_after_tokens: 120000\n  inflight_outputs: 20\nschemas:\n  inflight_cap: 5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "kuzu"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, notes, err := New(Deps{Cfg: cfg, Store: s, Server: "http://uncontacted.invalid/v1", Model: "fixture", Window: 262144, In: strings.NewReader(""), Out: io.Discard}, "ordinary user request")
	if err != nil {
		t.Fatal(err)
	}
	if r.Prune.OutputsAgeTokens != 120000 || r.Prune.OutputsPct != 0 || r.Prune.SchemasPct != 0 {
		t.Fatal("legacy percentages wired as active policy", r.Prune)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "120000 estimated context tokens") || !strings.Contains(joined, "percentages are ignored") || !strings.Contains(joined, "context.output_prune_after_tokens") {
		t.Fatal("startup hid effective policy/legacy migration", joined)
	}
	worker := launchDeps(cfg, s, "http://uncontacted.invalid/v1", "fixture", 0, 262144, strings.NewReader(""), io.Discard, &files.Workspace{Path: r.Workspace}, "")
	if worker.Prune.OutputsAgeTokens != 120000 {
		t.Fatal("worker policy drift", worker.Prune)
	}
}
