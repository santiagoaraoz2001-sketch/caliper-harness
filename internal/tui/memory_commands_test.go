package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/memory"
	"specific-harness/internal/store"
)

func TestRecallUsesCurrentProjectAndLegacyGlobalNotes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	db := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(project, key, body, previous string) *memory.Entry {
		t.Helper()
		e, err := memory.PutKnowledge(ctx, s, project, "source-session", key, body, previous, "", "")
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		return e
	}
	old := put(root, "palette", "quartz retired violet", "")
	put(root, "palette", "quartz current emerald", old.ID)
	put(t.TempDir(), "palette", "quartz unrelated scarlet", "")
	put(root, "harness/reference/example", "quartz reference example", "")
	if _, err := memory.Add(s, "note", "quartz shared global", "older-session"); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	m := New(testCfg(), "test")
	m.StorePath = db
	m.Cfg.Recall.TopN = 20
	m.Cfg.Recall.MaxChars = 2000
	m = submit(m, "/recall quartz")
	var result strings.Builder
	for _, line := range m.Chat.Lines {
		if line.Kind == lineSys {
			result.WriteString(line.Text)
		}
	}
	for _, expected := range []string{"quartz current emerald", "quartz shared global"} {
		if !strings.Contains(result.String(), expected) {
			t.Errorf("missing %q in %q", expected, result.String())
		}
	}
	for _, excluded := range []string{"retired violet", "unrelated scarlet", "reference example"} {
		if strings.Contains(result.String(), excluded) {
			t.Errorf("leaked %q in %q", excluded, result.String())
		}
	}
}

func TestRememberProjectDefaultAndExplicitGlobal(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	m := New(testCfg(), "test")
	m.StorePath = filepath.Join(t.TempDir(), "db")
	m.Chat.SessionIDs = []string{"actual-session"}
	m = submit(m, "/remember quartz project fact.  Preserve spacing.")
	m = submit(m, "/remember --global quartz shared fact")
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := memory.Knowledge(context.Background(), s, root, "", "", 0, 0, true)
	if err != nil || len(project) != 1 || project[0].Body != "quartz project fact.  Preserve spacing." || project[0].SessionRef != "actual-session" || !strings.HasPrefix(project[0].Key, "user/note/") {
		t.Fatalf("project notes %+v, %v", project, err)
	}
	global, err := memory.List(s, "note", "quartz", 0)
	if err != nil || len(global) != 1 || global[0].Body != "quartz shared fact" {
		t.Fatalf("global notes %+v, %v", global, err)
	}
	s.Close()
	// A new UI instance and a reopened store use the same notebook.
	fresh := New(testCfg(), "test")
	fresh.StorePath = m.StorePath
	fresh = submit(fresh, "/recall quartz")
	var text string
	for _, line := range fresh.Chat.Lines {
		if line.Kind == lineSys {
			text += line.Text
		}
	}
	if !strings.Contains(text, "project fact") || !strings.Contains(text, "shared fact") {
		t.Fatalf("fresh recall %q", text)
	}
	t.Chdir(t.TempDir())
	foreign := New(testCfg(), "test")
	foreign.StorePath = m.StorePath
	foreign = submit(foreign, "/recall quartz")
	for _, line := range foreign.Chat.Lines {
		if strings.Contains(line.Text, "project fact") {
			t.Fatal("project note escaped into another project")
		}
	}
}

func TestStyleCommandsPreserveVersionsAndProjectScope(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	m := New(testCfg(), "test")
	m.StorePath = filepath.Join(t.TempDir(), "db")
	m.Chat.SessionIDs = []string{"author-session"}
	m = submit(m, "/style add Prefer early returns.")
	m = submit(m, "/style add Keep pure functions for state updates.")
	m = submit(m, "/style")
	read := func() []*memory.Entry {
		t.Helper()
		s, err := store.Open(m.StorePath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var all []*memory.Entry
		for id := ""; ; {
			key := ""
			if id == "" {
				key = memory.StyleguideKey
			}
			version, err := memory.Knowledge(context.Background(), s, root, key, id, 1, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(version) == 0 {
				break
			}
			all = append(all, version[0])
			id = version[0].PreviousID
			if id == "" {
				break
			}
		}
		return all
	}
	versions := read()
	if len(versions) != 2 || versions[0].Body != "Prefer early returns.\nKeep pure functions for state updates." || versions[0].PreviousID != versions[1].ID {
		t.Fatalf("append versions %+v", versions)
	}
	m = submit(m, "/style set Use explicit names.")
	m = submit(m, "/style clear")
	versions = read()
	if len(versions) != 4 || versions[0].Body != "" || versions[1].Body != "Use explicit names." || versions[0].SessionRef != "author-session" {
		t.Fatalf("set/clear lost historical versions: %+v", versions)
	}
	m = submit(m, "/style add Project-specific convention.")
	t.Chdir(t.TempDir())
	other := New(testCfg(), "test")
	other.StorePath = m.StorePath
	other = submit(other, "/style")
	if len(other.Alerts) != 1 || !strings.Contains(other.Alerts[0].Text, "no project style") {
		t.Fatalf("foreign project style: alerts=%+v history=%+v input=%q lines=%+v", other.Alerts, other.Hist, other.Input.Value(), other.Chat.Lines)
	}
	// Malformed UI commands must not create another revision.
	t.Chdir(root)
	before := len(read())
	for _, command := range []string{"/style set", "/style add", "/style clear unexpected", "/style invalid", "/remember --global"} {
		m = submit(m, command)
	}
	if len(read()) != before {
		t.Fatal("invalid command changed style")
	}
}
