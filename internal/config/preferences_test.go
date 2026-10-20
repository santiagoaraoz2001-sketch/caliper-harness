package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPersonalSettingsAcrossDirectories(t *testing.T) {
	isolatedUser(t)
	a, b := t.TempDir(), t.TempDir()
	t.Chdir(a)
	c, _, e := Load(DefaultFile())
	if e != nil {
		t.Fatal(e)
	}
	c.Context.Window = 131072
	c.Params.Temperature = 0
	c.Params.Thinking = false
	c.Params.MaxOutputTokens = 32768
	c.Auth.Mode = AuthYOLO
	c.Decision.Disabled = true
	c.Model.Name = "coding"
	c.Coding.CheckCommand = "project-a-check"
	if e = SaveSettings(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{a, b, a} {
		t.Chdir(dir)
		got, _, e := Load(DefaultFile())
		if e != nil {
			t.Fatal(e)
		}
		if got.Context.Window != 131072 || got.Params.Temperature != 0 || got.Params.Thinking || got.Auth.Mode != AuthYOLO || !got.Decision.Disabled || got.Model.Name != "coding" || got.Params.MaxOutputTokens != 32768 {
			t.Fatalf("lost globals in %s: %+v", dir, got)
		}
		want := ""
		if dir == a {
			want = "project-a-check"
		}
		if got.Coding.CheckCommand != want {
			t.Fatal("project check leaked or lost")
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("working directory litter")
		}
	}
}
func TestPersonalSettingsExplicitProjectOverrides(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	c, _, _ := Load(DefaultFile())
	c.Context.Window = 131072
	if e := SaveSettings(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	explicit := filepath.Join(t.TempDir(), "project.yaml")
	raw := []byte("context:\n  window: 8192\nparams:\n  thinking: false\n")
	if e := os.WriteFile(explicit, raw, 0600); e != nil {
		t.Fatal(e)
	}
	got, _, e := Load(explicit)
	if e != nil || got.Context.Window != 8192 || got.Params.Thinking {
		t.Fatalf("explicit override: %+v %v", got, e)
	}
	got.Context.Window = 16384
	if e = SaveSettings(explicit, got); e != nil {
		t.Fatal(e)
	}
	global, _, e := Load(DefaultFile())
	if e != nil || global.Context.Window != 131072 {
		t.Fatal("project save changed global defaults", e)
	}
}
func TestPersonalSettingsMigrateOnlyManagedSnapshot(t *testing.T) {
	isolatedUser(t)
	a, b := t.TempDir(), t.TempDir()
	t.Chdir(b)
	old, _, _ := Load(DefaultFile())
	old.Context.Window = 4096
	old.Model.Name = "old"
	old.Coding.CheckCommand = "check-b"
	old.Store.Path = "custom-graph-b"
	if e := Save(DefaultFile(), old); e != nil {
		t.Fatal(e)
	}
	bpath := DefaultFile()
	braw, _ := os.ReadFile(bpath)
	t.Chdir(a)
	c, _, _ := Load(DefaultFile())
	c.Context.Window = 65536
	c.Model.Name = "new"
	if e := SaveSettings(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	t.Chdir(b)
	got, _, e := Load(DefaultFile())
	if e != nil {
		t.Fatal(e)
	}
	if got.Context.Window != 65536 || got.Model.Name != "new" || got.Coding.CheckCommand != "check-b" || got.Store.Path != "custom-graph-b" {
		t.Fatalf("legacy global overlay: %+v", got)
	}
	raw, _ := os.ReadFile(bpath)
	if !bytes.Equal(raw, braw) {
		t.Fatal("loading rewrote old snapshot")
	}
	if e = SaveSettings(bpath, got); e != nil {
		t.Fatal(e)
	}
	backup, _ := os.ReadFile(bpath + ".before-global-settings")
	if !bytes.Equal(backup, braw) {
		t.Fatal("original snapshot not backed up exactly")
	}
	raw, _ = os.ReadFile(bpath)
	for _, key := range []string{"context:", "model:", "params:"} {
		if bytes.Contains(raw, []byte(key)) {
			t.Fatal("global section frozen in workspace", key)
		}
	}
}
func TestConnectionsDoNotOverwritePersonalSettings(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	c, _, _ := Load(DefaultFile())
	c.Context.Window = 65536
	if e := SaveSettings(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	p, _ := PreferencesPath()
	before, _ := os.ReadFile(p)
	c.Context.Window = 4096
	c.Model.Name = "another"
	if e := SaveConnections(c); e != nil {
		t.Fatal(e)
	}
	if e := SaveEffective(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("connection save rewrote personal preferences")
	}
	got, _, e := Load(DefaultFile())
	if e != nil || got.Context.Window != 65536 || got.Model.Name != "another" {
		t.Fatal("connection interaction", e)
	}
}
func TestPersonalSettingsRefuseInvalidWithoutWrites(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	c, _, _ := Load(DefaultFile())
	if e := SaveSettings(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	p, _ := PreferencesPath()
	before, _ := os.ReadFile(p)
	c.Params.Temperature = -1
	if e := SaveSettings(DefaultFile(), c); e == nil {
		t.Fatal("accepted invalid value")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid save wrote settings")
	}
	for _, raw := range []string{"unknown: true\n", "context: {}\n---\nparams: {}\n"} {
		os.WriteFile(p, []byte(raw), 0600)
		if _, _, e := Load(DefaultFile()); e == nil {
			t.Fatal("accepted invalid personal file")
		}
	}
}

func TestExplicitManagedFilenameRetainsProjectScope(t *testing.T) {
	isolatedUser(t)
	t.Chdir(t.TempDir())
	c, _, _ := Load(DefaultFile())
	c.Context.Window = 65536
	if e := SaveSettings(DefaultFile(), c); e != nil {
		t.Fatal(e)
	}
	path := DefaultFile()
	if e := os.WriteFile(path, []byte("context:\n  window: 8192\n"), 0600); e != nil {
		t.Fatal(e)
	}
	global, _, e := Load(path)
	if e != nil || global.Context.Window != 65536 || global.ProjectOverride {
		t.Fatal("automatic workspace froze global context", e)
	}
	explicit, _, e := LoadProject(path)
	if e != nil || explicit.Context.Window != 8192 || !explicit.ProjectOverride {
		t.Fatal("explicit managed config lost scope", e)
	}
	explicit.Context.Window = 32768
	if e = SaveSettings(path, explicit); e != nil {
		t.Fatal(e)
	}
	global, _, e = Load(path)
	if e != nil || global.Context.Window != 65536 {
		t.Fatal("explicit save changed global preferences", e)
	}
	explicit, _, e = LoadProject(path)
	if e != nil || explicit.Context.Window != 32768 {
		t.Fatal("explicit project save lost value", e)
	}
}
