package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/watch"
)

func TestStartupDiffActualCoverageAndQuote(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a-empty", "b-unreadable", "c-omitted"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bad := filepath.Join(root, "b-unreadable")
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0600) })
	if _, err := os.ReadFile(bad); err == nil {
		t.Skip("permissions do not deny this process")
	}
	cfg := watch.DefaultConfig()
	cfg.StartupMaxFiles = 2
	cfg.Debounce = 10 * time.Millisecond
	w, err := watch.New(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.Chmod(bad, 0600); err != nil {
		t.Fatal(err)
	}
	// Saturate the actual event channel through real fsnotify events.
	for i := 0; i < 270; i++ {
		prior := len(w.Events()) + w.Stats().Dropped
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("new-%03d", i)), []byte("quoted_source\n"), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(time.Second)
		for len(w.Events())+w.Stats().Dropped == prior {
			select {
			case <-deadline:
				t.Fatalf("individual fsnotify event %d not delivered; coverage %+v, queued %d", i, w.Stats(), len(w.Events()))
			case <-time.After(time.Millisecond):
			}
		}
	}
	deadline := time.After(3 * time.Second)
	for w.Stats().Dropped == 0 {
		select {
		case <-deadline:
			t.Fatal("actual channel did not fill")
		case <-time.After(time.Millisecond):
		}
	}
	var ev watch.Event
	for ev = range w.Events() {
		if strings.HasPrefix(ev.Path, "new-") {
			break
		}
	}
	m := toDiff(diffModel())
	m.Diff.Watcher = w
	defer func() { m.Diff.Watcher = nil }()
	updated, _ := m.Update(diffEventMsg{ev: ev})
	m = updated.(Model)
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(Model)
		for _, open := range []bool{false, true} {
			m.Diff.Viewer = nil
			if open {
				m = enter(m)
			}
			v := m.View()
			for _, want := range []string{"Startup", "omitted", "skipped", "dropped", "error:", "permission denied", "baseline unknown"} {
				if !strings.Contains(v, want) {
					t.Fatalf("%v open%v missing %s:\n%s", size, open, want, v)
				}
			}
			for _, row := range strings.Split(v, "\n") {
				if ansi.StringWidth(row) > m.Width {
					t.Fatalf("%v row overwidth %d: %q", size, ansi.StringWidth(row), row)
				}
			}
			if rows := len(strings.Split(v, "\n")); rows > m.Height {
				t.Fatalf("%v %d rows", size, rows)
			}
			if open {
				lines, _, avail := m.viewerLayout(ViewDiff)
				if avail != viewerAvail(ev, m.diffHeight(viewHeight(m), m.Width)) || len(lines.Rows) == 0 {
					t.Fatal("logical viewport differs from renderer")
				}
				m = submit(m, "/comment [1] inspect")
				if len(m.Comment) != 1 || m.Comment[0].Quote != "quoted_source" {
					t.Fatalf("metadata contaminated quote=%+v", m.Comment)
				}
				m.Comment = nil
				m.navigate(ViewDiff, "")
			}
		}
	}
}
