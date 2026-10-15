package llm

import (
	"context"
	"encoding/hex"
	"fmt"
)

// ResponsePrefix is the owned MLX unfinished response contract. Tokens are appended
// directly to the original rendered prompt. Raw chunks restore the native parser,
// including a pending control marker or tool argument; they are never a user prompt.
type ResponsePrefix struct {
	Tokens           []int    `json:"tokens"`
	RawChunks        []string `json:"raw_chunks"`
	InitialState     string   `json:"initial_state"`
	PromptSHA256     string   `json:"prompt_sha256"`
	ToolIndex        int      `json:"tool_index"`
	PendingToolDraft string   `json:"pending_tool_draft,omitempty"`
}

func (p *ResponsePrefix) Clone() *ResponsePrefix {
	if p == nil {
		return nil
	}
	v := *p
	v.Tokens = append([]int(nil), p.Tokens...)
	v.RawChunks = append([]string(nil), p.RawChunks...)
	return &v
}

func (p *ResponsePrefix) Validate() error {
	if p == nil || len(p.Tokens) == 0 || len(p.RawChunks) != len(p.Tokens) || p.ToolIndex < 0 || (p.InitialState != "normal" && p.InitialState != "reasoning") {
		return fmt.Errorf("invalid native unfinished-response prefix shape")
	}
	digest, err := hex.DecodeString(p.PromptSHA256)
	if err != nil || len(digest) != 32 {
		return fmt.Errorf("unfinished-response prefix has no original prompt identity")
	}
	for _, token := range p.Tokens {
		if token < 0 {
			return fmt.Errorf("unfinished-response prefix has an invalid token")
		}
	}
	return nil
}

type responseContinuationKey struct{}

func extendsPrefix(previous, next *ResponsePrefix) bool {
	if previous == nil || next == nil || next.InitialState != previous.InitialState || next.PromptSHA256 != previous.PromptSHA256 || next.ToolIndex < previous.ToolIndex || len(next.Tokens) <= len(previous.Tokens) {
		return false
	}
	for index, token := range previous.Tokens {
		if next.Tokens[index] != token || next.RawChunks[index] != previous.RawChunks[index] {
			return false
		}
	}
	return true
}

// WithResponseContinuation keeps the same response fields and call IDs while the
// next physical request continues only its runtime-issued literal token prefix.
func WithResponseContinuation(ctx context.Context, previous Reply) context.Context {
	previous.Message = previous.Message.Clone()
	previous.Prefix = previous.Prefix.Clone()
	return context.WithValue(ctx, responseContinuationKey{}, previous)
}
