package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools"
	"specific-harness/internal/tools/files"
)

// Real ripgrep/builtin registry/Runner/native settlement; model and judge are doubles.
func searchProgressRegistry(t *testing.T) (*tool.Registry, string) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Fatal("actual ripgrep required", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("NEEDLE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	cfg := config.Defaults()
	tools.RegisterAt(reg, cfg, strings.NewReader(""), io.Discard, nil, &files.Workspace{Path: dir, Caps: cfg.Tools.Bash})
	return reg, dir
}
func searchProgressCall(id, name string, args tool.Args) llm.ToolCall {
	raw, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}
}
func zeroCalls(n int) []llm.ToolCall {
	var calls []llm.ToolCall
	for i := 0; i < n; i++ {
		calls = append(calls, searchProgressCall(fmt.Sprint("zero", i), "grep", tool.Args{"pattern": fmt.Sprint("absent", i)}))
	}
	return calls
}
func TestSearchZeroSuccessNativeArrayAndStall(t *testing.T) {
	for _, stall := range []int{0, 5} {
		t.Run(fmt.Sprint(stall), func(t *testing.T) {
			reg, dir := searchProgressRegistry(t)
			calls := zeroCalls(5)
			calls = append(calls, searchProgressCall("tail", "bash", tool.Args{"cmd": "printf tail > marker"}))
			model := &serialModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}}}
			judgments := 0
			r := &agent.Runner{Model: model, Registry: reg, MaxSteps: 7, LoopStall: stall, Threshold: .7, Session: &nativeRecordingSink{}, Judge: func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				var input struct {
					Latest struct {
						Tool      string
						Args      tool.Args
						NoMatches bool
					}
					ProgressBasis string
				}
				if err := json.Unmarshal([]byte(q.Context), &input); err != nil {
					t.Fatal(err)
				}
				if input.Latest.Tool != "bash" || !strings.Contains(input.ProgressBasis, "NoMatches") {
					t.Fatalf("missing actual query-specific evidence: %s", q.Context)
				}
				return decision.Verdict{Option: q.Options[2], Confidence: .9}, nil
			}}
			if _, _, err := r.Run(context.Background(), "source discovery component"); err != nil {
				t.Fatal(err)
			}
			_, markerErr := os.Stat(filepath.Join(dir, "marker"))
			if stall == 5 {
				if judgments != 1 || r.Outcome.Status != outcome.Completed || markerErr != nil {
					t.Fatalf("progress advice interrupted actual native tail: judgments=%d outcome=%+v marker=%v", judgments, r.Outcome, markerErr)
				}
			} else if judgments != 0 || markerErr != nil {
				t.Fatalf("disabled watcher or valid native tail broken: %d %v", judgments, markerErr)
			}
			results := receipts(t, r)
			if len(results) != 6 {
				t.Fatalf("lost native associations: %+v", results)
			}
			for i, e := range results {
				if e.Message.ToolCallID != calls[i].ID || e.Message.Name != calls[i].Name {
					t.Fatal("uncorrelated native result")
				}
				if i < 5 && (e.Kind != "output" || !strings.Contains(e.Message.Content, "this query only")) {
					t.Fatalf("zero match became failed invocation: %+v", e)
				}
			}
		})
	}
}
func TestSearchZeroPositiveAndHelpReset(t *testing.T) {
	for _, name := range []string{"grep", "help"} {
		t.Run(name, func(t *testing.T) {
			reg, _ := searchProgressRegistry(t)
			calls := zeroCalls(4)
			args := tool.Args{"pattern": "NEEDLE"}
			if name == "help" {
				args = tool.Args{"topic": "grep"}
			}
			calls = append(calls, searchProgressCall("reset", name, args))
			for i := 0; i < 4; i++ {
				calls = append(calls, searchProgressCall(fmt.Sprint("later", i), "grep", tool.Args{"pattern": fmt.Sprint("later_absent", i)}))
			}
			model := &serialModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}}}
			judgments := 0
			r := &agent.Runner{Model: model, Registry: reg, MaxSteps: 10, LoopStall: 5, Threshold: .7, Session: &nativeRecordingSink{}, Judge: func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				return decision.Verdict{Option: q.Options[2], Confidence: .9}, nil
			}}
			if _, _, err := r.Run(context.Background(), "source discovery component"); err != nil {
				t.Fatal(err)
			}
			if judgments != 0 || len(receipts(t, r)) != 9 || r.Outcome.Status != outcome.Completed {
				t.Fatalf("positive/helper poisoned by prior zero: %d %+v", judgments, r.Outcome)
			}
		})
	}
}

type searchTextModel struct{ replies []string }

func (m *searchTextModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	if len(m.replies) == 0 {
		return "final", "stop", llm.Usage{}, nil
	}
	text := m.replies[0]
	m.replies = m.replies[1:]
	return text, "stop", llm.Usage{}, nil
}
func TestSearchZeroValidationHasNoStaleMetadata(t *testing.T) {
	reg, _ := searchProgressRegistry(t)
	m := &searchTextModel{replies: []string{"```tool\ntool: grep\npattern: absent\n```", "```tool\ntool: grep\nunavailable_slot: value\n```"}}
	judgments := 0
	r := &agent.Runner{Model: m, Registry: reg, MaxSteps: 3, LoopStall: 2, Threshold: .7, Judge: func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		var input struct{ Latest json.RawMessage }
		if err := json.Unmarshal([]byte(q.Context), &input); err != nil {
			t.Fatal(err)
		}
		if string(input.Latest) != "null" {
			t.Fatalf("validation reused prior search outcome: %s", q.Context)
		}
		return decision.Verdict{Option: q.Options[2], Confidence: .9}, nil
	}}
	if _, _, err := r.Run(context.Background(), "source discovery component"); err != nil {
		t.Fatal(err)
	}
	if judgments != 1 || r.Outcome.Status != outcome.Completed {
		t.Fatalf("validation watcher unchanged: %d %+v", judgments, r.Outcome)
	}
}
