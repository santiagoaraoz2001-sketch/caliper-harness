package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

type terminalSink struct {
	failKind string
	cause    error
	kinds    []string
}

func (s *terminalSink) Append(_ context.Context, _ string, kind, _ string) error {
	s.kinds = append(s.kinds, kind)
	if kind == s.failKind {
		return s.cause
	}
	return nil
}

func TestOutcomeTerminalSessionFailures(t *testing.T) {
	for _, tc := range []struct {
		name, failKind string
		stop, cancel   bool
		want           outcome.Status
		code           int
	}{
		{"completed control", "", false, false, outcome.Completed, 0},
		{"done append failure", "done", false, false, outcome.Failed, 1},
		{"stopped append failure", "stopped", true, false, outcome.Stopped, 1},
		{"cancelled append failure", "cancelled", false, true, outcome.Cancelled, 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("terminal session unavailable")
			sink := &terminalSink{failKind: tc.failKind, cause: cause}
			model := &scriptModel{replies: []string{"raw final"}}
			if tc.stop {
				model.replies = []string{"```tool\ntool: absent\nx: y\n```"}
			}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				model.err = context.Canceled
			}
			r := &Runner{Model: model, Registry: tool.NewRegistry(), MaxSteps: 1, Session: sink}
			text, tx, err := r.Run(ctx, "real component prompt")
			if r.Outcome.Status != tc.want || r.Outcome.ExitCode() != tc.code {
				t.Fatalf("outcome=%+v code=%d", r.Outcome, r.Outcome.ExitCode())
			}
			if tc.failKind != "" && !errors.Is(err, cause) {
				t.Errorf("terminal cause lost: %v", err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation cause lost: %v", err)
			}
			if !tc.stop && !tc.cancel && text != "raw final" {
				t.Errorf("raw output=%q", text)
			}
			if len(tx) < 2 || len(sink.kinds) < 2 {
				t.Errorf("evidence lost: tx=%+v kinds=%v", tx, sink.kinds)
			}
			if tc.stop && !strings.Contains(r.Outcome.Reason, "step") {
				t.Errorf("actual managed-stop reason lost: %+v", r.Outcome)
			}
		})
	}
}

func TestOutcomeWorkflowRefusalCannotBeMasked(t *testing.T) {
	for _, name := range []string{"validation", "blocked", "exec error", "missing ref", "wrong report", "failed qualified report", "unknown ref", "uncertain owner", "inconsistent qualified status"} {
		t.Run(name, func(t *testing.T) {
			ref := "a"
			slots := "approach: a\n"
			def := tool.Definition{Name: "workflow", WorkflowRefArg: "approach", ResolveWorkflowRef: func(_ context.Context, args tool.Args) (string, error) {
				ref, _ := args["approach"].(string)
				if ref == "unknown" {
					return "", errors.New("no owning approach")
				}
				if name == "uncertain owner" {
					return ref, errors.New("owner read unavailable")
				}
				return ref, nil
			}, Params: []tool.Param{{Name: "approach", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) { return tool.Result{}, nil }}
			switch name {
			case "validation":
				def.Params = append(def.Params, tool.Param{Name: "other", Type: tool.TypeString, Required: true})
			case "missing ref":
				slots = "other: x\n"
			case "unknown ref":
				ref = "unknown"
				slots = "approach: unknown\n"
			}
			executed := 0
			reg := tool.NewRegistry()
			reg.Register(def)
			r := &Runner{Model: &scriptModel{replies: []string{"```tool\ntool: workflow\n" + slots + "```", "Everything succeeded."}}, Registry: reg, MaxSteps: 2, Exec: func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				executed++
				switch name {
				case "blocked":
					return tool.Result{Blocked: true, Text: "declined"}, nil
				case "exec error":
					return tool.Result{}, errors.New("execution refused")
				case "wrong report":
					return tool.Result{Workflow: &outcome.Report{Ref: "wrong", Qualified: true, Outcome: outcome.Outcome{Status: outcome.Completed}}}, nil
				case "inconsistent qualified status":
					return tool.Result{Workflow: &outcome.Report{Ref: ref, Qualified: true, Outcome: outcome.Outcome{Status: outcome.Stopped}}}, nil
				case "failed qualified report":
					return tool.Result{Failed: true, Workflow: &outcome.Report{Ref: ref, Qualified: true, Outcome: outcome.Outcome{Status: outcome.Completed}}}, nil
				}
				return tool.Result{}, nil
			}}
			text, _, err := r.Run(context.Background(), "safe fixture workflow")
			if err != nil {
				t.Fatal(err)
			}
			if (name == "validation" || name == "missing ref" || name == "unknown ref" || name == "uncertain owner") && executed != 0 {
				t.Fatal("refused invocation executed")
			}
			if name == "validation" || name == "missing ref" || name == "unknown ref" {
				if text != "Everything succeeded." || r.Outcome.Status != outcome.Completed || len(r.workflows) != 0 {
					t.Fatalf("invalid invocation admitted workflow: %+v %v", r.Outcome, r.workflows)
				}
				return
			}
			if text != "Everything succeeded." || r.Outcome.ExitCode() != 1 || !strings.Contains(r.Outcome.Reason, "required workflow unresolved") {
				t.Fatalf("final=%q outcome=%+v", text, r.Outcome)
			}
		})
	}
}

