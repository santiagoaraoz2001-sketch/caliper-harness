package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

type reviewerAuthorityModel struct {
	t       *testing.T
	tool    string
	reviews int
}

func (m *reviewerAuthorityModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, fmt.Errorf("native fixture must use the native transport")
}

func (m *reviewerAuthorityModel) ChatTools(_ context.Context, messages []llm.Message, definitions []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	reply := llm.Reply{Message: llm.Message{Role: "assistant", Content: "worker result"}, FinishReason: "stop"}
	if !strings.HasPrefix(fixtureRoleRequest(messages), "Review ") {
		return reply, nil
	}
	m.reviews++
	hasCheck := false
	for _, def := range definitions {
		if def.Function.Name == "bash" {
			m.t.Fatal("reviewer advertised free shell")
		}
		hasCheck = hasCheck || def.Function.Name == "check"
	}
	if !hasCheck {
		m.t.Fatal("frozen check unavailable")
	}
	if m.reviews == 1 {
		args := `{"cmd":"printf MUTATED > source.js"}`
		if m.tool == "check" {
			args = `{"cmd":"printf MUTATED > source.js","acceptance_cmd":"printf FIXTURE_ACCEPTANCE"}`
		}
		reply.Message.Content = ""
		reply.Message.ToolCalls = []llm.ToolCall{{ID: "forged-review-action", Name: m.tool, Arguments: args}}
		reply.FinishReason = "tool_calls"
	} else {
		reply.Message.Content = "UNVERIFIED: additional evidence unavailable; refused command did not execute"
	}
	return reply, nil
}

func TestReviewerNativeForgedShellAndCheckHaveNoEffects(t *testing.T) {
	for _, name := range []string{"bash", "check"} {
		t.Run(name, func(t *testing.T) {
			model := &reviewerAuthorityModel{t: t, tool: name}
			d, aid, _, root := roleHandoffFixture(t, 1, model)
			path := filepath.Join(root, "source.js")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			results, err := Launch(t.Context(), d, aid)
			content, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || string(content) != "original" || len(results) != 1 || results[0].Qualified() || model.reviews != 2 {
				t.Fatalf("native authority effect=%q reviews=%d err=%v/%v", content, model.reviews, err, readErr)
			}
			if !strings.Contains(results[0].AttemptLog, "forged-review-action") {
				t.Fatal("actual refused native call lost from evidence")
			}
		})
	}
}
