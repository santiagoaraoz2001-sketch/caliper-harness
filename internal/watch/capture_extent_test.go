package watch

import (
	"strings"
	"testing"
)

func TestChangeOutsideRetainedLinesIsExplicit(t *testing.T) {
	root := newRoot(t)
	write(t, root, "long.txt", "one\ntwo\nold tail\n")
	cfg := testCfg()
	cfg.FileLines = 2
	cfg.MaxBytes = 1024
	w := start(t, root, cfg)
	write(t, root, "long.txt", "one\ntwo\nnew tail\n")
	ev := next(t, w)
	if !ev.Truncated || !ev.OutsideCapture || ev.AddedN != 0 || ev.RemovedN != 0 || strings.Join(ev.After, "\n") != "one\ntwo" {
		t.Fatalf("%+v", ev)
	}
}