func TestOutcomeWorkflowSameRefReadbackAndReuse(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "workflow", WorkflowRefArg: "approach", ResolveWorkflowRef: func(_ context.Context, args tool.Args) (string, error) { return args["approach"].(string), nil }, Params: []tool.Param{{Name: "approach", Type: tool.TypeString, Required: true}}})
	call := "```tool\ntool: workflow\napproach: a\n```"
	n := 0
	r := &Runner{Model: &scriptModel{replies: []string{call, call, "qualified final"}}, Registry: reg, MaxSteps: 3, Exec: func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		n++
		if n == 1 {
			return tool.Result{Blocked: true, Text: "declined"}, nil
		}
		return tool.Result{Text: "owning readback", Workflow: &outcome.Report{Ref: "a", Qualified: true, Outcome: outcome.Outcome{Status: outcome.Completed}}}, nil
	}}
	_, _, err := r.Run(context.Background(), "same ref")
	if err != nil || r.Outcome.Status != outcome.Completed {
		t.Fatalf("outcome=%+v err=%v", r.Outcome, err)
	}
	r.Model = &scriptModel{replies: []string{"plain next final"}}
	_, _, err = r.Run(context.Background(), "independent next run")
	if err != nil || r.Outcome.Status != outcome.Completed || len(r.workflows) != 0 {
		t.Fatalf("stale workflow=%v outcome=%+v", r.workflows, r.Outcome)
	}
	// Discovery does not register a required invocation.
	r.Model = &scriptModel{replies: []string{"```tool\ntool: workflow\n```", "schema inspected"}}
	_, _, err = r.Run(context.Background(), "schema only")
	if err != nil || r.Outcome.Status != outcome.Completed || len(r.workflows) != 0 {
		t.Fatalf("schema marked required: %v %+v", r.workflows, r.Outcome)
	}
}

var _ Model = (*scriptModel)(nil)

func TestOutcomeInvalidReferenceCannotClearKnownObligation(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "workflow", WorkflowRefArg: "approach", ResolveWorkflowRef: func(_ context.Context, a tool.Args) (string, error) {
		if a["approach"] == "known" {
			return "known", nil
		}
		return "", errors.New("not an approach")
	}, Params: []tool.Param{{Name: "approach", Type: tool.TypeString, Required: true}}})
	r := &Runner{Model: &scriptModel{replies: []string{"```tool\ntool: workflow\napproach: known\n```", "```tool\ntool: workflow\napproach: invalid\n```", "all passed"}}, Registry: reg, MaxSteps: 3, Exec: func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		return tool.Result{Blocked: true, Text: "actual refusal"}, nil
	}}
	if _, _, e := r.Run(t.Context(), "component owner refusal"); e != nil {
		t.Fatal(e)
	}
	if r.Outcome.ExitCode() != 1 || len(r.workflows) != 1 || !strings.Contains(r.Outcome.Reason, "known") || strings.Contains(r.Outcome.Reason, "invalid") {
		t.Fatalf("known obligation lost or ghost added: %+v %v", r.Outcome, r.workflows)
	}
}
