package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/store"
)

func TestQueryProjectCurrentKnowledgeAndLegacyGlobals(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	put := func(project, key, body, previous string) *Entry {
		t.Helper()
		e, err := PutKnowledge(ctx, s, project, "old-session", key, body, previous, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	old := put(root, "design/palette", "palette magenta retired", "")
	current := put(root, "design/palette", "palette cerulean current", old.ID)
	put(t.TempDir(), "design/palette", "palette foreign project", "")
	put(root, "harness/reference/tools", "palette internal guide", "")
	if _, err := Add(s, "decision", "palette legacy global", "legacy"); err != nil {
		t.Fatal(err)
	}
	// An old head is still readable by ID; it must not win lexical recall.
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := QueryProject(ctx, s, root, "", "palette", 10, 1000, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cerulean current", "legacy global"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q after reopen: %s", want, got)
		}
	}
	for _, forbidden := range []string{"magenta", "foreign", "internal guide"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("recall leaked %q: %s", forbidden, got)
		}
	}
	stale, err := QueryProject(ctx, s, root, "", "magenta retired", 10, 1000, 4)
	if err != nil || stale != "" {
		t.Fatalf("obsolete version still matched: %q %v", stale, err)
	}
	historical, err := Knowledge(ctx, s, root, "", old.ID, 1, 0, true)
	if err != nil || len(historical) != 1 || historical[0].Body != old.Body {
		t.Fatalf("recall altered historical knowledge: %+v %v", historical, err)
	}
	heads, err := Knowledge(ctx, s, root, "design/palette", "", 1, 0, true)
	if err != nil || len(heads) != 1 || heads[0].ID != current.ID {
		t.Fatalf("current head changed: %+v %v", heads, err)
	}
	legacy, err := Query(s, "", "palette", 10, 1000, 4)
	if err != nil || !strings.Contains(legacy, "legacy global") || strings.Contains(legacy, "cerulean") {
		t.Fatalf("legacy global query changed: %q %v", legacy, err)
	}
	decision, err := QueryProject(ctx, s, root, "decision", "palette", 10, 1000, 4)
	if err != nil || decision != legacy {
		t.Fatalf("kind filter changed: %q %v", decision, err)
	}
}

func TestQueryProjectExistingBudgetAndCancellation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := "palette " + strings.Repeat("é", 100)
	if _, err := PutKnowledge(ctx, s, root, "old-session", "palette", body, "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 30, 1000, 0} {
		got, err := QueryProject(ctx, s, root, "", "palette", 3, limit, 4)
		if err != nil || !utf8.ValidString(got) || limit > 0 && utf8.RuneCountInString(got) > limit {
			t.Fatalf("cap %d violated: %q %v", limit, got, err)
		}
		if limit > 0 && limit < len([]rune(body)) && !strings.HasSuffix(got, "…") {
			t.Fatalf("omission undisclosed: %q", got)
		}
	}
	for _, query := range []string{"unrelated zebras", "what is the"} {
		got, err := QueryProject(ctx, s, root, "", query, 3, 1000, 4)
		if err != nil || got != "" {
			t.Fatalf("unmatched query %q got %q %v", query, got, err)
		}
	}
	if got, err := QueryProject(ctx, s, root, "", "palette", 0, 1000, 4); err != nil || got != "" {
		t.Fatalf("disabled recall: %q %v", got, err)
	}
	if _, err := QueryProject(ctx, s, "", "", "palette", 3, 1000, 4); err == nil {
		t.Fatal("project recall silently fell back to an uncaptured scope")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := QueryProject(canceled, s, root, "", "palette", 3, 1000, 4); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
