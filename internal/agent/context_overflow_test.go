package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
)

func TestContextOverflowClassification(t *testing.T) {
	rulith := errors.New(`Rulith native completion HTTP 400: {"error":{"code":400,"message":"request (262334 tokens) exceeds the available context size (262144 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":262334,"n_ctx":262144}}`)
	if n, ok := contextOverflow(rulith); !ok || n != 262334 {
		t.Fatalf("rulith overflow not classified: n=%d ok=%v", n, ok)
	}
	if n, ok := contextOverflow(errors.New("This model's maximum context length is 131072 tokens")); !ok || n != 0 {
		t.Fatalf("generic overflow not classified: n=%d ok=%v", n, ok)
	}
	for _, other := range []error{nil, errors.New("connection refused"), errors.New("request exceeds rate limit"), errors.New("context canceled")} {
		if _, ok := contextOverflow(other); ok {
			t.Fatalf("non-overflow classified as overflow: %v", other)
		}
	}
}

type overflowOnceModel struct {
	calls     int
	summaries int
}

func (m *overflowOnceModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "WORDS=") {
			m.summaries++
			return "Repair pending; actual commands failed; a listing was observed and no changes were made.", "", llm.Usage{In: 20, Out: 5, Reported: true}, nil
		}
	}
	m.calls++
	if m.calls == 1 {
		return "", "", llm.Usage{}, fmt.Errorf(`Rulith native completion HTTP 400: {"error":{"code":400,"message":"request (3000 tokens) exceeds the available context size (2000 tokens)","type":"exceed_context_size_error","n_prompt_tokens":3000,"n_ctx":2000}}`)
	}
	return "done", "", llm.Usage{In: 100, Out: 5, Reported: true}, nil
}

// A provider context-window refusal must compact and retry the step, not
// terminate the run: the estimate drifted under the provider's real count.
func TestRunRecoversFromProviderContextOverflow(t *testing.T) {
	model := &overflowOnceModel{}
	sink := &recordSink{}
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, MaxTokens: 100000,
		SessionID: "overflow-test", Session: sink, Out: io.Discard,
		Compaction: testCompaction(), TemplatesDir: testTemplateDir(t), Judge: acceptCompaction, Threshold: .7}
	r.Compaction.Keep = 0
	// Eligible history so compaction can actually publish a smaller packet.
	r.Continuation.Add("reply", "assistant", strings.Repeat("old worker text ", 200), false)
	r.Continuation.Add("output", "user", "tool result: actual listing; no changes made", false)
	r.Continuation.Add("schema", "user", "bash schema: command requires a single tool call", false)
	r.Continuation.Add("failure", "user", "ACTUAL FAILURE: no Go files [exit code 1]", false)
	r.Continuation.Add("evidence", "user", "RECEIPTS: one failure; declared-check correctness unverified", false)
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("overflow was terminal instead of recovered: %v", err)
	}
	if final != "done" || model.calls != 2 || model.summaries != 1 {
		t.Fatalf("final=%q worker calls=%d summaries=%d", final, model.calls, model.summaries)
	}
	compacts := 0
	for _, k := range sink.kinds {
		if k == "compact" {
			compacts++
		}
	}
	if compacts != 1 || r.Continuation.Compactions != 1 {
		t.Fatalf("compact events=%d continuation compactions=%d", compacts, r.Continuation.Compactions)
	}
}
