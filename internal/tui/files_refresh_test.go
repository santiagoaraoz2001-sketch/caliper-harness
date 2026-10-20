package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func filesFixture(t *testing.T) (Model, string) {
	t.Helper()
	dir := fileTree(t)
	t.Chdir(dir)
	cfg := testCfg()
	cfg.Store.Path = filepath.Join(dir, "owned-store")
	m := loadFiles(submit(New(cfg, "test"), "/files"))
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("Enter did not start selected read")
	}
	um, _ = m.Update(cmd())
	return um.(Model), dir
}

func filesPoll(t *testing.T, m Model) Model {
	t.Helper()
	um, cmd := m.Update(statusTickMsg{})
	m = um.(Model)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) < 3 {
		t.Fatalf("selected viewer has no asynchronous refresh: %T", cmd())
	}
	// Execute only the Files command, never status/store/timer commands.
	um, _ = m.Update(batch[len(batch)-1]())
	return um.(Model)
}

func TestFilesRefreshDiskQuoteAndScroll(t *testing.T) {
	m, dir := filesFixture(t)
	tail := "\n" + strings.Repeat("tail\n", 40)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree"+tail), 0600); err != nil {
		t.Fatal(err)
	}
	m = filesPoll(t, m)
	m.Files.Viewer.Scroll = 1
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("after\nnew quote\nthree"+tail), 0600); err != nil {
		t.Fatal(err)
	}
	m = filesPoll(t, m)
	if m.Files.Viewer == nil || m.Files.Viewer.Logical[0] != "after" || m.Files.Viewer.Scroll != 1 {
		t.Fatalf("viewer=%+v", m.Files.Viewer)
	}
	m = submit(m, "/comment [2] observed")
	if len(m.Comment) != 1 || m.Comment[0].Quote != "new quote" {
		t.Fatalf("quote=%+v", m.Comment)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	m = filesPoll(t, m)
	if m.Files.Viewer.Scroll != 0 {
		t.Fatalf("short scroll=%d", m.Files.Viewer.Scroll)
	}
}

func TestFilesRefreshFailureAndRecovery(t *testing.T) {
	for _, kind := range []string{"deleted", "binary", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			m, dir := filesFixture(t)
			p := filepath.Join(dir, "a.txt")
			switch kind {
			case "deleted":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			case "binary":
				if err := os.WriteFile(p, []byte{0, 1}, 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Chmod(p, 0); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(p, 0600)
				if _, err := os.ReadFile(p); err == nil {
					t.Skip("file permissions do not deny reads for this process")
				}
			}
			m = filesPoll(t, m)
			if m.Files.Viewer != nil || m.Files.Err == "" {
				t.Fatalf("old content remains: viewer=%+v err=%q", m.Files.Viewer, m.Files.Err)
			}
			closed := key(m, "esc")
			if closed.Files.Viewer != nil || closed.Files.Err != "" {
				t.Fatal("Esc did not immediately restore list after read error")
			}
			before := m.Files.Err
			m.onFilesLoaded(filesLoadedMsg{files: []FileEntry{{Rel: "a.txt"}}})
			if m.Files.Err != before {
				t.Fatal("successful list erased selected read error")
			}
			m = submit(m, "/comment [1] stale")
			if len(m.Comment) != 0 {
				t.Fatal("stale content quoted")
			}
			if err := os.Chmod(p, 0600); err != nil && kind != "deleted" {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("recovered"), 0600); err != nil {
				t.Fatal(err)
			}
			m = filesPoll(t, m)
			if m.Files.Viewer == nil || m.Files.Viewer.Logical[0] != "recovered" || m.Files.Err != "" {
				t.Fatalf("recovery viewer=%+v err=%q", m.Files.Viewer, m.Files.Err)
			}
		})
	}
}

