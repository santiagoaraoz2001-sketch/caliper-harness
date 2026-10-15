package agent

import (
	"context"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
)

// scriptStreamer replays canned replies plus fixed deltas.
type scriptStreamer struct {
	scriptModel
	streamed int
}

func (m *scriptStreamer) ChatStream(ctx context.Context, msgs []llm.Message, onDelta func(string)) (string, string, llm.Usage, error) {
	m.streamed++
	reply, served, u, err := m.Chat(ctx, msgs)
	if onDelta != nil {
		onDelta("d1")
		onDelta("d2")
	}
	return reply, served, u, err
}

func TestOnTokenStreams(t *testing.T) {
	model := &scriptStreamer{scriptModel: scriptModel{replies: []string{"done"}}}
	var deltas []string
	r := &Runner{Model: model, Registry: tool.NewRegistry(), MaxSteps: 5, OnToken: func(s string) { deltas = append(deltas, s) }}
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	if model.streamed != 1 || len(deltas) != 2 || deltas[0] != "d1" || deltas[1] != "d2" {
		t.Errorf("streamed=%d deltas=%v", model.streamed, deltas)
	}
}

func TestOnTokenFallsBack(t *testing.T) {
	model := &scriptModel{replies: []string{"done"}}
	called := false
	r := &Runner{Model: model, Registry: tool.NewRegistry(), MaxSteps: 5, OnToken: func(string) { called = true }}
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	if called {
		t.Error("plain model must not receive deltas")
	}
}

func TestHistoryInjects(t *testing.T) {
	model := &scriptModel{replies: []string{"done"}}
	r := &Runner{Model: model, Registry: tool.NewRegistry(), MaxSteps: 5, History: []llm.Message{
		{Role: "user", Content: "old q"},
		{Role: "assistant", Content: "old a"},
	}}
	if _, _, err := r.Run(context.Background(), "new q"); err != nil {
		t.Fatal(err)
	}
	msgs := model.seen[0]
	if len(msgs) != 4 || msgs[0].Role != "system" || msgs[1].Content != "old q" || msgs[2].Content != "old a" || msgs[3].Content != "new q" {
		t.Errorf("msgs=%+v", msgs)
	}
}
