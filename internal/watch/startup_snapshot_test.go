package watch

import (
	"github.com/fsnotify/fsnotify"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStartupSnapshotsBoundedCoverage(t *testing.T) {
	t.Run("attempts exclude ignored but include empty and errors", func(t *testing.T) {
		root := newRoot(t)
		write(t, root, ".hidden", "hidden")
		write(t, root, "data/ignored", "ignored")
		write(t, root, "a-empty", "")
		write(t, root, "b-unreadable", "secret")
		write(t, root, "c-omitted", "later")
		p := filepath.Join(root, "b-unreadable")
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(p, 0600) })
		if _, err := os.ReadFile(p); err == nil {
			t.Skip("permissions do not deny this process")
		}
		c := testCfg()
		c.StartupMaxFiles = 2
		w := start(t, root, c)
		s := w.Stats()
		if s.Attempts != 2 || s.Files != 1 || s.Bytes != 0 || s.Omitted != 2 || s.Error == "" {
			t.Fatalf("coverage=%+v", s)
		}
		if _, ok := w.snaps["a-empty"]; !ok {
			t.Fatal("empty must be a known capture")
		}
		if len(w.snaps) != 1 {
			t.Fatalf("excluded/error/omitted identities retained: %v", w.snaps)
		}
	})
	t.Run("aggregate remainder and probe", func(t *testing.T) {
		root := newRoot(t)
		write(t, root, "a", "12345")
		write(t, root, "b", "67890")
		write(t, root, "c", "")
		c := testCfg()
		c.MaxBytes = 8
		c.StartupMaxBytes = 8
		w := start(t, root, c)
		s := w.Stats()
		if s.Bytes != 8 || s.Attempts != 2 || s.Files != 2 || s.Omitted != 1 || s.Partial != 1 {
			t.Fatalf("coverage=%+v", s)
		}
		if got := w.snaps["b"]; !got.partial || !reflect.DeepEqual(got.lines, []string{"678"}) {
			t.Fatalf("remaining budget=%+v", got)
		}
		got, n, err := w.read(filepath.Join(root, "a"), 5)
		if err != nil || n != 5 || got.partial {
			t.Fatalf("exact limit falsely partial: %+v %d %v", got, n, err)
		}
		got, n, err = w.read(filepath.Join(root, "a"), 4)
		if err != nil || n != 4 || !got.partial || strings.Join(got.lines, "") != "1234" {
			t.Fatalf("overflow probe retained: %+v %d %v", got, n, err)
		}
	})
}

func TestStartupUnknownPartialAndReadFailure(t *testing.T) {
	t.Run("unknown becomes captured", func(t *testing.T) {
		root := newRoot(t)
		write(t, root, "a", "")
		write(t, root, "b", "before\nkeep\n")
		c := testCfg()
		c.StartupMaxFiles = 1
		w := start(t, root, c)
		write(t, root, "b", "after\nkeep\n")
		ev := next(t, w)
		if ev.BeforeKnown || ev.AddedN != 0 || len(ev.Added) != 0 || !reflect.DeepEqual(ev.After, []string{"after", "keep"}) {
			t.Fatalf("unknown invented addition=%+v", ev)
		}
		write(t, root, "b", "new\nkeep\n")
		ev = next(t, w)
		if !ev.BeforeKnown || !reflect.DeepEqual(ev.Removed, []string{"after"}) || !reflect.DeepEqual(ev.Added, []string{"new"}) {
			t.Fatalf("captured later baseline=%+v", ev)
		}
	})
	t.Run("suffix outside prefix is not unchanged", func(t *testing.T) {
		root := newRoot(t)
		write(t, root, "a", "same-one")
		c := testCfg()
		c.MaxBytes = 4
		w := start(t, root, c)
		write(t, root, "a", "same-two")
		ev := next(t, w)
		if !ev.BeforeKnown || !ev.BeforePartial || !ev.Truncated {
			t.Fatalf("partial comparison hidden=%+v", ev)
		}
	})
	t.Run("failed read is not deletion and retains baseline", func(t *testing.T) {
		root := newRoot(t)
		write(t, root, "a", "before\n")
		w := start(t, root, testCfg())
		p := filepath.Join(root, "a")
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(p, 0600) })
		if _, err := os.ReadFile(p); err == nil {
			t.Skip("permissions do not deny this process")
		}
		ev := next(t, w)
		if ev.Op == OpDelete || ev.ReadError == "" || len(ev.Removed) > 0 {
			t.Fatalf("read error became deletion=%+v", ev)
		}
		if _, ok := w.snaps["a"]; !ok {
			t.Fatal("good baseline lost")
		}
		os.Chmod(p, 0600)
		write(t, root, "a", "after\n")
		ev = next(t, w)
		if !ev.BeforeKnown || !reflect.DeepEqual(ev.Removed, []string{"before"}) {
			t.Fatalf("recovered baseline=%+v", ev)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		ev = next(t, w)
		if ev.Op != OpDelete || !reflect.DeepEqual(ev.Removed, []string{"after"}) {
			t.Fatalf("confirmed deletion=%+v", ev)
		}
	})
}

