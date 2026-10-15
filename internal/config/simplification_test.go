package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimplifiedDefaultAndExplicitBudgets(t *testing.T) {
	c := Defaults()
	if c.Agent.MaxSteps != 0 {
		t.Fatalf("default call ceiling=%d; want explicit no-ceiling", c.Agent.MaxSteps)
	}
	for _, limit := range []int{0, 1, 25, 80} {
		c.Agent.MaxSteps = limit
		if err := c.Validate(); err != nil {
			t.Fatalf("legal explicit limit%d: %v", limit, err)
		}
	}
	c.Agent.MaxSteps = -1
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "agent.max_steps") {
		t.Fatal("negative call limit admitted", err)
	}
	c = Defaults()
	c.Schemas.KeepLatest = 2
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "schemas.keep_latest") {
		t.Fatal("configuration below actual schema floor admitted", err)
	}
	c.Schemas.KeepLatest = 3
	if err := c.Validate(); err != nil {
		t.Fatal("actual schema floor refused", err)
	}
}

func TestRetiredPruneThinkingMigrationIsExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.yaml")
	if err := os.WriteFile(path, []byte("context:\n  prune_thinking: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "prune_thinking") {
		t.Fatal("retired key silently ignored", err)
	}
}
