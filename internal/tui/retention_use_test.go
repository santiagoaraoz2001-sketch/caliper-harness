package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"specific-harness/internal/session"
	"testing"
	"time"
)

func TestR25TUIExplicitUse(t *testing.T) {
	for _, route := range []string{"open", "resume", "legacy-resume"} {
		t.Run(route, func(t *testing.T) {
			dir := t.TempDir()
			old := time.Now().AddDate(0, -4, 0)
			for _, id := range []string{"used", "unused"} {
				if err := session.Append(dir, id, session.Event{Kind: "prompt", Text: "retained α"}); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(filepath.Join(dir, id+".jsonl"), old, old); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dir, "used.jsonl")
			before, _ := os.ReadFile(p)
			cfg := testCfg()
			cfg.Sessions.Path = dir
			m := New(cfg, "test")
			m.SessionsPath = dir
			loaded := m.refreshSessions()().(sessionsLoadedMsg)
			if loaded.err != nil {
				t.Fatal(loaded.err)
			}
			st, _ := os.Stat(p)
			if !st.ModTime().Before(time.Now().AddDate(0, -3, 0)) {
				t.Fatal("listing refreshed last use")
			}
			switch route {
			case "open":
				msg := m.openSession("used")().(sessionsOpenedMsg)
				if msg.err != nil {
					t.Fatal(msg.err)
				}
				updated, _ := m.Update(msg)
				if updated.(Model).Sessions.Detail.ID != "used" {
					t.Fatal("wrong opened session")
				}
			case "resume":
				c, _, err := BaselineContinuation(dir, "used", 32768, "fixture")
				if err != nil || len(c.Entries) == 0 {
					t.Fatalf("resume %v %+v", err, c)
				}
			case "legacy-resume":
				c, err := BaselineHistory(dir, "used")
				if err != nil || len(c) == 0 {
					t.Fatalf("resume %v", err)
				}
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("use changed raw event bytes")
			}
			deleted, err := session.Sweep(dir, 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(deleted) != 1 || deleted[0] != "unused" {
				t.Errorf("used session expired: deleted=%v", deleted)
			}
		})
	}
}

func TestR25TUIUseErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.jsonl")
	old := time.Now().AddDate(0, -4, 0)
	if err := os.WriteFile(p, []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg()
	cfg.Sessions.Path = dir
	m := New(cfg, "test")
	m.SessionsPath = dir
	for _, id := range []string{"bad", "missing"} {
		if msg := m.openSession(id)().(sessionsOpenedMsg); msg.err == nil {
			t.Fatal("open silently succeeded")
		}
		if _, _, err := BaselineContinuation(dir, id, 32768, "fixture"); err == nil {
			t.Fatal("resume silently succeeded")
		}
	}
	st, _ := os.Stat(p)
	if !st.ModTime().Before(time.Now().AddDate(0, -3, 0)) {
		t.Fatal("corrupt read refreshed last use")
	}
}
