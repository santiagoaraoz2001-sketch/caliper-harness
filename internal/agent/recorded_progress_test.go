package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Replays exact recorded native calls/results through the production observer,
// novelty counter and packet builder. It does not replay effects or infer again.
func TestRecordedSnakeProgressSequence(t *testing.T) {
	raw, err := os.ReadFile("testdata/snake-progress-events.json")
	if err != nil {
		t.Fatal(err)
	}
	var events []session.Event
	if err = json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	pc := prompts.Builtin()
	facts := BookkeepingFacts{Disclosure: "Recorded successful createPlan receipt; not a fresh graph read. Historical run step and elapsed time are unavailable; zero values are replay placeholders, not observed historical timing. No task execution or verification is claimed.", Preparation: pc.Text("preparation.guided_next"), Checkpoint: ProgressCheckpoint{Phase: "PREPARE", MissingFact: "unknown"}}
	r := &Runner{Prompts: pc, Threshold: .7, LoopStall: 5, LoopRepeat: 3, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	baseline, err := os.ReadFile("../../verification/2026-10-13-model-interface-progress/native-controls/BASELINE-PACKET.json")
	if err == nil {
		var p struct{ PendingRepair string }
		if err = json.Unmarshal(baseline, &p); err != nil {
			t.Fatal(err)
		}
		r.enforcement.repair = p.PendingRepair
	} else {
		r.enforcement.repair = "Recorded pending PAT update remains required; no task is claimed."
	}
	var w watcher
	calls := map[string]struct {
		Name string
		Args tool.Args
	}{}
	answered, observations := 0, 0
	for _, event := range events {
		if event.Kind == "prompt" {
			r.Continuation.Add("intent", "user", event.Text, false)
			continue
		}
		var e session.Entry
		if err = json.Unmarshal([]byte(event.Text), &e); err != nil {
			t.Fatal(err)
		}
		if event.Kind == "native_reply" {
			w.completedDiscovery(e.Message, 5)
			for _, call := range e.Message.ToolCalls {
				var a tool.Args
				if err = json.Unmarshal([]byte(call.Arguments), &a); err != nil {
					t.Fatal(err)
				}
				calls[call.ID] = struct {
					Name string
					Args tool.Args
				}{call.Name, a}
			}
			continue
		}
		if e.Message.Name == "plan" {
			var receipt struct{ Data json.RawMessage }
			text := strings.TrimPrefix(e.Message.Content, "tool result:\n")
			if err = json.Unmarshal([]byte(text), &receipt); err != nil {
				t.Fatal(err)
			}
			facts.Unfinished = receipt.Data
			continue
		}
		call, ok := calls[e.Message.ToolCallID]
		if !ok {
			t.Fatal("missing actual native call", e.Message.ToolCallID)
		}
		content := strings.TrimPrefix(e.Message.Content, "tool result:\n")
		var actualError error
		failed := e.Kind == "failure"
		if failed {
			message, body, _ := strings.Cut(strings.TrimPrefix(e.Message.Content, "tool error: "), "\n\npartial tool result (failed; not complete):\n")
			actualError = errors.New(message)
			content = body
		}
		execute := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
			return tool.Result{Text: content, Failed: failed}, actualError
		})
		_, _ = execute(t.Context(), tool.Definition{Name: call.Name}, call.Args)
		note := r.enforcement.current
		note.CallID, note.ObservedAt = e.Message.ToolCallID, event.Time
		if call.Name == "workflow_focus" {
			id := call.Args["ref"].(string)
			r.Continuation.Workflow = &session.WorkflowScope{Store: "recorded-owner", Kind: "plan", ID: id}
			facts.Checkpoint.PATStep, facts.Checkpoint.SelectedID = "plan-draft", id
		}
		productive := r.bookkeepingProgress(t.Context(), !failed)
		if call.Name == "ask" {
			answered++
			if !productive {
				t.Error("successful actual answer was not progress", note.CallID)
			}
		}
		if trip := w.observe(callSignature(call.Name, call.Args), productive, 3, 5); trip != "" {
			t.Error("actual five-call Snake sequence incorrectly reached a stall", trip)
		}
		w.discoveryReceipt(note, 5)
		observations++
	}
	if answered != 3 || observations != 5 || w.nonprod != 1 || r.enforcement.repair == "" || r.assignedClaim {
		t.Fatalf("recorded facts lost: answers=%d observations=%d nonproductive=%d", answered, observations, w.nonprod)
	}
	r.progressWatch = &w
	captured := decision.Request{}
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		captured = q
		return decision.Verdict{Option: q.Options[2], Confidence: .99}, nil
	}
	_, err = r.confirmLoop(t.Context(), &w, "cadence review of five recorded attempts; inspect the actual results and next legal action", func(string, string) error { return nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		RecentReceipts []struct{ CallID, OutputText, Error string }
		Checkpoint     ProgressCheckpoint
	}
	if err = json.Unmarshal([]byte(captured.Context), &packet); err != nil {
		t.Fatal(err)
	}
	if packet.Checkpoint.Phase != "PREPARE" || packet.Checkpoint.PATStep != "plan-draft" || len(packet.RecentReceipts) != 5 {
		t.Fatal("actual scope/results omitted")
	}
	for _, want := range []string{"20×20", "Dark modern", "Progressive speed-up", "appendKnowledge", "putKnowledge"} {
		if !strings.Contains(captured.Context, want) {
			t.Error("actual answer/error missing", want)
		}
	}
	for _, receipt := range packet.RecentReceipts {
		if receipt.CallID == "" {
			t.Error("receipt lost native correlation")
		}
	}
	if strings.Contains(captured.Context, "no observed progress") || strings.Contains(strings.Join(captured.Options, " "), "should stop") {
		t.Fatal("a cadence became a diagnosis or stop option")
	}
	if dir := os.Getenv("SPECIFIC_PROGRESS_CAPTURE"); dir != "" {
		body, err := json.MarshalIndent(captured, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "CURRENT-REQUEST.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
