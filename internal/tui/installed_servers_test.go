package tui

import (
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
	"strings"
	"testing"
	"time"
)

func TestInstalledServersAndLegacyDirectoriesAreNeverEnumerated(t *testing.T) {
	isolateModelRoots(t)
	bin, models := t.TempDir(), t.TempDir()
	for _, name := range []string{"gufo", "llama-server"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0700)
	}
	t.Setenv("PATH", bin)
	os.WriteFile(filepath.Join(models, "unadded.gguf"), []byte("GGUFmetadata-only"), 0600)
	m := settingsModel(t)
	m.Cfg.Model.Name = ""
	m.Cfg.Serve.ModelDirs = []string{models}
	got := m.probeServeWith(probeClient())().(serveProbedMsg)
	if len(got.rows) != 0 || len(got.installed) != 0 {
		t.Fatalf("ambient entries: %+v", got)
	}
}

func TestAlternativeRecipeDoesNotReplaceLiveOwner(t *testing.T) {
	m := settingsModel(t)
	p, err := serve.Start(serve.Recipe{Name: "owned-fixture", Runtime: "openai", Argv: []string{"sleep", "30"}}, filepath.Join(t.TempDir(), "owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	url := "http://127.0.0.1:18890/v1"
	m.Serve.Spawned = map[string]*serve.Proc{url: p}
	marker := filepath.Join(t.TempDir(), "unexpected-launch")
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{URL: url, Label: "alternative"}, Have: true, Ours: true, Recipe: &serve.Recipe{Name: "alternative", Runtime: "openai", URL: url, Argv: []string{"/bin/sh", "-c", `printf started > "$1"; exec sleep 30`, "fixture", marker}}}}
	m.serveStart()
	defer m.Serve.Spawned[url].Stop() // Reap even the deliberately broken overlay control.
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("alternate launch ran")
	}
	if m.Serve.Spawned[url] != p || p.PID() == 0 {
		t.Fatal("live owner replaced")
	}
	m.Serve.Rows[0].Health.Up = true
	m.Serve.Rows[0].Recipe.Runtime = "mlx" // Alternative recipe is not the launched process.
	m.serveUse()
	if m.Cfg.Model.Runtime != "openai" {
		t.Fatal("alternative recipe overrode actual launched runtime")
	}
}

func TestDiscoveredLauncherRequiresLocalCandidateAndLoadsLiteralPath(t *testing.T) {
	isolateModelRoots(t)
	bin, models := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gufo"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CALIPER_TEST_ARGV.tmp\"\n/bin/mv \"$CALIPER_TEST_ARGV.tmp\" \"$CALIPER_TEST_ARGV\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	output := filepath.Join(t.TempDir(), "args")
	t.Setenv("CALIPER_TEST_ARGV", output)
	model := filepath.Join(models, "local model.gguf")
	if err := os.WriteFile(model, []byte("GGUFmetadata-only"), 0600); err != nil {
		t.Fatal(err)
	}
	m := settingsModel(t)
	m.Cfg.Model.Server = "http://127.0.0.1:18890/v1"
	m.Cfg.Model.Name = "remote/model"
	m.Cfg.Serve.Models = []config.SavedModel{{Server: m.Cfg.Model.Server, Name: model, Path: model}}
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "my-gufo", Kind: "openai", Runtime: "openai", URL: m.Cfg.Model.Server, Argv: []string{filepath.Join(bin, "gufo"), "serve", "--model", "{model}"}}}
	got := m.probeServeWith(probeClient())().(serveProbedMsg)
	var candidate ServeRow
	for _, r := range got.rows {
		if r.Recipe == nil || r.Recipe.Name != "my-gufo" {
			continue
		}
		r.Health = serve.Health{}
		if r.Asset != nil {
			candidate = r
			continue
		}

	}
	if candidate.Asset == nil {
		t.Fatal("local candidate missing")
	}
	m.Serve.Rows = []ServeRow{candidate}
	m.Serve.Sel = 0
	cmd := m.serveLoad()
	if cmd == nil {
		t.Fatal("local candidate load missing")
	}
	msg := cmd().(serveLoadMsg)
	if msg.err != nil || msg.proc == nil {
		t.Fatalf("local launch: %v", msg.err)
	}
	defer msg.proc.Stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(output); err == nil && len(b) > 0 {
			if !strings.HasSuffix(string(b), candidate.Asset.Path+"\n") || strings.Contains(string(b), "remote/model") {
				t.Fatalf("wrong argv %q", b)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("local fixture did not capture args")
}
