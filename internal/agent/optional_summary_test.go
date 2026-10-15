package agent

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestDeterministicActualHelper66NeverRequested(t *testing.T) {
	raw, err := os.ReadFile("testdata/optional-summary/continuation.json")
	if err != nil {
		t.Fatal(err)
	}
	var c session.Continuation
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	reply, err := os.ReadFile("testdata/optional-summary/reply.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(string(reply))) != 177 || len(reply) != 1405 {
		t.Fatal("actual helper fixture drift")
	}
	m := &scriptModel{replies: []string{"done"}}
	sink := &recordSink{}
	r := activeRunner(m, sink, c)
	r.Compaction.WordsPerToken = .75
	r.ModelKey = c.ModelKey
	if _, _, err = r.Run(context.Background(), "Continue actual task."); err != nil {
		t.Fatal(err)
	}
	if len(m.seen) != 1 {
		t.Fatalf("expected main only, requests=%d", len(m.seen))
	}
	for _, want := range c.Entries[1:] {
		found := false
		for _, got := range m.seen[0] {
			if reflect.DeepEqual(want.Message, got) {
				found = true
			}
		}
		if !found {
			t.Fatal("original native group changed on next main request")
		}
	}
	if changed, err := r.maintainDiscovery(context.Background()); changed || err != nil {
		t.Fatalf("unchanged retry %v %v", changed, err)
	}
	if len(m.seen) != 1 || r.Continuation.Compactions != 0 {
		t.Fatal("repeated helper or fake applied summary")
	}
	for _, k := range sink.kinds {
		if k == "compaction_rejected" {
			t.Fatal("obsolete candidate path")
		}
	}
}
