package config

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationBudgetDefaultsAndYAML(t *testing.T) {
	raw, err := yaml.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "max_output_tokens: 16384") {
		t.Fatal("default budget missing/wrong")
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	for _, value := range []string{"2048", "0"} {
		if err := os.WriteFile(p, []byte("params:\n  max_output_tokens: "+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		c, _, err := Load(p)
		if err != nil {
			t.Fatalf("valid cap %s refused: %v", value, err)
		}
		if err := Save(p, c); err != nil {
			t.Fatal(err)
		}
		back, _, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := yaml.Marshal(back)
		if !strings.Contains(string(raw), "max_output_tokens: "+value) {
			t.Fatal("cap not preserved")
		}
	}
	for _, bad := range []string{"-1", "1.5", "\"2048\"", "999999999999999999999999999999", "hot"} {
		original := []byte("params:\n  max_output_tokens: " + bad + "\n")
		os.WriteFile(p, original, 0600)
		if _, _, err := Load(p); err == nil {
			t.Errorf("invalid integer budget accepted: %s", bad)
		}
		after, _ := os.ReadFile(p)
		if string(after) != string(original) {
			t.Fatal("failed load altered file")
		}
	}
}

func TestGenerationBudgetSaveRefusalAndNullOverlay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	c := Defaults()
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	c.Params.MaxOutputTokens = -1
	if err := Save(p, c); err == nil || !strings.Contains(err.Error(), "params.max_output_tokens") {
		t.Fatalf("invalid programmatic budget saved: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(before) {
		t.Fatal("failed save overwrote file")
	}
	// Existing YAML null-as-omitted overlay policy preserves the approved default.
	os.WriteFile(p, []byte("params:\n  max_output_tokens: null\n"), 0600)
	back, _, err := Load(p)
	if err != nil || back.Params.MaxOutputTokens != 16384 {
		t.Fatalf("null must keep default: %d %v", back.Params.MaxOutputTokens, err)
	}
}
