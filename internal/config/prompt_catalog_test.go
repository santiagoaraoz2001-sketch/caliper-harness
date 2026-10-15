package config

import (
	"os"
	"path/filepath"
	"testing"

	"specific-harness/internal/prompts"
)

func TestPromptSourceOverrideAndSnapshotOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.yaml")
	if err := os.WriteFile(path, []byte(prompts.Builtin().YAML()), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.Prompts.Path = path
	pc, err := cfg.LoadPrompts()
	if err != nil || pc.Source != path {
		t.Fatalf("catalog=%v error=%v", pc, err)
	}
	cfg.PromptCatalog = pc
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if same, err := cfg.LoadPrompts(); err != nil || same != pc {
		t.Fatal("child/request lost admitted snapshot")
	}
	cfg.PromptCatalog = nil
	if _, err := cfg.LoadPrompts(); err == nil {
		t.Fatal("next run ignored missing explicit source")
	}
	cfg.Templates.Path = "another-source"
	if _, err := cfg.LoadPrompts(); err == nil {
		t.Fatal("two explicit prompt sources admitted")
	}
}
