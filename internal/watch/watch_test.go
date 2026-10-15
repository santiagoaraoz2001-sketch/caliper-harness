package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func testCfg() Config {
	c := DefaultConfig()
	c.Debounce = 30 * time.Millisecond
	return c
}

func newRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "watch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return root
}

func start(t *testing.T, root string, cfg Config) *Watcher {
	t.Helper()
	w, err := New(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func next(t *testing.T, w *Watcher) Event {
	t.Helper()
	select {
	case ev := <-w.Events():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
		return Event{}
	}
}

func none(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case ev := <-w.Events():
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestModifyCoalesces(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.txt", "one\n")
	w := start(t, root, testCfg())
	write(t, root, "a.txt", "one\ntwo\n")
	write(t, root, "a.txt", "one\ntwo\nthree\n")
	ev := next(t, w)
	if ev.Op != OpModify || ev.Path != "a.txt" {
		t.Fatalf("event=%+v", ev)
	}
	// Startup captured one; only subsequently added lines are additions.
	if !reflect.DeepEqual(ev.Added, []string{"two", "three"}) || len(ev.Removed) != 0 {
		t.Errorf("diff=%+v", ev)
	}
	none(t, w)
}

func TestProgressiveDiff(t *testing.T) {
	root := newRoot(t)
	write(t, root, "b.txt", "one\n")
	w := start(t, root, testCfg())
	write(t, root, "b.txt", "one\ntwo\n")
	if ev := next(t, w); !reflect.DeepEqual(ev.Added, []string{"two"}) {
		t.Fatalf("baseline=%+v", ev)
	}
	write(t, root, "b.txt", "one\nTWO\n")
	ev := next(t, w)
	if !reflect.DeepEqual(ev.Added, []string{"TWO"}) || !reflect.DeepEqual(ev.Removed, []string{"two"}) {
		t.Errorf("progressive=%+v", ev)
	}
}

func TestCreateDelete(t *testing.T) {
	root := newRoot(t)
	w := start(t, root, testCfg())
	write(t, root, "new.txt", "hi\n")
	if ev := next(t, w); ev.Op != OpCreate || !reflect.DeepEqual(ev.Added, []string{"hi"}) {
		t.Errorf("create=%+v", ev)
	}
	os.Remove(filepath.Join(root, "new.txt"))
	if ev := next(t, w); ev.Op != OpDelete || !reflect.DeepEqual(ev.Removed, []string{"hi"}) {
		t.Errorf("delete=%+v", ev)
	}
}

func TestCreateDeleteCancels(t *testing.T) {
	root := newRoot(t)
	cfg := testCfg()
	cfg.Debounce = 150 * time.Millisecond
	w := start(t, root, cfg)
	// No disk touch: the path never exists, so the kernel emits
	// nothing and the merge runs fully synchronously.
	p := filepath.Join(root, "ghost.txt")
	w.handle(fsnotify.Event{Name: p, Op: fsnotify.Create})
	w.handle(fsnotify.Event{Name: p, Op: fsnotify.Remove})
	none(t, w)
}

func TestIgnores(t *testing.T) {
	root := newRoot(t)
	w := start(t, root, testCfg())
	write(t, root, ".git/HEAD", "ref\n")
	write(t, root, "data/sessions/x.jsonl", "{}\n")
	write(t, root, ".hidden", "x\n")
	none(t, w)
}

func TestRecursiveAdd(t *testing.T) {
	root := newRoot(t)
	w := start(t, root, testCfg())
	write(t, root, "sub/deep/f.txt", "deep\n")
	if ev := next(t, w); ev.Path != "sub/deep/f.txt" || ev.Op != OpCreate {
		t.Errorf("event=%+v", ev)
	}
}

func TestCaps(t *testing.T) {
	root := newRoot(t)
	cfg := testCfg()
	cfg.DiffLines = 2
	cfg.MaxBytes = 32
	cfg.FileLines = 4
	w := start(t, root, cfg)
	write(t, root, "big.txt", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	ev := next(t, w)
	if !ev.Truncated || ev.AddedN != 4 || len(ev.Added) != 2 || len(ev.After) != 4 {
		t.Errorf("caps=%+v", ev)
	}
}

func TestDiffLines(t *testing.T) {
	old := []string{"a", "b", "c", "d"}
	new := []string{"a", "B", "C", "d"}
	added, removed, an, rn := diffLines(old, new, 10)
	if !reflect.DeepEqual(added, []string{"B", "C"}) || !reflect.DeepEqual(removed, []string{"b", "c"}) {
		t.Errorf("added=%v removed=%v", added, removed)
	}
	if an != 2 || rn != 2 {
		t.Errorf("counts=%d/%d", an, rn)
	}
	added, _, an, _ = diffLines(old, new, 1)
	if len(added) != 1 || an != 2 {
		t.Errorf("cap=%v/%d", added, an)
	}
}

func TestMergeOp(t *testing.T) {
	if got := mergeOp("", OpModify, true); got != OpModify {
		t.Error(got)
	}
	if got := mergeOp(OpCreate, OpModify, false); got != OpCreate {
		t.Error(got)
	}
	if got := mergeOp(OpCreate, OpDelete, false); got != "" {
		t.Error(got)
	}
	if got := mergeOp(OpModify, OpDelete, false); got != OpDelete {
		t.Error(got)
	}
	if got := mergeOp(OpDelete, OpCreate, false); got != OpModify {
		t.Error(got)
	}
}
