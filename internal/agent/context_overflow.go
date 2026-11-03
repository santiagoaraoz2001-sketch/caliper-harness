package agent

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	promptTokensField  = regexp.MustCompile(`"n_prompt_tokens":\s*(\d+)`)
	requestTokensParen = regexp.MustCompile(`request \((\d+) tokens\)`)
)

// contextOverflow reports whether a provider error says the request itself
// exceeded the model's context window, plus the provider-measured prompt
// token count when the error carries one. This is the one generation
// failure compaction can cure: nothing was generated or executed, so the
// step may retry after an ordinary judged compaction. Classification is
// textual because providers report this refusal only in error bodies.
func contextOverflow(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	text := err.Error()
	lower := strings.ToLower(text)
	hit := strings.Contains(lower, "exceed_context_size") ||
		strings.Contains(lower, "exceeds the available context") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "context length exceeded") ||
		strings.Contains(lower, "context size") && strings.Contains(lower, "exceed")
	if !hit {
		return 0, false
	}
	for _, re := range []*regexp.Regexp{promptTokensField, requestTokensParen} {
		if m := re.FindStringSubmatch(text); m != nil {
			if n, convErr := strconv.Atoi(m[1]); convErr == nil {
				return n, true
			}
		}
	}
	return 0, true
}