func TestStartupDroppedAndFactoryRefusal(t *testing.T) {
	root := newRoot(t)
	w := start(t, root, testCfg())
	for i := 0; i < cap(w.out)+2; i++ {
		w.emit(Event{Op: OpModify}, false, "")
	}
	if w.Stats().Dropped != 2 {
		t.Fatalf("drops=%+v", w.Stats())
	}
	c := testCfg()
	c.StartupMaxFiles = 0
	if bad, err := New(root, c); err == nil {
		bad.Close()
		t.Fatal("invalid cap admitted")
	}
}

func TestStartupBinaryAndLineCaps(t *testing.T) {
	root := newRoot(t)
	write(t, root, "binary", "a\x00b")
	write(t, root, "lines", "one\ntwo\nthree\n")
	c := testCfg()
	c.FileLines = 2
	w := start(t, root, c)
	if s := w.Stats(); s.Files != 2 || s.Attempts != 2 || s.Partial != 2 {
		t.Fatalf("coverage=%+v", s)
	}
	if got := w.snaps["binary"]; !got.partial || !reflect.DeepEqual(got.lines, []string{"<binary file>"}) {
		t.Fatalf("binary=%+v", got)
	}
	if got := w.snaps["lines"]; !got.partial || !reflect.DeepEqual(got.lines, []string{"one", "two"}) {
		t.Fatalf("lines=%+v", got)
	}
}

func TestStartupRegularAdmissionAndKernelError(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a", "before\n")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	w := start(t, root, testCfg())
	if s := w.Stats(); s.Attempts != 1 || s.Files != 1 {
		t.Fatalf("symlink consumed admission=%+v", s)
	}
	if _, n, err := w.read(filepath.Join(root, "link"), 16); err == nil || n != 0 {
		t.Fatalf("symlink read=%d %v", n, err)
	}
	if _, n, err := w.read(root, 16); err == nil || n != 0 {
		t.Fatalf("directory read=%d %v", n, err)
	}
	// Component delivery on the actual fsnotify error channel, not a claimed kernel overflow.
	w.fw.Errors <- fsnotify.ErrEventOverflow
	deadline := time.After(time.Second)
	for w.Stats().Error == "" {
		select {
		case <-deadline:
			t.Fatal("error not recorded")
		case <-time.After(time.Millisecond):
		}
	}
	write(t, root, "a", "after\n")
	ev := next(t, w)
	if ev.BeforeKnown || ev.AddedN != 0 {
		t.Fatalf("lost coverage invented before=%+v", ev)
	}
	write(t, root, "a", "later\n")
	ev = next(t, w)
	if !ev.BeforeKnown || !reflect.DeepEqual(ev.Removed, []string{"after"}) || w.Stats().Error == "" || w.Stats().Errors != 1 {
		t.Fatalf("recapture/error provenance=%+v %+v", ev, w.Stats())
	}
}