func TestFilesRefreshRejectsLateReads(t *testing.T) {
	m, dir := filesFixture(t)
	old := m.openFile("a.txt")()
	later := m.openFile("sub/b.txt")
	um, _ := m.Update(later())
	m = um.(Model)
	um, _ = m.Update(old)
	m = um.(Model)
	if m.Files.Viewer.Title != "sub/b.txt" {
		t.Fatalf("late switch=%+v", m.Files.Viewer)
	}
	// The same path reopened still has a distinct lifetime/read request.
	pending := m.openFile("a.txt")()
	m = key(m, "esc")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("reopened"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := m.openFile("a.txt")
	um, _ = m.Update(fresh())
	m = um.(Model)
	um, _ = m.Update(pending)
	m = um.(Model)
	if m.Files.Viewer == nil || m.Files.Viewer.Logical[0] != "reopened" {
		t.Fatal("reopen lost current read")
	}
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	failed := m.openFile("a.txt")()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("newest"), 0600); err != nil {
		t.Fatal(err)
	}
	good := m.openFile("a.txt")
	um, _ = m.Update(good())
	m = um.(Model)
	um, _ = m.Update(failed)
	m = um.(Model)
	if m.Files.Viewer == nil || m.Files.Viewer.Logical[0] != "newest" || m.Files.Err != "" {
		t.Fatalf("stale same-file error landed: %+v %q", m.Files.Viewer, m.Files.Err)
	}
	pending = m.openFile("a.txt")()
	m = key(m, "esc")
	um, _ = m.Update(pending)
	m = um.(Model)
	if m.Files.Viewer != nil {
		t.Fatal("closed viewer resurrected")
	}
}

func TestFilesRefreshConfiguredBoundedRead(t *testing.T) {
	m, dir := filesFixture(t)
	m.Cfg.Tools.Bash.MaxBytes = 8
	// Eight bytes end mid-UTF8 rune; omitted suffix is not counted as lines.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("prefix é"+strings.Repeat("x", 1<<20)), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := m.openFile("a.txt")
	um, _ := m.Update(cmd())
	m = um.(Model)
	if m.Files.Viewer == nil {
		t.Fatalf("read failed: %q", m.Files.Err)
	}
	text := strings.Join(m.Files.Viewer.Logical, "\n")
	if !utf8.ValidString(text) || !strings.Contains(text, "bytes omitted") || strings.Contains(text, "more lines capped") || len(text) > 200 {
		t.Fatalf("unbounded/false result bytes=%d prefix=%q", len(text), text[:min(120, len(text))])
	}
}

func TestFilesRefreshCapturedRoot(t *testing.T) {
	m, _ := filesFixture(t)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "a.txt"), []byte("other root"), 0600); err != nil {
		t.Fatal(err)
	}
	pending := m.openFile("a.txt")
	t.Chdir(other)
	um, _ := m.Update(pending())
	m = um.(Model)
	if m.Files.Viewer == nil || m.Files.Viewer.Logical[0] != "one" {
		t.Fatalf("read used changing cwd: %+v", m.Files.Viewer)
	}
	// Poll retains the opened root; a newly requested same path owns the new root.
	m = filesPoll(t, m)
	if m.Files.Viewer.Logical[0] != "one" {
		t.Fatal("poll switched root")
	}
	old := m.openFile("a.txt")()
	t.Chdir(t.TempDir())
	failed := m.openFile("a.txt")
	um, _ = m.Update(failed())
	m = um.(Model)
	um, _ = m.Update(old)
	m = um.(Model)
	if m.Files.Viewer != nil || m.Files.Err == "" {
		t.Fatal("different-root stale result replaced current failure")
	}
}

func TestFilesRefreshReadErrorVisibleAtTerminalWidth(t *testing.T) {
	m, dir := filesFixture(t)
	rel := strings.Repeat("long-owned-directory-", 9) + "/missing.txt"
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := m.openFile(rel)
	um, _ := m.Update(cmd())
	m = um.(Model)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	m = filesPoll(t, m)
	if m.Files.Viewer != nil || m.Files.Err == "" {
		t.Fatal("missing file did not clear viewer")
	}
	_, readErr := os.ReadFile(p)
	pathErr, ok := readErr.(*os.PathError)
	if !ok || !os.IsNotExist(pathErr) {
		t.Fatalf("missing-file fixture: %v", readErr)
	}
	for _, width := range []int{120, 80} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m.Width, m.Height = width, 32
			var visible []string
			// Read the public Model view through an ordinary terminal's available cells.
			for _, row := range strings.Split(m.View(), "\n") {
				visible = append(visible, ansi.Truncate(ansi.Strip(row), width, ""))
			}
			screen := strings.Join(visible, "\n")
			for _, want := range append(strings.Fields(pathErr.Err.Error()), "retrying", "esc closes detail") {
				if !strings.Contains(screen, want) {
					t.Fatalf("width%d hidden %q:\n%s", width, want, screen)
				}
			}
		})
	}
}
