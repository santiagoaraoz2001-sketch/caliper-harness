package launch

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
)

// Captured October18 Task A edit receipts. Only the machine-specific workspace
// prefix is normalized; source previews, result truncation and hashes are intact.
// Reviewer/decision answers are controlled; no inference or captured effects run.
func TestCapturedReviewReceivesFileObservations(t *testing.T) {
	raw, err := os.ReadFile("testdata/review-file-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	var transcript []llm.Message
	if err := json.Unmarshal(raw, &transcript); err != nil {
		t.Fatal(err)
	}
	reviewed := false
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		reviewed = true
		request := fixtureRoleRequest(messages)
		for _, fact := range []string{"old_sha256=", "setDirection", "[preview omitted]", "earlier work may be retained", "Unfinished sibling work is not a defect"} {
			if !strings.Contains(request, fact) {
				t.Errorf("review missing recorded fact/boundary %q", fact)
			}
		}
		if strings.Contains(request, "UNEXECUTED_PROPOSAL") {
			t.Error("unexecuted model proposal became review evidence")
		}
		return "PASS: controlled reviewer for evidence wiring only", nil
	})
	d, aid, ids, _ := roleHandoffFixture(t, 1, model)
	d.Prompts = prompts.Builtin()
	if _, err := plan.TransitionApproach(d.Store, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(d.Store, ids[0], plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err := plan.SetTaskAttempts(d.Store, ids[0], 1); err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(d.Store, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	r := resultOf(task)
	r.Outcome.Status = outcome.Completed
	r.Transcript = append(transcript, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "never-ran", Name: "edit", Arguments: `{"new":"UNEXECUTED_PROPOSAL"}`}}})
	results := []Result{r}
	if err := verifyPass(t.Context(), d, results, []int{0}, 0); err != nil {
		t.Fatal(err)
	}
	if !reviewed || !results[0].Qualified() {
		t.Fatalf("review not reached/qualified: %+v", results)
	}
}

func TestReviewFileObservationsPreserveRefusalAndTextMode(t *testing.T) {
	pc := prompts.Builtin()
	messages := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "refused", Name: "edit", Arguments: `{"new":"NEVER_APPLIED"}`}}},
		{Role: "tool", ToolCallID: "refused", Name: "edit", Content: "entire call array refused; no calls ran"},
		{Role: "tool", ToolCallID: "unmatched", Name: "edit", Content: "UNMATCHED"},
		{Role: "assistant", Content: "```tool\ntool: write\npath: file\ncontent: proposal\n```"},
		{Role: "user", Content: "tool result:\nfile changed; sha256=recorded"},
		{Role: "assistant", Content: "```tool\ntool: edit\npath: file\nold: a\nnew: b\n```"},
		{Role: "user", Content: "USER COMMENT: user prose is not a receipt"},
		{Role: "assistant", Content: "```tool\ntool: edit\npath: file\nold: a\nnew: b\n```"},
		{Role: "user", Content: "tool result (truncated):\npartial observed change"},
		{Role: "assistant", Content: "```tool\ntool: write\npath: file\ncontent: proposal\n```"},
		{Role: "user", Content: "tool error: could not persist receipt; partial effect unknown"},
	}
	got := reviewFileObservations(messages, pc)
	if !strings.Contains(got, "no calls ran") || !strings.Contains(got, "sha256=recorded") || !strings.Contains(got, "partial observed change") || !strings.Contains(got, "partial effect unknown") || strings.Contains(got, "NEVER_APPLIED") || strings.Contains(got, "UNMATCHED") || strings.Contains(got, "USER COMMENT") {
		t.Fatal(got)
	}
}
