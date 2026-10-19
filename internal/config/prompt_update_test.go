package config

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/prompts"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUpdatePreservesExplicitCustomizedCatalogWithoutOptionalAssistedSetupKey(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	var doc struct {
		Texts map[string]string `yaml:"texts"`
	}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc.Texts, "setup.assisted") // Customized catalog from before v0.1.5.
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	old := t.TempDir()
	oldPath := filepath.Join(old, "prompts.yaml")
	next := filepath.Join(t.TempDir(), "prompts.yaml")
	if err := os.WriteFile(oldPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte(prompts.Builtin().YAML()), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.Prompts.Path = oldPath
	if _, err := cfg.LoadPrompts(); err != nil {
		t.Fatal("older catalog prevented ordinary coding", err)
	}
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err != nil {
		t.Fatal("optional UI guide blocked compatible update", err)
	}
	if after, err := os.ReadFile(oldPath); err != nil || !bytes.Equal(after, raw) {
		t.Fatal("original customized catalog changed", err)
	}
	connections, _ := ConnectionsPath()
	if _, err := os.Stat(connections); !os.IsNotExist(err) {
		t.Fatal("failed compatibility check changed connections", err)
	}
}

func TestUpdatePreservesCustomShippedPromptsOutsideVersions(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	old := t.TempDir()
	next := filepath.Join(t.TempDir(), "prompts.yaml")
	original := []byte(prompts.Builtin().YAML())
	custom := append([]byte("# user-authored guidance retained exactly\n"), original...)
	oldPath := filepath.Join(old, "prompts.yaml")
	if err := os.WriteFile(oldPath, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "MANIFEST.sha256"), []byte(fmt.Sprintf("%x  ./prompts.yaml\n", sha256.Sum256(original))), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Coding.CheckCommand = "keep exact check"
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err != nil {
		t.Fatal(err)
	}
	after, used, err := Load(DefaultFile())
	if err != nil || !used {
		t.Fatal(err)
	}
	root, _ := UserDataDir()
	if !strings.HasPrefix(after.Prompts.Path, filepath.Join(root, "prompts")) || after.Coding.CheckCommand != cfg.Coding.CheckCommand {
		t.Fatalf("binding lost: %+v", after)
	}
	raw, err := os.ReadFile(after.Prompts.Path)
	if err != nil || string(raw) != string(custom) {
		t.Fatalf("custom bytes lost: %v", err)
	}
	oldRaw, _ := os.ReadFile(oldPath)
	if string(oldRaw) != string(custom) {
		t.Fatal("old version changed")
	}
}
func TestUpdateLeavesDefaultCatalogAndRefusesIncompatibleCustom(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	old := t.TempDir()
	oldPath := filepath.Join(old, "prompts.yaml")
	next := filepath.Join(t.TempDir(), "prompts.yaml")
	original := []byte(prompts.Builtin().YAML())
	for _, path := range []string{oldPath, next} {
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(old, "MANIFEST.sha256"), []byte(fmt.Sprintf("%x  ./prompts.yaml\n", sha256.Sum256(original))), 0600)
	cfg, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DefaultFile()); !os.IsNotExist(err) {
		t.Fatal("default upgrade pinned old catalog")
	}
	explicit := filepath.Join(t.TempDir(), "custom.yaml")
	os.WriteFile(explicit, original, 0600)
	cfg.Prompts.Path = explicit
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DefaultFile()); !os.IsNotExist(err) {
		t.Fatal("explicit custom path rewritten")
	}
	os.WriteFile(explicit, []byte("texts:\n  wrong: invalid\n"), 0600)
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err == nil {
		t.Fatal("incompatible custom catalog accepted")
	}
}

