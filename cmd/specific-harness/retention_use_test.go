package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"strings"
	"testing"
	"time"
)

func TestR25CLISessionUse(t *testing.T) {
	for _, cmd := range []string{"sessions", "judgments"} {
		t.Run(cmd, func(t *testing.T) {
			dir := t.TempDir()
			old := time.Now().AddDate(0, -4, 0)
			for _, id := range []string{"used", "unused"} {
				if err := session.Append(dir, id, session.Event{Kind: "prompt", Text: "exact α"}); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(filepath.Join(dir, id+".jsonl"), old, old); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dir, "used.jsonl")
			before, _ := os.ReadFile(p)
			cfg := filepath.Join(t.TempDir(), "cfg.yaml")
			if err := os.WriteFile(cfg, []byte(fmt.Sprintf("sessions:\n  path: %q\n", dir)), 0600); err != nil {
				t.Fatal(err)
			}
			if out, code := cliProcess(t, "sessions", "--config", cfg); code != 0 {
				t.Fatalf("listing%d %s", code, out)
			}
			st, _ := os.Stat(p)
			if !st.ModTime().Before(time.Now().AddDate(0, -3, 0)) {
				t.Fatal("CLI listing refreshed last use")
			}
			if out, code := cliProcess(t, cmd, "used", "--config", cfg); code != 0 {
				t.Fatalf("explicit%d %s", code, out)
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("use changed raw bytes")
			}
			deleted, err := session.Sweep(dir, 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(deleted) != 1 || deleted[0] != "unused" {
				t.Errorf("explicit read did not retain used session: %v", deleted)
			}
		})
	}
}

func TestR25CLIUseErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	old := time.Now().AddDate(0, -4, 0)
	if err := os.WriteFile(path, []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("sessions:\n  path: %q\n", dir)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"sessions", "bad"}, {"judgments", "bad"}, {"tui", "--resume", "bad"}, {"tui", "--resume", "missing"}} {
		out, code := cliProcess(t, append(argv, "--config", cfg)...)
		if code != 1 || !strings.Contains(out, "error:") {
			t.Fatalf("%v exit %d: %s", argv, code, out)
		}
		st, err := os.Stat(path)
		if err != nil || !st.ModTime().Before(time.Now().AddDate(0, -3, 0)) {
			t.Fatalf("failed access touched or swept archive: %v", err)
		}
	}
}

func TestR25SharedStartupSweep(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sessions.Path = t.TempDir()
	if err := session.Append(cfg.Sessions.Path, "old", session.Event{Kind: "prompt", Text: "keep"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(cfg.Sessions.Path, "old.jsonl")
	old := time.Now().AddDate(0, -4, 0)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	cfg.Retention.SessionMonths = 0
	sweepSessions(cfg)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("disabled startup sweep deleted archive: %v", err)
	}
	cfg.Retention.SessionMonths = 3
	sweepSessions(cfg)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("startup sweep did not delete untouched archive: %v", err)
	}
}
