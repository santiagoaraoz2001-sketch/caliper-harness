package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unknown keys refuse loudly at load instead of silently dropping:
// the same refuse-all contract as the settings form, extended to
// hand-edited YAML. Typo'd keys must never pass quiet.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.yaml")
	if err := os.WriteFile(p, []byte("model:\n  name: m\nfuture_knob: 42\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(p)
	if err == nil {
		t.Fatal("unknown key must refuse, not load")
	}
	if !strings.Contains(err.Error(), "future_knob") {
		t.Errorf("refusal must name the key: %v", err)
	}
}
