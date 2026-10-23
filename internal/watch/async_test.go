package watch

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestAsyncStartupDoesNotBlockInterfaceOrClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	w, err := newWatcher(newRoot(t), testCfg(), true, func(root string, visit fs.WalkDirFunc) error {
		close(entered)
		<-release // Simulate slow directory enumeration, not CPU timing.
		return filepath.WalkDir(root, visit)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer close(release)
	<-entered
	if !w.Stats().Initializing {
		t.Fatal("incomplete coverage was not disclosed")
	}
	closed := make(chan error, 1)
	go func() { closed <- w.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing waits on blocked filesystem discovery")
	}
}

func TestAsyncStartupCapturesBaselineAndThenChanges(t *testing.T) {
	root := newRoot(t)
	write(t, root, "src/a.txt", "before\n")
	w, err := NewAsync(root, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	select {
	case <-w.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("scan did not finish")
	}
	if s := w.Stats(); s.Initializing || s.Files != 1 || s.Limited {
		t.Fatalf("coverage=%+v", s)
	}
	write(t, root, "src/a.txt", "after\n")
	ev := next(t, w)
	if !ev.BeforeKnown || len(ev.Removed) != 1 || ev.Removed[0] != "before" {
		t.Fatalf("baseline lost: %+v", ev)
	}
}

func TestDiscoveryBudgetIncludesDirectoriesAndStopsTraversal(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a/one.txt", "one")
	write(t, root, "b/two.txt", "two")
	cfg := testCfg()
	cfg.StartupMaxEntries = 2
	w := start(t, root, cfg)
	if s := w.Stats(); s.Entries != 2 || !s.Limited || s.Files != 1 {
		t.Fatalf("unbounded or undisclosed scan: %+v", s)
	}
	for _, path := range w.fw.WatchList() {
		if path == filepath.Join(root, "b") {
			t.Fatal("watch registered past traversal limit")
		}
	}
}

func TestAsyncStartupFailureIsDisclosed(t *testing.T) {
	w, err := newWatcher(newRoot(t), testCfg(), true, func(string, fs.WalkDirFunc) error { return errors.New("discovery failed") })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	deadline := time.After(time.Second)
	for w.Stats().Errors == 0 {
		select {
		case <-deadline:
			t.Fatal("discovery failure hidden")
		case <-time.After(time.Millisecond):
		}
	}
	if s := w.Stats(); s.Error == "" || s.Initializing {
		t.Fatalf("coverage=%+v", s)
	}
}
