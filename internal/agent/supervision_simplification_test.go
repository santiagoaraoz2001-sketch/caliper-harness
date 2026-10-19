package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestGuidedDraftDoesNotAskJudgeToRepeatNextQuestion(t *testing.T) {
	for _, mode := range []string{"draft", "final", "changed question", "comment", "failed draft", "claimed work", "failed claimed work"} {
		t.Run(mode, func(t *testing.T) {
			question := "DRAFT METHOD: how will you implement this step?"
			r := &Runner{Prompts: prompts.Builtin(), Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "draft"}}, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return BookkeepingFacts{Preparation: question}, nil }}}
			r.Continuation.Add("intent", "user", "Implement the requested behavior", false)
			execute := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				raw, _ := json.Marshal(map[string]any{"data": map[string]string{"id": "draft"}, "next_question": question})
				return tool.Result{Text: string(raw), Failed: mode == "failed draft" || mode == "failed claimed work"}, nil
			})
			if _, err := execute(t.Context(), tool.Definition{Name: "graphql", Label: "plan"}, tool.Args{"query": "mutation { saved }"}); err != nil {
				t.Fatal(err)
			}
			if mode == "claimed work" || mode == "failed claimed work" {
				r.assignedClaim = true
			}
			if mode == "changed question" {
				question = "Resolve the newly pending revision"
			}
			if mode == "comment" {
				r.Continuation.Add("note", "user", "USER COMMENT: revise the target", false)
			}
			calls := 0
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				calls++
				return decision.Verdict{Option: q.Options[0], Confidence: .9}, nil
			}
			if _, err := r.enforce(t.Context(), mode == "final", "", func(string, string) error { return nil }); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "draft" || mode == "claimed work" {
				want = 0
			}
			if calls != want {
				t.Fatalf("judge calls=%d want=%d", calls, want)
			}
		})
	}
}

func TestProgressDedupRetainsEvidenceAndOwnerContext(t *testing.T) {
	r, w := largeProgressPacket(t)
	// One actual call appears in multiple views, including a failed receipt.
	r.enforcement.latest = r.enforcement.current
	w.recentReceipts[0].Failed = true
	w.recentReceipts[0].Error = "actual failure must remain visible"
	before, _ := json.Marshal([]any{r.enforcement.current, w.recentReceipts, w.recentReplies, r.Continuation})
	p, err := r.progressEvidence(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(p)
	p.deduplicateEvidence()
	compact, _ := json.Marshal(p)
	if len(compact) >= len(original) || !strings.Contains(string(compact), "same call arguments as") {
		t.Fatal("duplicate payload not reduced with provenance")
	}
	if p.Latest.Args["content"] != r.enforcement.current.Args["content"] || p.Latest.CallID != "large-write" || !p.RecentReceipts[0].Failed || p.RecentReceipts[0].Error != "actual failure must remain visible" {
		t.Fatal("current body or failure evidence changed")
	}
	if !strings.Contains(string(compact), "Yes: silent game.") {
		t.Fatal("user answer lost")
	}
	after, _ := json.Marshal([]any{r.enforcement.current, w.recentReceipts, w.recentReplies, r.Continuation})
	if string(before) != string(after) {
		t.Fatal("judge projection mutated owner context")
	}
	// Equal arguments from different calls are not the same event.
	p = progressPacket{Latest: &loopObservation{CallID: "first", Args: tool.Args{"content": strings.Repeat("a", 2000)}}, RecentCompletedReplies: []llm.Message{{ToolCalls: []llm.ToolCall{{ID: "second", Arguments: `{"content":"` + strings.Repeat("a", 2000) + `"}`}}}}}
	p.deduplicateEvidence()
	if p.RecentCompletedReplies[0].ToolCalls[0].Arguments == "" {
		t.Fatal("distinct call conflated")
	}
	call := &p.RecentCompletedReplies[0].ToolCalls[0]
	call.ID = "first"
	call.Arguments = `{"content":"discarded","content":"` + strings.Repeat("a", 2000) + `"}`
	malformed := call.Arguments
	p.deduplicateEvidence()
	if call.Arguments != malformed {
		t.Fatal("malformed duplicate-key evidence normalized away")
	}
}

// Opt-in replay reads saved requests only: no server, judge or running store.
func TestCapturedProgressDedupReplay(t *testing.T) {
	dir := os.Getenv("CALIPER_PROGRESS_REPLAY_DIR")
	if dir == "" {
		t.Skip("captured request directory not supplied")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.request.json"))
	if err != nil {
		t.Fatal(err)
	}
	count, reduced, before, after := 0, 0, 0, 0
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var q decision.Request
		if json.Unmarshal(raw, &q) != nil || q.Context == "" {
			continue
		}
		var p progressPacket
		if json.Unmarshal([]byte(q.Context), &p) != nil || p.Checkpoint.Phase == "" {
			continue
		}
		a, _ := json.Marshal(p)
		p.deduplicateEvidence()
		b, _ := json.Marshal(p)
		if len(b) > len(a) {
			t.Fatalf("dedup enlarged %s", file)
		}
		if len(b) < len(a) {
			reduced++
		}
		count++
		before += len(a)
		after += len(b)
	}
	if count == 0 || reduced == 0 {
		t.Fatal("no useful captured replay")
	}
	t.Logf("captured packets=%d reduced=%d original_bytes=%d deduplicated_bytes=%d", count, reduced, before, after)
}

func TestClaimReceiptDoesNotDuplicateFrozenSystemBrief(t *testing.T) {
	brief := map[string]string{"title": "task", "prompt": "unique frozen objective", "current": "old planning observations", "target": "unique target", "sketch": "unique steps", "acceptance": "unique acceptance", "tests": "unique tests", "verify": "unique reviewer requirements", "check_command": "suite", "acceptance_command": "behavior"}
	r := &Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry(), Session: &recordSink{}, SessionID: "test", Workspace: "/project", Assigned: &AssignedOps{ClaimedBrief: func() map[string]string { return brief }, Start: func(context.Context, string) (*session.Assignment, string, error) {
		return &session.Assignment{Store: "owner", Workspace: "/project", PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 1}, "suite", nil
	}}}
	r.Outcome.Status = outcome.Unknown
	r.begin("implement the requested task")
	receipt, err := r.TaskStartDefinition().Execute(t.Context(), tool.Args{"task": "task"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt.Text, "claimed task task attempt1") || strings.Contains(receipt.Text, brief["prompt"]) {
		t.Fatal("claim receipt lost identity or repeated the brief", receipt.Text)
	}
	system := r.basePrompt()
	for _, key := range []string{"prompt", "current", "target", "sketch", "acceptance", "tests", "verify"} {
		if strings.Count(system, brief[key]) != 1 {
			t.Fatalf("frozen %s missing or repeated", key)
		}
	}
	if !strings.Contains(system, "recorded during planning (historical;") {
		t.Fatal("planning observations presented as current facts")
	}
}
