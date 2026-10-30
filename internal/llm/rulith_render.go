package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Some chat templates trim completed assistant content, while the native cache
// retains its generated whitespace. Protect only that content during rendering;
// no marker reaches inference, history or the logical request identity.
func (c *Client) rulithRenderedPrompt(ctx context.Context, request chatRequest) (string, error) {
	identity, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	var stem string
	for salt := 0; ; salt++ {
		stem = fmt.Sprintf("CALIPER_TEMPLATE_CONTENT_%d_", salt)
		if !strings.Contains(string(identity), stem) {
			break
		}
	}
	type span struct{ begin, end, content string }
	var spans []span
	request.Messages = append([]Message(nil), request.Messages...)
	for i, message := range request.Messages {
		if message.Role != "assistant" || len(message.ToolCalls) != 0 || message.Content == strings.TrimSpace(message.Content) {
			continue
		}
		prefix := fmt.Sprintf("%s%d_", stem, i)
		s := span{prefix + "BEGIN", prefix + "END", message.Content}
		spans = append(spans, s)
		request.Messages[i].Content = s.begin + s.content + s.end
	}
	var rendered struct {
		Prompt string `json:"prompt"`
	}
	if err := c.rulithJSON(ctx, "/apply-template", request, &rendered); err != nil {
		return "", err
	}
	for _, s := range spans {
		if strings.Count(rendered.Prompt, s.begin) != 1 || strings.Count(rendered.Prompt, s.end) != 1 {
			return "", fmt.Errorf("Rulith template lost or duplicated preserved assistant content")
		}
		before, rest, _ := strings.Cut(rendered.Prompt, s.begin)
		content, after, found := strings.Cut(rest, s.end)
		if !found || content != s.content {
			return "", fmt.Errorf("Rulith template altered preserved assistant content")
		}
		rendered.Prompt = before + content + after
	}
	if len(spans) > 0 && strings.Contains(rendered.Prompt, stem) {
		return "", fmt.Errorf("Rulith template retained a content marker")
	}
	return rendered.Prompt, nil
}
