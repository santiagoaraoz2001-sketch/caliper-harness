package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthListYAMLShapeRefusesBeforeUse(t *testing.T) {
	for _, tc := range []struct{ name, yaml, key string }{{"blank", "free_tools: ['']", "auth.free_tools"}, {"duplicate", "judged_tools: [bash, bash]", "auth.judged_tools"}, {"overlap", "free_tools: [bash]\n  judged_tools: [bash]", "auth.judged_tools"}, {"canonical", "free_tools: [' BASH ']", "auth.free_tools"}} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			os.WriteFile(p, []byte("auth:\n  mode: yolo\n  "+tc.yaml+"\n"), 0600)
			_, _, e := Load(p)
			if e == nil || !strings.Contains(e.Error(), tc.key) {
				t.Fatalf("expected named YAML refusal, got %v", e)
			}
		})
	}
}
