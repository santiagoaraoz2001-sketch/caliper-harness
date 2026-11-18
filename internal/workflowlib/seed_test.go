package workflowlib

import (
	"path/filepath"
	"testing"
)

func TestEnsureSeedPublishesOnceWithoutEnabling(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSeed(s); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSeed(s); err != nil {
		t.Fatal(err)
	}
	blocks, workflows := s.List()
	if len(blocks) != 4 || len(workflows) != 1 {
		t.Fatalf("seed blocks=%d workflows=%d", len(blocks), len(workflows))
	}
	for _, b := range blocks {
		if b.Status != StatusPublished {
			t.Fatalf("seed block %s is %s", b.ID, b.Status)
		}
	}
	if _, err := s.StartExecution("repair-and-verify", nil); err == nil {
		t.Fatal("seed workflow must not be enabled implicitly")
	}
}
