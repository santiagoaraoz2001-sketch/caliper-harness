package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"specific-harness/internal/config"
)

func TestSessionResumePreservesConfigurationScope(t *testing.T) {
	home := t.TempDir()
	for key, value := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"APPDATA": filepath.Join(home, "config"), "LOCALAPPDATA": filepath.Join(home, "local"),
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"),
	} {
		t.Setenv(key, value)
	}
	project := t.TempDir()
	t.Chdir(project)
	automatic := config.DefaultFile()
	global, _, err := config.Load(automatic)
	if err != nil {
		t.Fatal(err)
	}
	global.Context.Window = 65536
	global.Model.Name = "personal-model"
	if err = config.SaveSettings(automatic, global); err != nil {
		t.Fatal(err)
	}
	// A pre-upgrade automatic snapshot still contains old personal choices.
	old := global
	old.Context.Window, old.Model.Name = 32768, "old-project-model"
	old.Coding.CheckCommand = "project-check"
	if err = config.Save(automatic, old); err != nil {
		t.Fatal(err)
	}
	authored := filepath.Join(project, "authored.yaml")
	if err = config.Save(authored, old); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		path     string
		explicit bool
	}{
		{"automatic workspace", automatic, false},
		{"explicit managed path", automatic, true},
		{"authored project", authored, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := config.Load
			if tc.explicit {
				loader = config.LoadProject
			}
			before, _, err := loader(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			m := New(before, "test")
			m.CfgPath = tc.path
			m.Sessions.Detail = &SessionDetail{ID: "saved-session"}
			m.Input.SetValue("keep my draft")
			tabs := len(m.Tabs)
			got := stubTerminal(t, nil)
			m.resumeSession()
			if len(*got) < 4 || (*got)[1] != "tui" || (*got)[len(*got)-2] != "--resume" || (*got)[len(*got)-1] != "saved-session" {
				t.Fatalf("invalid resume command: %v", *got)
			}
			resumePath, resumeLoader := config.DefaultFile(), config.Load
			for i := 2; i < len(*got)-1; i++ {
				if (*got)[i] == "--config" {
					resumePath, resumeLoader = (*got)[i+1], config.LoadProject
				}
			}
			after, _, err := resumeLoader(resumePath)
			if err != nil {
				t.Fatal(err)
			}
			if before.Context.Window != after.Context.Window || before.Model.Name != after.Model.Name || before.Coding.CheckCommand != after.Coding.CheckCommand || before.Store.Path != after.Store.Path || before.Sessions.Path != after.Sessions.Path {
				t.Fatalf("resume changed effective configuration: window %d → %d, model %q → %q, argv=%v",
					before.Context.Window, after.Context.Window, before.Model.Name, after.Model.Name, *got)
			}
			if tc.path == automatic && before.ProjectOverride != after.ProjectOverride {
				t.Fatalf("managed configuration scope changed: %v → %v", before.ProjectOverride, after.ProjectOverride)
			}
			if after.Coding.CheckCommand != "project-check" || len(m.Tabs) != tabs || m.Input.Value() != "keep my draft" || m.Sessions.Detail.ID != "saved-session" {
				t.Fatal("resume lost project state or changed the current tab")
			}
			current, err := os.ReadFile(tc.path)
			if err != nil || !bytes.Equal(raw, current) || !reflect.DeepEqual(before, m.Cfg) {
				t.Fatal("resume rewrote the configuration", err)
			}
		})
	}
}
