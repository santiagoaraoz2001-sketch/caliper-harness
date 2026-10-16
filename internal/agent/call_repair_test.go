package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func repairRunner() *Runner {
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "read", Purpose: "Read the requested file", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}}})
	reg.Register(tool.Definition{Name: "write", Purpose: "Write supplied file contents", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}, {Name: "content", Type: tool.TypeString, Required: true}}})
	reg.Register(tool.Definition{Name: "plan", Opening: true, Purpose: "Open the first planning question"})
	return &Runner{Registry: reg, Prompts: prompts.Builtin()}
}

func chooseRepair(name string, confidence float64) JudgeFunc {
	return func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		var packet callRepairPacket
		_ = json.Unmarshal([]byte(req.Context), &packet)
		for i, candidate := range packet.Candidates {
			if candidate.Name == name {
				return decision.Verdict{Pick: i + 1, Option: req.Options[i+1], Confidence: confidence}, nil
			}
		}
		return decision.Verdict{Pick: 0, Option: req.Options[0], Confidence: confidence}, nil
	}
}

func TestCallRepairPreservesArgumentsAndRecordsBeforeSelection(t *testing.T) {
	r := repairRunner()
	r.Continuation.Entries = []session.Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "Inspect the source file before proposing a fix. Do not change it yet."}}}
	var events []string
	r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		saveInterfaceProgressInput(t, "tool-repair-read", req)
		if len(events) != 1 || events[0] != "judgment-input" {
			t.Fatal("judgment input not recorded before judge", events)
		}
		if strings.Contains(req.Context, `"Name":"write"`) || strings.Contains(req.Context, `"Name":"plan"`) {
			t.Fatal("candidate invented or discarded arguments", req.Context)
		}
		return chooseRepair("read", .8)(ctx, req)
	}
	path := "dir/white space\nname.go"
	d, args, selected, err := r.repairInvocation(t.Context(), "inspect", tool.Args{"file_path": path}, "unknown tool", func(k, _ string) error { events = append(events, k); return nil })
	if err != nil || !selected || d.Name != "read" || args["path"] != path {
		t.Fatal(d.Name, args, selected, err)
	}
	if strings.Join(events, ",") != "judgment-input,judgment,tool-repair" {
		t.Fatal(events)
	}
}

func TestCallRepairNeverInventsOrDropsAuthoredData(t *testing.T) {
	for _, authored := range []tool.Args{{"content": "code without path"}, {"path": "a.go", "unknown": "must not discard"}, {"title": "stale plan field", "outcome": "must not silently discard"}} {
		r := repairRunner()
		r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
			t.Fatal("judge offered lossy candidate", authored)
			return decision.Verdict{}, nil
		}
		_, _, selected, err := r.repairInvocation(t.Context(), "mistyped", authored, "bad shape", func(string, string) error { return nil })
		if err != nil || selected {
			t.Fatal(selected, err)
		}
	}
}

func TestCallRepairOpeningAndHiddenRegistry(t *testing.T) {
	r := repairRunner()
	r.Judge = chooseRepair("plan", .9)
	var receipt string
	_, args, selected, err := r.repairInvocation(t.Context(), "plan", tool.Args{"query": "not a saved requirement"}, "unknown query", func(kind, text string) error {
		if kind == "tool-repair" {
			receipt = text
		}
		return nil
	})
	if err != nil || !selected || len(args) != 0 || !strings.Contains(receipt, `"IgnoredFields":["query"]`) || !strings.Contains(receipt, "not a saved requirement") {
		t.Fatal(args, selected, err, receipt)
	}
	r.Registry = r.Registry.View([]string{"plan"})
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		t.Fatal("hidden tool considered")
		return decision.Verdict{}, nil
	}
	_, _, selected, err = r.repairInvocation(t.Context(), "inspect", tool.Args{"path": "a.go"}, "unknown", func(string, string) error { return nil })
	if err != nil || selected {
		t.Fatal(selected, err)
	}
}

func TestCallRepairUnavailableUncertainAndBadVerdictRemainUnrepaired(t *testing.T) {
	for _, mode := range []string{"unavailable", "uncertain", "decline", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			r := repairRunner()
			r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				switch mode {
				case "unavailable":
					return decision.Verdict{}, errors.New("offline")
				case "uncertain":
					return chooseRepair("read", .49)(ctx, req)
				case "decline":
					return decision.Verdict{Pick: 0, Option: req.Options[0], Confidence: .99}, nil
				default:
					return decision.Verdict{Pick: 90, Option: req.Options[1], Confidence: .99}, nil
				}
			}
			_, _, selected, err := r.repairInvocation(t.Context(), "inspect", tool.Args{"path": "a"}, "unknown", func(string, string) error { return nil })
			if err != nil || selected {
				t.Fatal(selected, err)
			}
		})
	}
}

