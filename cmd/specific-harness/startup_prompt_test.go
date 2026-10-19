package main

import "testing"

// Downloads at startup require an explicit yes; every other answer,
// including an empty line, declines.
func TestParseYesRequiresExplicitYes(t *testing.T) {
	for answer, want := range map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, " Yes \n": true,
		"\n": false, "n\n": false, "no\n": false, "yeah\n": false, "": false,
	} {
		if got := parseYes(answer); got != want {
			t.Fatalf("parseYes(%q)=%v want %v", answer, got, want)
		}
	}
}
