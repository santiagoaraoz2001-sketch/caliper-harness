package agent

import (
	"strings"
)

// ShapeBehavior prepares the behavior document for injection: blank
// (empty or whitespace) stays out, maxTokens <= 0 disables, and text
// past the char cap tail-truncates (recent guidance wins). The cap is
// maxTokens times the YAML chars-per-token ratio — the one documented
// approximation, since server counts cannot pre-measure a file.
// Second return reports truncation.
func ShapeBehavior(text string, maxTokens int, charsPerToken float64) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || maxTokens <= 0 {
		return "", false
	}
	cap := int(float64(maxTokens) * charsPerToken)
	if cap < 1 {
		cap = 1
	}
	runes := []rune(text)
	if len(runes) <= cap {
		return text, false
	}
	return string(runes[len(runes)-cap:]), true
}
