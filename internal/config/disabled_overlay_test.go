package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSavedDecisionFalseOverridesPersonalTrue(t *testing.T) {
	connectionHome(t)
	path := filepath.Join(t.TempDir(), "project.yaml")
	for _, personal := range []bool{true, false} {
		base := Defaults()
		base.Decision.Disabled = personal
		if err := SaveConnections(base); err != nil {
			t.Fatal(err)
		}
		project := base
		project.ProjectOverride = true
		project.Decision.Disabled = !personal
		project.Decision.Model = "selected-judge"
		if err := SaveSettings(path, project); err != nil {
			t.Fatal(err)
		}
		got, _, err := LoadProject(path)
		if err != nil || got.Decision.Disabled != !personal || got.Decision.Model != project.Decision.Model {
			t.Fatalf("roundtrip: %+v %v", got.Decision, err)
		}
		if err := os.WriteFile(path, []byte("decision:\n  model: inherited-availability\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got, _, err = LoadProject(path)
		if err != nil || got.Decision.Disabled != personal {
			t.Fatalf("omitted field did not inherit: %+v %v", got.Decision, err)
		}
	}
}
