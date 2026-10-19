package run

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/store"
)

func TestNewWiresRunner(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, notes, err := New(Deps{
		Tools: "full", Cfg: config.Defaults(), Server: "http://x/v1", Model: "m",
		MaxSteps: 5, Window: 1000, Store: s, SessionID: "t",
		AskYesNo: func(context.Context, string) bool { return true },
		In:       strings.NewReader(""), Out: io.Discard,
	}, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "not applied") {
		t.Errorf("notes=%v", notes)
	}
	for _, name := range []string{"bash", "ask", "launch", "judge", "recall"} {
		if _, ok := r.Registry.Lookup(name); !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	if r.Ask == nil || r.Exec == nil || r.MaxSteps != 5 || r.MaxTokens != 1000 {
		t.Error("runner fields not threaded")
	}
}

func TestRetiredAbsoluteCompactionSettingIsIgnored(t *testing.T) {
	cfg := config.Defaults()
	base := compactionFromConfig(cfg)
	cfg.Context.CompactionAbsoluteTokens = 65536 // Old saved YAML still loads, but cannot activate this trigger.
	if got := compactionFromConfig(cfg); got != base {
		t.Fatalf("retired setting changed compaction policy: %+v", got)
	}
	if base.Trigger != 90 {
		t.Fatalf("unexpected context policy: %+v", cfg.Context)
	}
}
