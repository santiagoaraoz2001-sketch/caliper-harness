package agent

import (
	"context"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

func TestExecutedNegativeResultDoesNotBecomeSchemaRepair(t *testing.T) {
	for _, code := range []int{0, 1, 128} {
		r := &Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry()}
		r.Registry.Register(tool.Definition{Name: "bash", Desc: "actual shell"})
		r.Continuation.Add("intent", "user", "Inspect current branch without changing it.", false)
		w := &watcher{}
		w.completedDiscovery(llm.Message{Role: "assistant", Content: "```tool\ntool: bash\ncmd: git rev-parse HEAD\n```"}, 5)
		note := &observed{CallID: "actual", Tool: "bash", Result: tool.Result{Text: "unborn HEAD", ExitCode: &code, Failed: code != 0}}
		r.enforcement.current = note
		w.discoveryReceipt(note, 5)
		if code != 0 {
			r.Continuation.Add("failure", "user", "git rev-parse HEAD exited nonzero", false)
		}
		p, err := r.progressEvidence(t.Context(), w)
		if err != nil || p.PreflightRefusal != "" || p.CurrentToolSchema != "" || p.LatestSettled.ExitCode == nil || *p.LatestSettled.ExitCode != code {
			t.Fatalf("execution became invalid syntax: %+v %v", p, err)
		}
	}
}

func TestSuccessfulInspectionsSkipBookkeepingButKeepFinalAndFailure(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "root"}}, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return BookkeepingFacts{}, nil }}}
	calls := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Option: q.Options[0], Confidence: .9}, nil
	}
	for i := 0; i < 20; i++ {
		r.enforcement.latest = &observed{Inspection: true, Tool: "read", Result: tool.Result{Text: "actual page", Truncated: true}}
		if _, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatalf("successful paged reads called judge %d times", calls)
	}
	r.enforcement.latest.Error = "actual read failed"
	_, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.enforce(t.Context(), true, "actual provisional answer", func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("failure/final advice lost: %d", calls)
	}
}

func TestDisabledCadenceStillProjectsCurrentRefusal(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry()}
	r.Registry.Register(tool.Definition{Name: "edit", Desc: "actual edit", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}}})
	w := &watcher{}
	zero := 0
	old := &observed{CallID: "old-read", Tool: "read", Inspection: true, Result: tool.Result{Text: "successful source", ExitCode: &zero}}
	w.completedDiscovery(llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "old-read", Name: "read", Arguments: `{}`}}}, 0)
	w.discoveryReceipt(old, 0)
	refused := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "refused-edit", Name: "edit", Arguments: `{}`}}}
	w.completedDiscovery(refused, 0)
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "reply", Message: refused}, session.Entry{Kind: "failure", Message: llm.Message{Role: "tool", Name: "edit", ToolCallID: "refused-edit", Content: "call not executed: entire call array refused; no calls ran; missing path"}})
	p, err := r.progressEvidence(t.Context(), w)
	if err != nil || p.PreflightRefusal == "" || p.CurrentToolSchema == "" {
		t.Fatalf("cadence disabled current refusal evidence: %+v %v", p, err)
	}
	if len(w.recentReplies) != 1 || len(w.recentReceipts) != 1 {
		t.Fatal("disabled cadence retained more than latest boundary")
	}
	next := &observed{CallID: "fixed-edit", Tool: "edit", Result: tool.Result{Text: "actual repaired call", Changed: true}}
	reply := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: next.CallID, Name: "edit", Arguments: `{"path":"actual.go"}`}}}
	w.completedDiscovery(reply, 0)
	w.discoveryReceipt(next, 0)
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "reply", Message: reply}, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", Name: "edit", ToolCallID: next.CallID, Content: "successful actual edit"}})
	p, err = r.progressEvidence(t.Context(), w)
	if err != nil || p.PreflightRefusal != "" || p.LatestSettled.CallID != next.CallID {
		t.Fatal("repaired current call retained stale refusal", err)
	}
}
