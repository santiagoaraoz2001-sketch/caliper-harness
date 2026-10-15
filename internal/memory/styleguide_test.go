package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/store"
)

func TestStyleguideStartsEmptyAndEvolves(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, root := context.Background(), t.TempDir()
	if got, err := ReadStyleguide(ctx, s, root); err != nil || got != "" {
		t.Fatalf("unpopulated styleguide: %q %v", got, err)
	}
	if _, err := AppendStyleguideRule(ctx, s, root, "session-1", "  "); err == nil {
		t.Fatal("blank rule accepted")
	}
	if _, err := AppendStyleguideRule(ctx, s, root, "session-1", "Prefer early returns over nested conditionals."); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendStyleguideRule(ctx, s, root, "session-2", "No one-letter variable names outside loops."); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStyleguide(ctx, s, root)
	if err != nil || !strings.Contains(got, "early returns") || !strings.Contains(got, "one-letter") {
		t.Fatalf("evolved styleguide lost a rule: %q %v", got, err)
	}
	// The same document stays reachable through the ordinary knowledge API.
	entries, err := Knowledge(ctx, s, root, StyleguideKey, "", 1, 0, true)
	if err != nil || len(entries) != 1 || entries[0].Body != got {
		t.Fatalf("styleguide not readable as knowledge: %v %v", entries, err)
	}
	// It is scoped to its own workspace directory.
	if other, err := ReadStyleguide(ctx, s, t.TempDir()); err != nil || other != "" {
		t.Fatalf("styleguide leaked across workspaces: %q %v", other, err)
	}
}
