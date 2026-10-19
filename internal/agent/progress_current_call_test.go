package agent

import (
	"context"
	"encoding/json"
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

// This opt-in writes the exact component Judge input for a later independently
// run native D1 control. Saving a packet is not model-inference evidence.
func saveInterfaceProgressInput(t *testing.T, name string, req decision.Request) {
	t.Helper()
	if dir := os.Getenv("SPECIFIC_INTERFACE_PROMPT_CAPTURE"); dir != "" {
		raw, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// Real Runner/native-array refusal and settlement, scripted complete model and
// advisory verdicts. No model service or inference is involved.
func TestInterfaceProgressRefusedCallGroundingAndSilence(t *testing.T) {
	for _, direction := range []struct {
		name       string
		pick       int
		confidence float64
		failure    error
		guidance   bool
	}{
		{"continue", 0, .99, nil, false},
		{"uncertain-repair", 1, .499, nil, false},
		{"repair", 1, .5, nil, true},
		{"reconsider", 2, .5, nil, true},
		{"advice-unavailable", 1, .99, context.DeadlineExceeded, false},
	} {
		t.Run(direction.name, func(t *testing.T) {
			m := &discoveryModel{}
			originalThought := "Literal completed reasoning before the refused plan call; preserve it unchanged."
			for i := 0; i < 5; i++ {
				m.replies = append(m.replies, llm.Reply{Message: llm.Message{Role: "assistant", Reasoning: originalThought, ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("refused-%d", i), Name: "plan", Arguments: `{"more":true}`}}}, FinishReason: "tool_calls"})
			}
			m.replies = append(m.replies, llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "valid-entry", Name: "plan", Arguments: `{}`}}}, FinishReason: "tool_calls"})
			judgments, actualEntries := 0, 0
			sink := &recordSink{}
			r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				// Named JSON fields also compile against old source for the RED
				// control; absence fails behavior rather than compilation.
				var packet struct {
					Checkpoint                          ProgressCheckpoint
					PreflightRefusal, CurrentToolSchema string
					RecentReceipts                      []loopObservation
					Assigned, CurrentClaim              bool
				}
				if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
					t.Fatal(err)
				}
				if len(packet.RecentReceipts) != 0 || !strings.Contains(packet.PreflightRefusal, `unsupported argument "more"`) || !strings.Contains(packet.CurrentToolSchema, "Call plan with {}") || !strings.Contains(packet.CurrentToolSchema, "\n{}\n") {
					t.Fatalf("zero-receipt refusal lacks its actual current legal call: %s", q.Context)
				}
				if strings.Contains(packet.Checkpoint.NextAction, "EXECUTE") || strings.Contains(packet.CurrentToolSchema, "more=true") || packet.Assigned || packet.CurrentClaim {
					t.Fatalf("refusal was replaced by future execution/claim advice: %+v", packet)
				}
				if len(q.Options) != 3 || q.Options[0] != "continue productive work" || q.Options[1] != "repair the latest rejected call" || q.Options[2] != "reconsider the unsuccessful approach" {
					t.Fatal("directions are not the three approved choices", q.Options)
				}
				saveInterfaceProgressInput(t, "blocked-"+direction.name, q)
				return decision.Verdict{Option: q.Options[direction.pick], Confidence: direction.confidence}, direction.failure
			}, sink)
			r.Prompts = prompts.Builtin()
			r.Registry.Register(tool.Definition{Name: "plan", Desc: r.Prompts.Text("tools.plan.description"), Purpose: r.Prompts.Text("planning.entry"), Execute: func(context.Context, tool.Args) (tool.Result, error) {
				actualEntries++
				return tool.Result{Text: "first current question returned; no task claim or coding effect"}, nil
			}})
			if _, _, err := r.Run(t.Context(), "implement the same requested change; keep files and actual failures"); err != nil {
				t.Fatal(err)
			}
			if judgments != 1 || actualEntries != 1 || r.assignedClaim || r.Outcome.Status != outcome.Completed || len(m.seen) != 7 {
				t.Fatalf("advice performed or stopped work: judges=%d entries=%d claim=%t outcome=%+v requests=%d", judgments, actualEntries, r.assignedClaim, r.Outcome, len(m.seen))
			}
			guidance := ""
			for i, kind := range sink.kinds {
				if kind == "guidance" {
					guidance = sink.texts[i]
				}
			}
			if (guidance != "") != direction.guidance {
				t.Fatal("Continue, confidence floor or advice failure changed silence", guidance)
			}
			if direction.guidance && (!strings.Contains(guidance, `unsupported argument "more"`) || !strings.Contains(guidance, "\n{}\n") || strings.Contains(guidance, `invoke="EXECUTE"`)) {
				t.Fatal("delivered nudge lacks the exact current refusal/call", guidance)
			}
			if direction.name == "reconsider" && (!strings.Contains(guidance, "specific missing fact") || !strings.Contains(guidance, "different grounded method")) {
				t.Fatal("reconsider delivered another execution demand", guidance)
			}
			refused, unchangedThought := 0, false
			for _, entry := range r.Continuation.Entries {
				for _, call := range entry.Message.ToolCalls {
					if strings.HasPrefix(call.ID, "refused-") {
						refused++
						if call.Name != "plan" || call.Arguments != `{"more":true}` || entry.Message.Reasoning != originalThought {
							t.Fatal("progress changed original calls or reasoning", entry)
						}
					}
				}
				unchangedThought = unchangedThought || entry.Message.Reasoning == originalThought
			}
			if refused != 5 || !unchangedThought {
				t.Fatal("raw completed thought/call provenance disappeared", refused)
			}
		})
	}
}

