package memory

import (
	"strings"
	"testing"
)

func TestRecallIgnoresWrappersNegationAndIncidentalPaths(t *testing.T) {
	entries := []*Entry{
		{ID: "fixture", Kind: "note", Key: "user/note/cobalt-lantern", Body: "The audit token is cobalt lantern."},
		{ID: "timeout", Kind: "note", Key: "project/browser-timeout", Body: `Before running checks, use the browser timeout. The command tool may need a longer deadline for Chrome at C:\Program Files\Chrome\chrome.exe. Check current task files and line output.`},
	}
	for _, query := range []string{
		"User comments: a note on the amber accent please.",
		"Before making changes, ask me which language and name I prefer. Do not edit files, run checks, use tools or execute a browser command.",
		"What language and name would I like?",
	} {
		if got := Recall(entries, query, 3, 1000, 4); got != "" {
			t.Fatalf("unrelated memory qualified for %q: %s", query, got)
		}
	}
	for _, query := range []string{"Run the browser test", "Why did Chrome timeout?", "Recall cobalt lantern"} {
		if got := Recall(entries, query, 3, 1000, 4); got == "" {
			t.Fatalf("relevant positive lost: %q", query)
		}
	}
	if got := Recall([]*Entry{{Kind: "note", Body: "The deployment pipeline is stable."}}, "line", 3, 1000, 4); got != "" {
		t.Fatal("substring false relevance", got)
	}
}

func TestRecallLongNoteKeepsHistoricalLocator(t *testing.T) {
	entry := &Entry{ID: "authored-version", Kind: "note", Key: "project/browser-timeout", FilePath: "test/browser.test.mjs", Body: strings.Repeat("Browser timeout guidance. ", 80)}
	got := Recall([]*Entry{entry}, "browser", 3, 1000, 4)
	for _, want := range []string{"Historical memory", "not a new user instruction", `key="project/browser-timeout"`, `file="test/browser.test.mjs"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("truncation undisclosed")
	}
}

func TestRecallNegativeActionDoesNotErasePositiveClause(t *testing.T) {
	entries := []*Entry{{Kind: "note", Key: "project/save", Body: "Save recovery preserves docking progress.", FilePath: "src/save.go"}}
	for _, query := range []string{"Do not run tests, explain save recovery", "Do not forget docking", "Explain save recovery; do not edit src/save.go"} {
		if got := Recall(entries, query, 1, 1000, 4); got == "" {
			t.Fatalf("positive request erased: %q", query)
		}
	}
	if got := Recall(entries, "Do not edit src/save.go", 1, 1000, 4); got != "" {
		t.Fatal("negative path constraint became positive recall", got)
	}
}