func TestUpdatePreservedPromptsApplyAcrossWorkspacesWithoutLeakingOverrides(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	old := t.TempDir()
	next := filepath.Join(t.TempDir(), "prompts.yaml")
	raw := []byte(prompts.Builtin().YAML())
	custom := append([]byte("# machine customization\n"), raw...)
	os.WriteFile(filepath.Join(old, "prompts.yaml"), custom, 0600)
	os.WriteFile(next, raw, 0600)
	os.WriteFile(filepath.Join(old, "MANIFEST.sha256"), []byte(fmt.Sprintf("%x  ./prompts.yaml\n", sha256.Sum256(raw))), 0600)
	cfg, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err != nil {
		t.Fatal(err)
	}
	first, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	second, _, err := Load(DefaultFile())
	if err != nil || second.Prompts.Path != first.Prompts.Path {
		t.Fatalf("other workspace lost machine prompt: %s %v", second.Prompts.Path, err)
	}
	local := filepath.Join(t.TempDir(), "local.yaml")
	os.WriteFile(local, raw, 0600)
	os.WriteFile(DefaultPath, []byte("prompts:\n  path: "+local+"\n"), 0600)
	explicit, _, err := Load(DefaultFile())
	if err != nil || explicit.Prompts.Path != local {
		t.Fatal("project override lost", err)
	}
	if err := SaveConnections(explicit); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	third, _, err := Load(DefaultFile())
	if err != nil || third.Prompts.Path != first.Prompts.Path {
		t.Fatal("workspace-specific prompts leaked to machine connections", err)
	}
}

func TestPromptUpdateBacksUpOriginalSettings(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	cfg, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(DefaultFile(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := SaveConnections(cfg); err != nil {
		t.Fatal(err)
	}
	connectionPath, _ := ConnectionsPath()
	originals := map[string][]byte{}
	for _, path := range []string{DefaultFile(), connectionPath} {
		originals[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	old := t.TempDir()
	next := filepath.Join(t.TempDir(), "prompts.yaml")
	raw := []byte(prompts.Builtin().YAML())
	os.WriteFile(filepath.Join(old, "prompts.yaml"), append([]byte("# custom\n"), raw...), 0600)
	os.WriteFile(next, raw, 0600)
	os.WriteFile(filepath.Join(old, "MANIFEST.sha256"), []byte(fmt.Sprintf("%x  ./prompts.yaml\n", sha256.Sum256(raw))), 0600)
	if err := PreserveCustomPrompts(DefaultFile(), cfg, old, next); err != nil {
		t.Fatal(err)
	}
	root, _ := UserDataDir()
	backups, err := os.ReadDir(filepath.Join(root, "update-backups"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups %v %v", backups, err)
	}
	for _, b := range backups {
		dir := filepath.Join(root, "update-backups", b.Name())
		source, err := os.ReadFile(filepath.Join(dir, "source.txt"))
		if err != nil {
			t.Fatal(err)
		}
		copy, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
		if err != nil || string(copy) != string(originals[string(source)]) {
			t.Fatal("original settings not preserved", err)
		}
	}
}

func TestInstalledPromptPreservationDoesNotUsePackageWorkspace(t *testing.T) {
	isolatedUser(t)
	packageDir := t.TempDir()
	t.Chdir(packageDir)
	old := t.TempDir()
	next := filepath.Join(t.TempDir(), "prompts.yaml")
	original := []byte(prompts.Builtin().YAML())
	custom := append([]byte("# customized installation\n"), original...)
	if err := os.WriteFile(filepath.Join(old, "prompts.yaml"), custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, original, 0600); err != nil {
		t.Fatal(err)
	}
	// An unrelated package-local config must neither be read nor overwritten.
	unrelated := []byte("not_a_caliper_key: preserve\n")
	if err := os.WriteFile(DefaultPath, unrelated, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.Model.Name = "keep-selected-model"
	if err := SaveConnections(cfg); err != nil {
		t.Fatal(err)
	}
	if err := PreserveInstalledPrompts(old, next); err != nil {
		t.Fatal(err)
	}
	after := Defaults()
	if err := loadConnections(&after); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(after.Prompts.Path)
	if err != nil || string(raw) != string(custom) || after.Model.Name != cfg.Model.Name {
		t.Fatalf("preservation failed: %v %+v", err, after.Prompts)
	}
	raw, err = os.ReadFile(DefaultPath)
	if err != nil || string(raw) != string(unrelated) {
		t.Fatal("package-local configuration changed")
	}
	workspace, err := WorkspaceDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("package workspace created: %v", err)
	}
	// Existing machine overrides are validated; incompatibility cannot silently activate.
	if err := os.WriteFile(after.Prompts.Path, []byte("texts: {wrong: invalid}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PreserveInstalledPrompts(old, next); err == nil {
		t.Fatal("incompatible machine override accepted")
	}
}