func TestInterfaceProgressProductiveInspectionControl(t *testing.T) {
	m := &discoveryModel{}
	for i := 0; i < 5; i++ {
		m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
	}
	sink, judgments := &recordSink{}, 0
	r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		var packet struct {
			PreflightRefusal, CurrentToolSchema string
			LatestSourceText                    string
		}
		if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if packet.PreflightRefusal != "" || packet.CurrentToolSchema != "" || !strings.Contains(packet.LatestSourceText, "actual source 4") {
			t.Fatalf("productive reads became a rejected-call control: %s", q.Context)
		}
		saveInterfaceProgressInput(t, "productive-inspection", q)
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}, sink)
	if _, _, err := r.Run(t.Context(), "inspect the responsible source for the requested change"); err != nil || judgments != 1 {
		t.Fatalf("productive control failed: %v judgments=%d", err, judgments)
	}
	for _, kind := range sink.kinds {
		if kind == "guidance" {
			t.Fatal("Continue sent a message after productive inspection")
		}
	}
}

func TestInterfaceProgressNewerSuccessClearsOlderFailure(t *testing.T) {
	r, w := largeProgressPacket(t)
	r.Registry = tool.NewRegistry(r.Prompts)
	r.Registry.Register(tool.Definition{Name: "read", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}}})
	w.completedDiscovery(llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "old-failure", Name: "read", Arguments: `{"path":"absent"}`}}}, 5)
	w.discoveryReceipt(&observed{Tool: "read", CallID: "old-failure", Result: tool.Result{Failed: true, Text: "actual earlier missing file"}}, 5)
	w.completedDiscovery(llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "new-success", Name: " READ ", Arguments: `{"path":"present"}`}}}, 5)
	// A current successful settled result is authoritative even when an older
	// watcher receipt remains last; its normalized spelling cannot revive it.
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "new-success", Name: "read", Content: "actual current successful read"}})
	p, err := r.progressEvidence(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	var current struct{ PreflightRefusal, CurrentToolSchema string }
	_ = json.Unmarshal(raw, &current)
	if current.PreflightRefusal != "" || current.CurrentToolSchema != "" {
		t.Fatalf("old failure replaced a newer success: %s", raw)
	}
}

func TestInterfaceProgressWholeArrayRefusalRetainsSubmittedSchemas(t *testing.T) {
	m := &discoveryModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "bad-bash", Name: "bash", Arguments: `{"cmd":"printf must-not-run","timeout":"bad"}`},
		{ID: "valid-read", Name: "read", Arguments: `{"path":"0"}`},
	}}, FinishReason: "tool_calls"}}}
	judgments := 0
	r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		var packet struct{ PreflightRefusal, CurrentToolSchema string }
		if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(packet.PreflightRefusal, "timeout") || !strings.Contains(packet.CurrentToolSchema, "tool: bash") || !strings.Contains(packet.CurrentToolSchema, "tool: read") {
			t.Fatalf("whole-array rejection was assigned only to its final member: %s", q.Context)
		}
		saveInterfaceProgressInput(t, "mixed-array-refusal", q)
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}, &recordSink{})
	r.LoopStall = 1
	r.Registry.Register(bash.NewAt(config.Defaults().Tools.Bash, r.Workspace))
	if _, _, err := r.Run(t.Context(), "inspect source without executing invalid calls"); err != nil || judgments != 1 {
		t.Fatalf("mixed-array control failed: %v judgments=%d", err, judgments)
	}
	if r.enforcement.current != nil {
		t.Fatal("invalid array executed a member")
	}
}
