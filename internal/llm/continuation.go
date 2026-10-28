package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ResponsePrefix retains one unfinished native response. MLX replays sampled
// tokens; Rulith preserves rendered prompt and response bytes for retokenization.
// Neither strategy inserts a new message or rebuilds partial tool arguments.
type ResponsePrefix struct {
	// Empty Strategy is the existing MLX token-prefix contract. RulithRaw
	// retains rendered bytes; its server retokenizes them. It is deliberately
	// not represented as replay of the originally sampled token IDs.
	Strategy         string   `json:"strategy,omitempty"`
	Prompt           string   `json:"prompt,omitempty"`
	Raw              string   `json:"raw,omitempty"`
	RequestSHA256    string   `json:"request_sha256,omitempty"`
	GeneratedTokens  int      `json:"generated_tokens,omitempty"`
	Tokens           []int    `json:"tokens"`
	RawChunks        []string `json:"raw_chunks"`
	InitialState     string   `json:"initial_state"`
	PromptSHA256     string   `json:"prompt_sha256"`
	ToolIndex        int      `json:"tool_index"`
	PendingToolDraft string   `json:"pending_tool_draft,omitempty"`
}

const RulithRawPrefix = "rulith_raw"

// ResponseBytes counts only the unfinished response, not the original prompt.
func (p *ResponsePrefix) ResponseBytes() int {
	if p == nil {
		return 0
	}
	if p.Strategy == RulithRawPrefix {
		return len(p.Raw)
	}
	n := 0
	for _, s := range p.RawChunks {
		n += len(s)
	}
	return n
}

func (p *ResponsePrefix) GeneratedTokenCount() int {
	if p == nil {
		return 0
	}
	if p.Strategy == RulithRawPrefix {
		return p.GeneratedTokens
	}
	return len(p.Tokens)
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
	if p != nil && p.Strategy == RulithRawPrefix {
		if p.Prompt == "" || p.Raw == "" || !utf8.ValidString(p.Prompt) || !utf8.ValidString(p.Raw) || p.GeneratedTokens <= 0 ||
			(p.InitialState != "normal" && p.InitialState != "reasoning") || len(p.Tokens) != 0 || len(p.RawChunks) != 0 {
			return fmt.Errorf("invalid Rulith unfinished-response byte prefix")
		}
		digest := sha256.Sum256([]byte(p.Prompt))
		request, err := hex.DecodeString(p.RequestSHA256)
		if hex.EncodeToString(digest[:]) != p.PromptSHA256 || err != nil || len(request) != sha256.Size {
			return fmt.Errorf("Rulith unfinished response has no valid original prompt/request identity")
		}
		return nil
	}
	if p != nil && p.Strategy != "" {
		return fmt.Errorf("unknown unfinished-response strategy %q", p.Strategy)
	}
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
	if previous != nil && next != nil && previous.Strategy == RulithRawPrefix && next.Strategy == RulithRawPrefix {
		return previous.Validate() == nil && next.Validate() == nil && previous.Prompt == next.Prompt &&
			previous.PromptSHA256 == next.PromptSHA256 && previous.RequestSHA256 == next.RequestSHA256 &&
			previous.InitialState == next.InitialState && next.GeneratedTokens > previous.GeneratedTokens &&
			len(next.Raw) > len(previous.Raw) && strings.HasPrefix(next.Raw, previous.Raw)
	}
	if previous != nil && next != nil && previous.Strategy != next.Strategy {
		return false
	}
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
// next physical request continues its declared native prefix strategy.
func WithResponseContinuation(ctx context.Context, previous Reply) context.Context {
	previous.Message = previous.Message.Clone()
	previous.Prefix = previous.Prefix.Clone()
	return context.WithValue(ctx, responseContinuationKey{}, previous)
}
