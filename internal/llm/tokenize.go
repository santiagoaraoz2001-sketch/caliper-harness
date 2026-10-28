package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrTokenCountingUnsupported marks an intentional adapter capability gap.
// Native transport, response and cancellation failures never use this marker.
var ErrTokenCountingUnsupported = errors.New("native token counting unsupported")

// CountTokens uses the selected model's native tokenizer without generation.
// A generic completion endpoint cannot attest a tokenizer or a token budget.
func (c *Client) CountTokens(ctx context.Context, text string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch c.runtime {
	case "", RuntimeOpenAI, RuntimeMLX, RuntimeOllama:
		return 0, fmt.Errorf("%w for runtime %q", ErrTokenCountingUnsupported, c.runtime)
	case RuntimeRulith:
	default:
		return 0, fmt.Errorf("domain token count unavailable for unknown runtime %q", c.runtime)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result struct {
		Tokens []*int64 `json:"tokens"`
	}
	if err := c.rulithJSON(ctx, "/tokenize", map[string]any{"content": text, "add_special": false}, &result); err != nil {
		return 0, err
	}
	if result.Tokens == nil || (text != "" && len(result.Tokens) == 0) {
		return 0, fmt.Errorf("native tokenizer returned no token IDs")
	}
	for _, id := range result.Tokens {
		if id == nil || *id < 0 {
			return 0, fmt.Errorf("native tokenizer returned an invalid token ID")
		}
	}
	return len(result.Tokens), nil
}
