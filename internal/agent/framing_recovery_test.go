package agent

import (
	"context"
	"errors"
	"io"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

type framingModel struct {
	failures int
	fail     *llm.ToolFramingError
	seen     [][]llm.Message
	calls    []llm.ToolCall
}

func (m *framingModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	panic("native only")
}
func (m *framingModel) ChatTools(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.seen = append(m.seen, messages)
	if len(m.seen) <= max(1, m.failures) {
		return llm.Reply{Message: llm.Message{Role: "assistant", Reasoning: "I should open planning now.", ToolCalls: m.calls}}, m.fail
	}
	return llm.Reply{Message: llm.Message{Role: "assistant", Content: "finished"}, FinishReason: "stop"}, nil
}
func TestFramingRecoveryCapturedDrafts(t *testing.T) {
	for _, draft := range []string{"plan><arg_key></arg_key><arg_value>{}</arg_value>", "plan<arg_key></arg_key><arg_value>{}</arg_value>"} {
		t.Run(draft, func(t *testing.T) {
			r := repairRunner()
			m := &framingModel{fail: &llm.ToolFramingError{Draft: draft, Reason: "empty argument key", Boundary: "tool_close"}}
			r.Model = m
			r.MaxSteps = 3
			r.Out = io.Discard
			judged, executed := false, 0
			r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				saveInterfaceProgressInput(t, "framing-plan", req)
				if !strings.Contains(req.Context, "empty argument key") {
					t.Fatal(req.Context)
				}
				judged = true
				return chooseRepair("plan", .9)(ctx, req)
			}
			r.Exec = func(_ context.Context, d tool.Definition, a tool.Args) (tool.Result, error) {
				if !judged || d.Name != "plan" || len(a) != 0 {
					t.Fatal(judged, d.Name, a)
				}
				executed++
				return tool.Result{Text: "first question"}, nil
			}
			_, _, err := r.Run(t.Context(), "plan then work")
			if err != nil || executed != 1 || len(m.seen) != 2 {
				t.Fatal(err, executed, len(m.seen))
			}
			if err := r.Continuation.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestFramingRecoveryDeclinedContinuesWithoutPrefixEffects(t *testing.T) {
	r := repairRunner()
	m := &framingModel{fail: &llm.ToolFramingError{Draft: "plan><arg_key></arg_key><arg_value>{}</arg_value>", Reason: "empty argument key", Boundary: "tool_close", CallIndex: 1}, calls: []llm.ToolCall{{ID: "first", Name: "read", Arguments: `{"path":"a"}`}}}
	r.Model = m
	r.MaxSteps = 3
	r.Out = io.Discard
	r.Judge = chooseRepair("plan", .4)
	r.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		t.Fatal("executed declined array")
		return tool.Result{}, nil
	}
	_, _, err := r.Run(t.Context(), "do work")
	if err != nil || len(m.seen) != 2 {
		t.Fatal(err, len(m.seen))
	}
	last := m.seen[1][len(m.seen[1])-1].Content
	if !strings.Contains(last, "empty argument key") {
		t.Fatal(last)
	}
	if err := r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestFramingRecoveryCannotOutrunArchiveOrCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		r := repairRunner()
		r.Judge = chooseRepair("plan", .9)
		ctx, stop := context.WithCancel(t.Context())
		if cancel {
			stop()
		}
		defer stop()
		sentinel := errors.New("archive unavailable")
		_, selected, err := r.recoverFraming(ctx, llm.Reply{}, &llm.ToolFramingError{Draft: "plan><arg_key></arg_key><arg_value>{}</arg_value>"}, func(string, string) error {
			if !cancel {
				return sentinel
			}
			return nil
		})
		if selected || err == nil {
			t.Fatal(selected, err)
		}
	}
}

func TestFramingRecoveryPreservesAuthorizationAndWholeArrayPolicy(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "plan must be alone"}[prefix], func(t *testing.T) {
			r := repairRunner()
			m := &framingModel{fail: &llm.ToolFramingError{Draft: "plan><arg_key></arg_key><arg_value>{}</arg_value>", Reason: "empty argument key", Boundary: "tool_close"}}
			if prefix {
				m.calls = []llm.ToolCall{{ID: "first", Name: "read", Arguments: `{"path":"a"}`}}
				m.fail.CallIndex = 1
			}
			r.Model = m
			r.MaxSteps = 4
			r.Out = io.Discard
			r.Judge = chooseRepair("plan", .9)
			approvals := 0
			r.Exec = func(_ context.Context, _ tool.Definition, _ tool.Args) (tool.Result, error) {
				approvals++
				return tool.Result{Blocked: true, Text: "user denied"}, nil
			}
			_, _, err := r.Run(t.Context(), "open planning")
			want := 1
			if prefix {
				want = 0
			}
			if err != nil || approvals != want {
				t.Fatal(err, approvals)
			}
			if err := r.Continuation.Validate(); err != nil {
				t.Fatal(err)
			}
			joined := ""
			for _, e := range r.Continuation.Entries {
				if e.Message.Role == "tool" {
					joined += e.Message.Content
				}
			}
			if !prefix && !strings.Contains(joined, "user denied") {
				t.Fatal(joined)
			}
			if prefix && !strings.Contains(joined, "call it alone") {
				t.Fatal(joined)
			}
		})
	}
}

func TestFramingRecoveryGenericCallKeepsAuthoredValueAndSerialPrefix(t *testing.T) {
	r := repairRunner()
	path := "directory/file.go"
	m := &framingModel{fail: &llm.ToolFramingError{Draft: "read><arg_key>path</arg_key><arg_value>directory/file.go</arg_value>", Reason: "malformed name separator", Boundary: "tool_close", CallIndex: 1}, calls: []llm.ToolCall{{ID: "first", Name: "read", Arguments: `{"path":"first.go"}`}}}
	r.Model = m
	r.MaxSteps = 4
	r.Out = io.Discard
	r.Judge = chooseRepair("read", .9)
	var paths []string
	r.Exec = func(_ context.Context, d tool.Definition, a tool.Args) (tool.Result, error) {
		if d.Name != "read" {
			t.Fatal(d.Name)
		}
		paths = append(paths, a["path"].(string))
		return tool.Result{Text: "source"}, nil
	}
	_, _, err := r.Run(t.Context(), "inspect sources")
	if err != nil || strings.Join(paths, ",") != "first.go,"+path {
		t.Fatal(err, paths)
	}
	if err := r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFramingRecoveryDeclinesUseExistingProgressWatcher(t *testing.T) {
	r := repairRunner()
	m := &framingModel{failures: 2, fail: &llm.ToolFramingError{Draft: "plan><arg_key></arg_key><arg_value>{}</arg_value>", Reason: "empty argument key", Boundary: "tool_close"}}
	r.Model = m
	r.MaxSteps = 4
	r.Out = io.Discard
	r.LoopRepeat = 2
	loops := 0
	r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		if strings.Contains(req.Context, `"Candidates"`) {
			return chooseRepair("plan", .4)(ctx, req)
		}
		loops++
		return decision.Verdict{Pick: 0, Option: req.Options[0], Confidence: .9}, nil
	}
	r.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		t.Fatal("declined call ran")
		return tool.Result{}, nil
	}
	_, _, err := r.Run(t.Context(), "plan work")
	if err != nil || loops != 1 || len(m.seen) != 3 {
		t.Fatal(err, loops, len(m.seen))
	}
}
