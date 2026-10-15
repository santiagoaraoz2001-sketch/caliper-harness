package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

func TestNoCallCeilingCrosses25AndNativeArray(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			var calls []string
			reg := tool.NewRegistry()
			reg.Register(echoTool(&calls))
			var model Model
			if native {
				m := &discoveryModel{}
				reply := llm.Reply{Message: llm.Message{Role: "assistant"}}
				for i := 0; i < 30; i++ {
					reply.Message.ToolCalls = append(reply.Message.ToolCalls, llm.ToolCall{ID: fmt.Sprint(i), Name: "echo", Arguments: fmt.Sprintf(`{"text":%q}`, fmt.Sprint(i))})
				}
				m.replies = []llm.Reply{reply}
				model = m
			} else {
				m := &scriptModel{}
				for i := 0; i < 30; i++ {
					m.replies = append(m.replies, echoBlock(fmt.Sprint(i)))
				}
				m.replies = append(m.replies, "done")
				model = m
			}
			r := &Runner{Model: model, Registry: reg, MaxSteps: 0, Session: &recordSink{}, SessionID: "no-ceiling"}
			_, _, err := r.Run(context.Background(), "complete this bounded component task")
			if err != nil || r.Outcome.Status != outcome.Completed || len(calls) != 30 {
				t.Fatalf("no ceiling calls=%d outcome=%+v error=%v", len(calls), r.Outcome, err)
			}
		})
	}
}
func TestPositiveCallCeilingExactArrayAndRefusal(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var calls []string
			reg := tool.NewRegistry()
			reg.Register(echoTool(&calls))
			reply := llm.Reply{Message: llm.Message{Role: "assistant"}}
			for i := 0; i < n; i++ {
				reply.Message.ToolCalls = append(reply.Message.ToolCalls, llm.ToolCall{ID: fmt.Sprint(i), Name: "echo", Arguments: fmt.Sprintf(`{"text":%q}`, fmt.Sprint(i))})
			}
			r := &Runner{Model: &discoveryModel{replies: []llm.Reply{reply}}, Registry: reg, MaxSteps: 2, Session: &recordSink{}, SessionID: "positive"}
			_, _, err := r.Run(context.Background(), "actual positive two-call policy")
			expected := 2
			if n == 3 {
				expected = 0
			}
			if err != nil || len(calls) != expected || r.Outcome.Status != outcome.Stopped {
				t.Fatalf("positive calls=%d want%d outcome=%+v err=%v", len(calls), expected, r.Outcome, err)
			}
		})
	}
}
func TestNoCallCeilingHostCheckAndCancellation(t *testing.T) {
	for _, limit := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			dir := t.TempDir()
			r := &Runner{Prompts: prompts.Builtin(), Model: &scriptModel{replies: []string{"done"}}, Registry: tool.NewRegistry(), MaxSteps: limit, CodingRequired: true, Workspace: dir, CheckCommand: "printf verified > proof", Session: &diskBudgetSink{dir: t.TempDir()}, SessionID: "host-check"}
			r.Registry.Register(r.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, dir)))
			_, _, err := r.Run(context.Background(), "actual final check")
			_, effect := os.Stat(filepath.Join(dir, "proof"))
			if limit == 1 {
				if !os.IsNotExist(effect) || r.Outcome.Status != outcome.Stopped {
					t.Fatal("last-call check was invented", effect, r.Outcome)
				}
			} else {
				if effect != nil || !r.Continuation.Coding.Passed() || r.Outcome.Status != outcome.Completed {
					t.Fatal("check eligibility lost", effect, r.Outcome)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	var calls []string
	reg := tool.NewRegistry()
	def := echoTool(&calls)
	original := def.Execute
	def.Execute = func(ctx context.Context, a tool.Args) (tool.Result, error) {
		res, e := original(ctx, a)
		cancel()
		return res, e
	}
	reg.Register(def)
	r := &Runner{Model: &scriptModel{replies: []string{echoBlock("once"), echoBlock("twice")}}, Registry: reg, MaxSteps: 0}
	_, _, err := r.Run(ctx, "cancel actual unlimited work")
	if !errors.Is(err, context.Canceled) || r.Outcome.Status != outcome.Cancelled || len(calls) != 1 {
		t.Fatal("unlimited ignores cancellation", err, r.Outcome, calls)
	}
}

type diskBudgetSink struct{ dir string }

func (s *diskBudgetSink) Append(_ context.Context, id, kind, text string) error {
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}
func TestNoCallCeilingDiscoveryBudgetIsNotZeroRemaining(t *testing.T) {
	m := &discoveryModel{}
	for i := 0; i < 5; i++ {
		m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
	}
	r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		var p map[string]json.RawMessage
		if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
			t.Fatal(err)
		}
		if string(p["RemainingSteps"]) != "null" || string(p["RemainingSeconds"]) != "null" {
			t.Fatalf("unbounded budget falsely exhausted: %s", q.Context)
		}
		return decision.Verdict{Option: discoveryContinueOption, Confidence: .9}, nil
	}, &recordSink{})
	r.MaxSteps, r.MaxRunSeconds = 0, 0
	_, _, err := r.Run(context.Background(), "same useful task")
	if err != nil || len(m.seen) != 6 {
		t.Fatal("unlimited discovery did not progress", err, len(m.seen))
	}
	first := m.seen[0]
	if len(first) != 2 || first[0].Role != "system" || !strings.Contains(first[0].Content, `"RemainingSteps":null`) || first[1].Role != "user" || first[1].Content != "same useful task" {
		t.Fatal("initial host budget displaced task or is missing from system header")
	}
	saved, err := r.Continuation.Encode()
	if err != nil || strings.Contains(saved, "Initial run budget:") {
		t.Fatal("injected budget retained as user history", err)
	}
	m.seen = nil
	r.MaxSteps, r.MaxRunSeconds = 3, 17
	if _, _, err := r.Run(context.Background(), "explicit resumed request"); err != nil {
		t.Fatal(err)
	}
	first = m.seen[0]
	if first[0].Role != "system" || !strings.Contains(first[0].Content, `"StepLimit":3`) || !strings.Contains(first[0].Content, `"RunLimitSeconds":17`) || first[len(first)-1].Content != "explicit resumed request" {
		t.Fatal("resume did not rebuild actual host budget before the exact user request")
	}
}
func TestLargeCodingReceiptAppendsAndReopens(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("escaped \"line\"\n", 100000)
	r := &Runner{Session: &diskBudgetSink{dir: dir}, SessionID: "large-coding", Continuation: session.Continuation{Coding: &session.CodingState{TaskID: "task", Workspace: "/owned", Receipt: &session.CheckReceipt{Text: text}}}}
	if err := r.codingRecord(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := session.Read(dir, "large-coding")
	if err != nil || len(events) != 1 {
		t.Fatal("supported coding receipt cannot reopen", len(events), err)
	}
	var c session.CodingState
	if json.Unmarshal([]byte(events[0].Text), &c) != nil || c.Receipt.Text != text {
		t.Fatal("coding source altered")
	}
}