func TestCallRepairLogFailureAndCancellationPreventSelection(t *testing.T) {
	for _, fail := range []string{"judgment-input", "judgment", "tool-repair", "cancel"} {
		t.Run(fail, func(t *testing.T) {
			r := repairRunner()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				if fail == "cancel" {
					cancel()
				}
				return chooseRepair("read", .9)(ctx, req)
			}
			saved := errors.New("receipt write failed")
			_, _, selected, err := r.repairInvocation(ctx, "inspect", tool.Args{"path": "a"}, "unknown", func(k, _ string) error {
				if k == fail {
					return saved
				}
				return nil
			})
			if selected || err == nil {
				t.Fatal(selected, err)
			}
			if fail != "cancel" && !errors.Is(err, saved) {
				t.Fatal(err)
			}
		})
	}
}

func TestCallRepairCapacityPreservesCurrentReplyAndAuthoredValues(t *testing.T) {
	r := repairRunner()
	history := strings.Repeat("old source ", 500)
	r.Continuation.Entries = []session.Entry{{Message: llm.Message{Role: "user", Content: "repair a.go"}, Kind: "intent"}, {Message: llm.Message{Role: "tool", Content: history, Name: "read", ToolCallID: "old"}}}
	current := llm.Message{Role: "assistant", Reasoning: "I mean to read this source", ToolCalls: []llm.ToolCall{{ID: "now", Name: "inspect", Arguments: `{"path":"a.go"}`}}}
	calls := 0
	r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		calls++
		var packet callRepairPacket
		if err := json.Unmarshal([]byte(req.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if packet.AuthoredArguments["path"] != "a.go" || len(packet.CurrentReply) != 1 || packet.CurrentReply[0].Reasoning != current.Reasoning || len(packet.Candidates) != 1 {
			t.Fatal("essential evidence changed", req.Context)
		}
		if calls == 1 {
			if packet.WorkerContext[1].Content != history {
				t.Fatal("history omitted before admission")
			}
			return decision.Verdict{}, &decision.ContextCapacityError{Message: "capacity"}
		}
		if packet.WorkerContext[0].Content != "repair a.go" || packet.WorkerContext[1].Content != "" || len(packet.Progress.CapacityOmissions) != 1 {
			t.Fatal("omission not disclosed", req.Context)
		}
		return chooseRepair("read", .9)(ctx, req)
	}
	_, _, selected, err := r.repairInvocation(t.Context(), "inspect", tool.Args{"path": "a.go"}, "unknown", func(string, string) error { return nil }, current)
	if err != nil || !selected || calls != 2 || r.Continuation.Entries[1].Message.Content != history {
		t.Fatal(selected, calls, err)
	}
}

func TestCallRepairEssentialContextOverflowDeclinesWithoutRetry(t *testing.T) {
	r := repairRunner()
	calls := 0
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{}, &decision.ContextCapacityError{Message: "essential input too large"}
	}
	_, _, selected, err := r.repairInvocation(t.Context(), "inspect", tool.Args{"path": "a.go"}, "unknown", func(string, string) error { return nil }, llm.Message{Role: "assistant", Reasoning: strings.Repeat("current reasoning ", 100)})
	if err != nil || selected || calls != 1 {
		t.Fatal(selected, calls, err)
	}
}

func TestCallRepairConfidenceFloorAndCommandBytes(t *testing.T) {
	r := repairRunner()
	r.Registry.Register(tool.Definition{Name: "bash", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString, Required: true}}})
	r.Judge = chooseRepair("bash", .5)
	command := "printf '%s\\n' '$HOME'\n# trailing spaces  "
	_, args, selected, err := r.repairInvocation(t.Context(), "shell", tool.Args{"command": command}, "unknown", func(string, string) error { return nil })
	if err != nil || !selected || args["cmd"] != command {
		t.Fatal(args, selected, err)
	}
}

func TestKnownToolMissingAnswersDoesNotOfferUnrelatedTools(t *testing.T) {
	for _, name := range []string{"plan", "plan>"} {
		r := repairRunner()
		r.Registry = tool.NewRegistry()
		r.Registry.Register(tool.Definition{Name: "plan", Params: []tool.Param{{Name: "title", Type: tool.TypeString, Required: true}}})
		for _, other := range []string{"check", "find_dir", "help"} {
			r.Registry.Register(tool.Definition{Name: other})
		}
		r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
			t.Fatal("missing plan answer sent to unrelated selection")
			return decision.Verdict{}, nil
		}
		_, _, selected, err := r.repairInvocation(t.Context(), name, tool.Args{}, "plan needs title", func(string, string) error { return nil })
		if err != nil || selected {
			t.Fatal(name, selected, err)
		}
	}
}
