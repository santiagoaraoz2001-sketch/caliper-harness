package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadinessDefaultAndExplicitLegacyPolicy(t *testing.T) {
	if got := Defaults().Decision.CoherenceMaxChars; got != 0 {
		t.Fatalf("default adds %d-byte admission before the native tokenizer", got)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, value := range []int{0, 3000} {
		text := "decision:\n  coherence_max_chars: 0\n"
		if value > 0 {
			text = "decision:\n  coherence_max_chars: 3000\n"
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, _, err := Load(path)
		if err != nil || cfg.Decision.CoherenceMaxChars != value {
			t.Fatalf("explicit policy %d: %d %v", value, cfg.Decision.CoherenceMaxChars, err)
		}
	}
}
