package watch

import (
	"testing"
	"time"
)

func TestDependencyTreesDoNotDisplaceAuthoredCaptures(t *testing.T) {
	root := newRoot(t)
	write(t, root, "node_modules/pkg/generated.js", "generated")
	write(t, root, "packages/app/node_modules/pkg/generated.js", "nested generated")
	write(t, root, "packages/app/src/node_modules-notes.js", "authored")
	cfg := DefaultConfig()
	cfg.Debounce = 10 * time.Millisecond
	cfg.StartupMaxFiles = 1
	w := start(t, root, cfg)
	if len(w.snaps) != 1 || w.snaps["packages/app/src/node_modules-notes.js"].lines == nil {
		t.Fatalf("dependencies displaced authored baseline: %+v", w.snaps)
	}
	write(t, root, "packages/app/node_modules/pkg/later.js", "generated later")
	write(t, root, "packages/app/src/node_modules-notes.js", "authored changed")
	select {
	case event := <-w.Events():
		if event.Path != "packages/app/src/node_modules-notes.js" {
			t.Fatalf("dependency event leaked: %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authored edit was not captured")
	}
}
