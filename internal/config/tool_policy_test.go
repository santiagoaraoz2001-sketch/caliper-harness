package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToolPolicyDefaultsAndStrictRunSeconds(t *testing.T) {
	c := Defaults()
	if c.Model.ToolProtocol != "structured" || c.Agent.MaxRunSeconds != 0 {
		t.Fatal("invented defaults")
	}
	for _, raw := range []string{"agent:\n  max_run_seconds: -1\n", "agent:\n  max_run_seconds: 1.5\n", "agent:\n  max_run_seconds: '1'\n", "agent:\n  max_run_seconds: 999999999999999999999\n", "model:\n  tool_protocol: guessed\n"} {
		p := filepath.Join(t.TempDir(), "cfg.yaml")
		os.WriteFile(p, []byte(raw), 0600)
		if _, _, e := Load(p); e == nil {
			t.Fatalf("bad policy admitted %s", raw)
		}
	}
}
