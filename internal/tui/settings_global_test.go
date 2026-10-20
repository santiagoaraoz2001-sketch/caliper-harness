package tui

import (
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"strings"
	"testing"
)

func TestSettingsDefaultScopePersistsAcrossProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	a, b := t.TempDir(), t.TempDir()
	t.Chdir(a)
	c, _, e := config.Load(config.DefaultFile())
	if e != nil {
		t.Fatal(e)
	}
	m := New(c, "test")
	m.CfgPath = config.DefaultFile()
	m = submit(m, "/settings")
	m.applyField(settingsField("context", "window", "int"), "65536")
	m.applyField(settingsField("params", "temperature", "float"), "0.2")
	m.applyField(settingsField("auth", "mode", "auth"), config.AuthYOLO)
	m.applyField(settingsField("decision", "disabled", "bool"), "true")
	m.applyField(settingsField("coding", "check_command", "text"), "project-a")
	m = key(m, "s")
	if a := lastAlert(m); a.Kind == "err" || !strings.Contains(a.Text, "personal defaults") {
		t.Fatalf("save receipt: %+v", a)
	}
	t.Chdir(b)
	got, _, e := config.Load(config.DefaultFile())
	if e != nil {
		t.Fatal(e)
	}
	if got.Context.Window != 65536 || got.Params.Temperature != 0.2 || got.Auth.Mode != config.AuthYOLO || !got.Decision.Disabled || got.Coding.CheckCommand != "" {
		t.Fatalf("cross-directory Settings: %+v", got)
	}
	n := New(got, "test")
	n.CfgPath = config.DefaultFile()
	if !strings.Contains(n.renderSettings(80, 22), "personal defaults") {
		t.Fatal("global scope is not visible")
	}
	entries, _ := os.ReadDir(b)
	if len(entries) != 0 {
		t.Fatal("working directory litter")
	}
}
