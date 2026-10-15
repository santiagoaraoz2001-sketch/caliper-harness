package agent

import (
	"context"
	"testing"

	"specific-harness/internal/tool"
)

func TestMemoriesInjectAfterPrompt(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"done"}}
	sink := &recordSink{}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, SessionID: "m", Session: sink, Memories: "Relevant memories:\n- [note] x"}
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	msgs := model.seen[0]
	if len(msgs) != 3 || msgs[0].Role != "system" || msgs[1].Role != "user" || msgs[1].Content != "go" ||
		msgs[2].Role != "user" || msgs[2].Content != "Relevant memories:\n- [note] x" {
		t.Errorf("msgs=%+v", msgs)
	}
	var found bool
	for i, k := range sink.kinds {
		if k == "recall" && sink.texts[i] == "Relevant memories:\n- [note] x" {
			found = true
		}
	}
	if !found {
		t.Errorf("no recall event in %v", sink.kinds)
	}
}

func TestMemoriesEmptyOmits(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"done"}}
	sink := &recordSink{}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, SessionID: "m", Session: sink}
	if _, _, err := r.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(model.seen[0]) != 2 {
		t.Errorf("msgs=%+v", model.seen[0])
	}
	for _, k := range sink.kinds {
		if k == "recall" {
			t.Errorf("empty memories must not log recall: %v", sink.kinds)
		}
	}
}
