package hw

import (
	"strings"
	"testing"
)

func TestHardwareMLXSuggestionsAreAdvisory(t *testing.T) {
	l := LimitsFor(Profile{MemTotalBytes: 32 << 30})
	out := l.String()
	for _, want := range []string{"context=65536", "concurrency=4", "mlx-decode=4", "mlx-prompt=2", "advisory", "not applied"} {
		if !strings.Contains(out, want) {
			t.Errorf("hardware readout omitted %q: %s", want, out)
		}
	}
}
