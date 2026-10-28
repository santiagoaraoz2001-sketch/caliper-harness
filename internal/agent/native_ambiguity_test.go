package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestDuplicateArgumentsRefuseWholeArrayAndAllowCorrectedCall(t *testing.T) {
	effects := 0
	r := Runner{Registry: tool.NewRegistry(), Prompts: prompts.Builtin()}
	r.Continuation = session.Continuation{Version: 1, Entries: []session.Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "exercise the probe"}}}}
	r.Registry.Register(tool.Definition{Name: "probe", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{Text: a["value"].(string)}, nil
	}})
	bad := `{"value":"first","value":"second"}`
	log := func(string, string) error { return nil }
	observe := func(string, bool, string) string { return "" }
	exec := func(ctx context.Context, d tool.Definition, a tool.Args) (tool.Result, error) {
		return d.Execute(ctx, a)
	}
	_, _, err := r.nativeStep(context.Background(), llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "bad", Name: "probe", Arguments: bad}, {ID: "innocent", Name: "probe", Arguments: `{"value":"do not run"}`},
	}}, exec, log, nil, observe)
	if err != nil || effects != 0 {
		t.Fatalf("recoverable refusal should have no effects: err=%v effects=%d", err, effects)
	}
	if len(r.Continuation.Entries) != 4 || r.Continuation.Entries[1].Message.ToolCalls[0].Arguments != bad {
		t.Fatal("raw authored ambiguity or correlated results lost")
	}
	for _, e := range r.Continuation.Entries[2:] {
		if !strings.Contains(e.Message.Content, "no calls ran") || e.Kind != "failure" {
			t.Fatalf("missing array refusal: %+v", e)
		}
	}
	_, _, err = r.nativeStep(context.Background(), llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "corrected", Name: "probe", Arguments: `{"value":"corrected"}`}}}, exec, log, nil, observe)
	if err != nil || effects != 1 {
		t.Fatalf("explicit corrected call did not recover exactly once: err=%v effects=%d", err, effects)
	}
}
