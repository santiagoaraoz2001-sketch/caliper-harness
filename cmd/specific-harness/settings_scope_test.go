package main

import (
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"testing"
)

func TestCLIExplicitManagedConfigPreservesScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Chdir(t.TempDir())
	path := config.DefaultFile()
	c, _, e := config.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	c.Context.Window = 65536
	if e = config.SaveSettings(path, c); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("context:\n  window: 8192\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, cmd := range []string{"tui", "config"} {
		for _, explicit := range []bool{false, true} {
			argv := []string{cmd}
			want := 65536
			if explicit {
				argv = append(argv, "--config", path)
				want = 8192
			}
			_, args, e := parseCLI(argv)
			if e != nil {
				t.Fatal(e)
			}
			got, _, e := args.loadConfig()
			if e != nil || got.Context.Window != want || got.ProjectOverride != explicit {
				t.Fatalf("%v window%d scope%v err%v", argv, got.Context.Window, got.ProjectOverride, e)
			}
		}
	}
}
