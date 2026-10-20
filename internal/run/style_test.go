package run

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"

	"gopkg.in/yaml.v3"
)

func TestProjectStyleSnapshotWithDecisionOff(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	first, err := memory.AppendStyleguideRule(ctx, s, root, "user-session", "Keep reducers pure.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AppendStyleguideRule(ctx, s, t.TempDir(), "other", "FOREIGN_STYLE"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Behavior.Path = ""
	cfg.Decision.Disabled = true
	cfg.Decision.StyleEnforcement = false
	cfg.Recall.TopN = 0
	build := func() string {
		t.Helper()
		r, _, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "fixture", Out: io.Discard}, "request")
		if err != nil {
			t.Fatal(err)
		}
		if r.StyleEnforcement || !strings.Contains(r.Behavior, root) || strings.Contains(r.Behavior, "FOREIGN_STYLE") {
			t.Fatalf("style scope or advice state: %+v", r)
		}
		return r.Behavior
	}
	before := build()
	children, err := LaunchDeps(cfg, s, "http://unused", "fixture", 1, 32768, nil, io.Discard)
	if err != nil || children.Behavior != before {
		t.Fatalf("main and child/review snapshot differ: %v\n%s\n%s", err, before, children.Behavior)
	}
	second, err := memory.AppendStyleguideRule(ctx, s, root, "later-user", "Use stable IDs.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, first.ID) || strings.Contains(before, second.ID) || strings.Contains(children.Behavior, "Use stable IDs.") {
		t.Fatal("existing role snapshot changed")
	}
	after := build()
	if !strings.Contains(after, second.ID) || !strings.Contains(after, "Use stable IDs.") || strings.Contains(after, first.ID) {
		t.Fatal("new run did not capture the current revision")
	}
	if _, err := memory.PutKnowledge(ctx, s, root, "user-clear", memory.StyleguideKey, "", second.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	empty, _, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "fixture", Out: io.Discard}, "request")
	if err != nil || empty.Behavior != "" {
		t.Fatalf("clear did not omit new guidance: %q %v", empty.Behavior, err)
	}
}

func TestOldCatalogProjectStyleFallbackIsDisclosed(t *testing.T) {
	raw, err := os.ReadFile("../prompts/prompts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Texts map[string]string `yaml:"texts"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	delete(document.Texts, "continuation.project_style")
	older, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "older-prompts.yaml")
	if err := os.WriteFile(path, older, 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := prompts.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	guide, err := memory.AppendStyleguideRule(context.Background(), s, root, "user", "Use explicit names.")
	if err != nil {
		t.Fatal(err)
	}
	combined, body, note := projectStyle(s, root, "UNCHANGED_GLOBAL_BEHAVIOR", pc)
	if !strings.HasPrefix(combined, "UNCHANGED_GLOBAL_BEHAVIOR\n\n") || !strings.Contains(combined, guide.ID) || body != guide.Body || !strings.Contains(note, "shipped project-style wrapper") {
		t.Fatalf("fallback %q %q %q", combined, body, note)
	}
	document.Texts["continuation.project_style"] = ""
	blank, _ := yaml.Marshal(document)
	if err := os.WriteFile(path, blank, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prompts.Load(path); err == nil || !strings.Contains(err.Error(), "empty key continuation.project_style") {
		t.Fatalf("explicit blank must follow existing rejection, not silently inject: %v", err)
	}
}
