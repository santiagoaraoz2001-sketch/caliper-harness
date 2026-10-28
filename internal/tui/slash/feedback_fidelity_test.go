package slash

import (
	"strings"
	"testing"
)

func TestBtwPayloadPreservesAuthoredWhitespace(t *testing.T) {
	want := "Keep α  →  β; preserve  two  spaces"
	c, args, ok := New().Parse("/btw " + want)
	if !ok || c.Name != "btw" || strings.Join(args, " ") != want {
		t.Fatalf("feedback changed: %q", strings.Join(args, " "))
	}
}
