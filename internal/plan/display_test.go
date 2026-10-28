package plan

import (
	"strings"
	"testing"
)

func TestDisplayStructuredRetainsAllFieldsWithoutJSONEscapes(t *testing.T) {
	raw := `{"goal":"First\nSecond","unknown_future":{"items":["alpha","β🙂"]},"count":9007199254740993}`
	got := DisplayStructured(raw)
	for _, part := range []string{"goal: First\nSecond", "unknown_future:", "items:", "alpha", "β🙂", "9007199254740993"} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in %q", part, got)
		}
	}
	if strings.Contains(got, `\n`) || strings.Contains(got, `\"`) {
		t.Fatal(got)
	}
	if DisplayStructured("legacy paragraph\nline two") != "legacy paragraph\nline two" {
		t.Fatal("legacy prose changed")
	}
	if DisplayText("safe\x1b[31m") != "safe\\u001b[31m" {
		t.Fatal("terminal control remained active")
	}
}
