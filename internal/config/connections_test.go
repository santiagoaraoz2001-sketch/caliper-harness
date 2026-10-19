package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func connectionHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
}
func TestConnectionsReusableWithoutProjectState(t *testing.T) {
	connectionHome(t)
	c := Defaults()
	c.Model.Name = "selected"
	c.Decision.Model = "judge"
	c.Coding.CheckCommand = "private-project-check"
	c.Store.Path = "private-graph"
	if err := SaveConnections(c); err != nil {
		t.Fatal(err)
	}
	path, _ := ConnectionsPath()
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("project state leaked into connections")
	}
	other, used, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || used || other.Model.Name != "selected" || other.Coding.CheckCommand != "" {
		t.Fatalf("global load: %+v %v", other.Model, err)
	}
	project := filepath.Join(t.TempDir(), "explicit.yaml")
	os.WriteFile(project, []byte("model:\n  name: explicit\ncoding:\n  check_command: go test ./...\n"), 0600)
	got, used, err := Load(project)
	if err != nil || !used || got.Model.Name != "explicit" || got.Decision.Model != "judge" {
		t.Fatalf("project override: %+v %v", got, err)
	}
}
func TestConnectionsStrictAndRepeatSave(t *testing.T) {
	connectionHome(t)
	c := Defaults()
	if err := SaveConnections(c); err != nil {
		t.Fatal(err)
	}
	c.Model.Name = "new"
	if err := SaveConnections(c); err != nil {
		t.Fatal(err)
	}
	path, _ := ConnectionsPath()
	for _, raw := range []string{"unknown: true\n", "model: {}\n---\nmodel: {}\n"} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, _, err := Load("absent.yaml"); err == nil {
			t.Fatal("accepted malformed global file")
		}
	}
}
