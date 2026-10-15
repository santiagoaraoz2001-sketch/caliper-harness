package search

import (
	"context"
	"os"
	"path/filepath"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestBoundDiscoveryWorkspaceScope(t *testing.T) {
	requireRG(t)
	root, outside := fixture(t), fixture(t)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []tool.Definition{NewGrepAt(testCfg(), root), NewFindFileAt(testCfg(), root)} {
		for _, p := range []string{outside, rel, "escape"} {
			t.Run(d.Name+"/reject/"+p, func(t *testing.T) {
				pattern := "NEEDLE"
				if d.Name == "find_file" {
					pattern = "*.txt"
				}
				args, repair := tool.Validate(d, map[string]string{"pattern": pattern, "path": p})
				if repair != "" {
					t.Fatal(repair)
				}
				result, err := d.Execute(context.Background(), args)
				if err == nil || !strings.Contains(err.Error(), "workspace") {
					t.Errorf("outside must refuse before search: result=%+v err=%v", result, err)
				}
			})
		}
		for _, p := range []string{"", root} {
			t.Run(d.Name+"/inside/"+p, func(t *testing.T) {
				pattern := "NEEDLE"
				if d.Name == "find_file" {
					pattern = "*.txt"
				}
				slots := map[string]string{"pattern": pattern}
				if p != "" {
					slots["path"] = p
				}
				args, repair := tool.Validate(d, slots)
				if repair != "" {
					t.Fatal(repair)
				}
				result, err := d.Execute(context.Background(), args)
				if err != nil || !strings.Contains(result.Text, "visible.txt") || strings.Contains(result.Text, "ignored.txt") {
					t.Fatalf("inside result=%+v err=%v", result, err)
				}
			})
		}
	}
	// These APIs intentionally retain their unbound cross-project semantics.
	for _, d := range []tool.Definition{NewGrep(testCfg()), NewFind(testCfg())} {
		pattern := "NEEDLE"
		if d.Name == "find" {
			pattern = "*.txt"
		}
		args, _ := tool.Validate(d, map[string]string{"pattern": pattern, "path": outside})
		result, err := d.Execute(context.Background(), args)
		if err != nil || !strings.Contains(result.Text, "visible.txt") {
			t.Fatalf("standalone %s changed: %+v %v", d.Name, result, err)
		}
	}
}

func TestBoundDiscoveryRefusesBeforeRGAndCancels(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	t.Setenv("PATH", t.TempDir()) // Missing executable must not mask path admission.
	for _, d := range []tool.Definition{NewGrepAt(testCfg(), root), NewFindAt(testCfg(), root), NewFindFileAt(testCfg(), root)} {
		args, _ := tool.Validate(d, map[string]string{"path": outside, "pattern": "*"})
		_, err := d.Execute(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "workspace") {
			t.Fatalf("%s invoked executable lookup before scope refusal: %v", d.Name, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = d.Execute(ctx, args)
		if err != context.Canceled {
			t.Fatalf("%s cancellation: %v", d.Name, err)
		}
	}
}

func TestBoundDiscoveryIgnoresFollowConfig(t *testing.T) {
	requireRG(t)
	root, outside := fixture(t), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside-only.txt"), []byte("OUTSIDE_ONLY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "rg.conf")
	if err := os.WriteFile(configPath, []byte("--follow\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", configPath)
	for _, d := range []tool.Definition{NewGrepAt(testCfg(), root), NewFindAt(testCfg(), root)} {
		pattern := "OUTSIDE_ONLY"
		if d.Name == "find" {
			pattern = "outside-only.txt"
		}
		args, _ := tool.Validate(d, map[string]string{"pattern": pattern})
		res, err := d.Execute(context.Background(), args)
		if err != nil || !res.NoMatches {
			t.Fatalf("%s followed config symlink: %+v %v", d.Name, res, err)
		}
	}
}
