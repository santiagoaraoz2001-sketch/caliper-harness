package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/store"
)

func TestRecallLocatorsAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		name, query, receipt string
		entry                *Entry
	}{
		{"key", "render-cache", `key="render-cache"`, &Entry{Kind: "note", Key: "render-cache", Body: "Keep derived values immutable."}},
		{"file", "src/game.ts", `file="src/game.ts"`, &Entry{Kind: "note", FilePath: "src/game.ts", Body: "Keep derived values immutable."}},
		{"Chinese", "渲染", "渲染 使用纯函数", &Entry{Kind: "note", Body: "渲染 使用纯函数"}},
		{"accented case", "RENDERIZACIÓN", "renderización", &Entry{Kind: "note", Body: "La renderización usa funciones puras."}},
		{"English", "derived", "Keep derived values immutable.", &Entry{Kind: "note", Body: "Keep derived values immutable."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Recall([]*Entry{tc.entry}, tc.query, 3, 1000, 2)
			if !strings.Contains(got, tc.receipt) {
				t.Fatalf("lookup %q lost its matching note or locator: %q", tc.query, got)
			}
			for _, limit := range []int{1, 8, 30} {
				got = Recall([]*Entry{tc.entry}, tc.query, 3, limit, 2)
				if !utf8.ValidString(got) || utf8.RuneCountInString(got) > limit {
					t.Fatalf("existing budget %d violated: %q", limit, got)
				}
			}
		})
	}
	entry := &Entry{Kind: "note", Key: "渲染", Body: "Use pure functions."}
	for _, query := range []string{"", "渲", "a", "!!!", "what is the"} {
		if got := Recall([]*Entry{entry}, query, 3, 1000, 2); got != "" {
			t.Fatalf("empty, short or stopword query %q matched: %q", query, got)
		}
	}
	if got := Recall([]*Entry{entry}, "渲染", 3, 1000, 3); got != "" {
		t.Fatalf("word floor must count characters, not UTF-8 bytes: %q", got)
	}
	if got := Recall([]*Entry{entry}, "渲", 3, 1000, 0); got == "" {
		t.Fatal("existing zero word floor should permit one-character queries")
	}
}

func TestRecallMetadataScoresOncePerQueryWordAndKeepsTies(t *testing.T) {
	first := &Entry{Kind: "note", Body: "render cache"}
	second := &Entry{Kind: "note", Key: "render-cache", FilePath: "render/cache.go", Body: "render cache"}
	third := &Entry{Kind: "note", Key: "render", Body: "other"}
	entries := []*Entry{third, first, second}
	ranked := Rank(entries, "render cache", 2)
	if ranked[0] != first || ranked[1] != second || ranked[2] != third {
		t.Fatalf("repeated words across metadata changed distinct-word scores or stable ties: %+v", ranked)
	}
	if entries[0] != third || entries[1] != first || entries[2] != second {
		t.Fatal("ranking changed the caller's slice")
	}
}

func TestQueryProjectRecallLocatorsSurviveStoreReopen(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	put := func(project, key, body, previous, path string) *Entry {
		t.Helper()
		entry, err := PutKnowledge(ctx, s, project, "old-session", key, body, previous, "", path)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	old := put(root, "render-cache", "Obsolete body.", "", "")
	put(root, "render-cache", "Keep derived values immutable.", old.ID, "src/game.ts")
	put(root, "界面设计", "渲染 使用纯函数", "", "")
	put(t.TempDir(), "render-cache", "Foreign body.", "", "")
	put(root, "harness/reference/render-cache", "Harness guide body.", "", "")
	s.Close()
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, query := range []string{"render-cache", "src/game.ts", "渲染", "界面设计"} {
		got, err := QueryProject(ctx, s, root, "", query, 10, 1000, 2)
		if err != nil || got == "" {
			t.Fatalf("stored locator %q could not be recalled: %q %v", query, got, err)
		}
		for _, excluded := range []string{"Obsolete", "Foreign", "Harness guide"} {
			if strings.Contains(got, excluded) {
				t.Fatalf("query %q leaked %s: %q", query, excluded, got)
			}
		}
	}
	got, err := QueryProject(ctx, s, root, "", "src/game.ts", 3, 1000, 2)
	if err != nil || !strings.Contains(got, `key="render-cache"`) || !strings.Contains(got, fmt.Sprintf("file=%q", filepath.Join("src", "game.ts"))) {
		t.Fatalf("stored locator receipt missing: %q %v", got, err)
	}
}
