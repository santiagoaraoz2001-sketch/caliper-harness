package config

import (
	"os"
	"path/filepath"
	"specific-harness/internal/testfs"
	"testing"
)

func isolatedUser(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
}

func TestWorkspaceDefaultsOutsideProjectAndStable(t *testing.T) {
	isolatedUser(t)
	project := t.TempDir()
	t.Chdir(project)
	location, err := WorkspaceDir()
	if err != nil {
		t.Fatal(err)
	}
	c, used, err := Load(DefaultFile())
	if err != nil || used {
		t.Fatalf("load %v %v", used, err)
	}
	for _, path := range []string{DefaultFile(), c.Store.Path, c.Sessions.Path, c.Behavior.Path} {
		if filepath.Dir(path) != location {
			t.Fatalf("not centralized: %s", path)
		}
	}
	c.Decision.Disabled = true
	c.Prompts.Path = filepath.Join(t.TempDir(), "custom-prompts.yaml")
	if err := Save(DefaultFile(), c); err != nil {
		t.Fatal(err)
	}
	after, used, err := Load(DefaultFile())
	if err != nil || !used || !after.Decision.Disabled || after.Prompts.Path != c.Prompts.Path {
		t.Fatalf("roundtrip: %+v %v", after, err)
	}
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 0 {
		t.Fatalf("project clutter: %v %v", entries, err)
	}
	other := t.TempDir()
	t.Chdir(other)
	second, err := WorkspaceDir()
	if err != nil || second == location {
		t.Fatalf("project collision: %s %v", second, err)
	}
	t.Chdir(project)
	restored, _ := WorkspaceDir()
	if restored != location {
		t.Fatal("workspace identity changed")
	}
}

func TestWorkspaceDefaultsPreserveLegacyAndExplicitPaths(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("data/sessions", 0700); err != nil {
		t.Fatal(err)
	}
	for p, content := range map[string]string{"data/kuzu": "existing graph", "data/sessions/old.jsonl": "old work", "behavior.md": "custom", LegacyPath: "params:\n  temperature: 0.2\n"} {
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if DefaultFile() != LegacyPath {
		t.Fatal("legacy config missed")
	}
	c, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.Path != "data/kuzu" || c.Sessions.Path != "data/sessions" || c.Behavior.Path != "behavior.md" {
		t.Fatalf("legacy paths changed: %+v", c)
	}
	raw := "store:\n  path: explicit-db\nsessions:\n  path: explicit-sessions\nbehavior:\n  path: explicit-behavior.md\nprompts:\n  path: custom.yaml\n"
	if err := os.WriteFile(DefaultPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err = Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.Path != "explicit-db" || c.Sessions.Path != "explicit-sessions" || c.Behavior.Path != "explicit-behavior.md" || c.Prompts.Path != "custom.yaml" {
		t.Fatal("explicit paths altered")
	}
	for p, want := range map[string]string{"data/kuzu": "existing graph", "data/sessions/old.jsonl": "old work", "behavior.md": "custom"} {
		got, _ := os.ReadFile(p)
		if string(got) != want {
			t.Fatalf("work changed: %s", p)
		}
	}
}

func TestWorkspaceSymlinkHasSameIdentity(t *testing.T) {
	isolatedUser(t)
	project := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := testfs.Symlink(project, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Chdir(project)
	a, err := WorkspaceDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(alias)
	b, err := WorkspaceDir()
	if err != nil || a != b {
		t.Fatalf("alias split: %s %s %v", a, b, err)
	}
}

func TestWorkspaceLegacyHalfHistoryKeepsPair(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("data/sessions", 0700); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(DefaultFile())
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.Path != "data/kuzu" || c.Sessions.Path != "data/sessions" {
		t.Fatal("legacy graph/session history split")
	}
}
