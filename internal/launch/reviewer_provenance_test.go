package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

type reviewerProvenanceModel struct {
	t               *testing.T
	workspace, task string
	reviews         int
}

func (m *reviewerProvenanceModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, fmt.Errorf("expected native transport")
}
func (m *reviewerProvenanceModel) ChatTools(_ context.Context, msgs []llm.Message, defs []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	text := "EVIDENCE_MISSING: I ran the acceptance command from the parent directory and it could not find its file."
	request := fixtureRoleRequest(msgs)
	if strings.HasPrefix(request, "Review ") {
		m.reviews++
		if !strings.Contains(request, "workspace: "+m.workspace+" (relative paths resolve here)") {
			m.t.Errorf("packet workspace differs from bound check: %s", request)
		}
		prefix := "current typed check facts: "
		_, tail, ok := strings.Cut(request, prefix)
		if !ok {
			m.t.Fatal("typed check facts omitted")
		}
		raw, _, _ := strings.Cut(tail, "\n")
		var c session.CodingState
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			m.t.Fatal(err)
		}
		if c.TaskID != m.task || c.Attempt != 1 || c.Workspace != m.workspace || !c.Passed() || c.AcceptanceCommand != "printf FIXTURE_ACCEPTANCE" || c.Command != "printf FIXTURE_SUITE" {
			m.t.Fatalf("wrong current facts: %s", raw)
		}
		if !strings.Contains(request, "worker report (unverified): "+text) {
			m.t.Fatal("authored wrong-CWD allegation promoted or lost")
		}
		for _, required := range []string{"Distinguish inherited worker claims from your own observations", "a probe from another directory does not establish failure of the pinned check"} {
			if !strings.Contains(request, required) {
				m.t.Errorf("missing attribution instruction %q", required)
			}
		}
		check := false
		for _, d := range defs {
			if d.Function.Name == "bash" {
				m.t.Fatal("reviewer gained free shell")
			}
			check = check || d.Function.Name == "check"
		}
		if !check {
			m.t.Fatal("frozen check absent")
		}
		text = "UNVERIFIED: controlled reviewer observes receipt attribution only; no semantic qualification claimed."
	}
	return llm.Reply{Message: llm.Message{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
}
func TestReviewerNativePacketSeparatesInheritedClaimAndPinnedChecks(t *testing.T) {
	model := &reviewerProvenanceModel{t: t}
	d, aid, ids, root := roleHandoffFixture(t, 1, model)
	d.Prompts = prompts.Builtin()
	d.TemplatesDir = ""
	model.workspace, model.task = root, ids[0]
	results, err := Launch(t.Context(), d, aid)
	if err != nil || len(results) != 1 || model.reviews != 1 || results[0].Qualified() {
		t.Fatalf("review attribution control reviews%d results%+v err%v", model.reviews, results, err)
	}
}
