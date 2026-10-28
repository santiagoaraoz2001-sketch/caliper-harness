package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectRuntimeDoesNotInheritIncompatibleProtocol(t *testing.T) {
	connectionHome(t)
	c := Defaults()
	c.Decision.Runtime = "ollama"
	c.Decision.Protocol = "system_one"
	if err := SaveConnections(c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "project.yaml")
	if err := os.WriteFile(path, []byte("decision:\n  runtime: laya\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadProject(path)
	if err != nil || got.Decision.Runtime != "laya" || got.Decision.Protocol != "" {
		t.Fatalf("inherited incompatible protocol: %+v %v", got.Decision, err)
	}
	if err := os.WriteFile(path, []byte("decision:\n  runtime: laya\n  protocol: system_one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadProject(path); err == nil {
		t.Fatal("explicit invalid protocol was silently cleared")
	}
}
