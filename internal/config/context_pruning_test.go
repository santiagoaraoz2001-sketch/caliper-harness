package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOutputPruningYAMLPolicyAndLegacyCompatibility(t *testing.T) {
	for _, age := range []string{"50000", "0", "120000"} {
		t.Run(age, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := "context:\n  output_prune_after_tokens: " + age + "\n  inflight_outputs: 20\nschemas:\n  inflight_cap: 5\n"
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, used, err := Load(path)
			if err != nil || !used {
				t.Fatalf("approved age with legacy values refused: used=%t err=%v", used, err)
			}
			raw, err := yaml.Marshal(cfg)
			if err != nil || !strings.Contains(string(raw), "output_prune_after_tokens: "+age) {
				t.Fatal("saved configuration lost age policy", err, string(raw))
			}
			if cfg.Context.InflightOutputs != 20 || cfg.Schemas.InflightCap != 5 {
				t.Fatal("legacy config values did not load compatibly")
			}
		})
	}
}

func TestOutputPruningYAMLNegativeAgeNamesCorrection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("context:\n  output_prune_after_tokens: -1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "output_prune_after_tokens cannot be negative") {
		t.Fatal("negative age needs an actionable field error", err)
	}
}
