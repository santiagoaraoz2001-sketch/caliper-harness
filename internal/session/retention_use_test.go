package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestR25MarkUseFailure(t *testing.T) {
	dir := t.TempDir()
	if err := MarkUsed(dir, "missing"); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "cannot record use") {
		t.Fatalf("lost timestamp failure cause: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("mark use created missing archive: %v", err)
	}
	if err := MarkUsed(dir, "../escape"); err == nil {
		t.Fatal("invalid use ID accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed use had effects: %v %v", entries, err)
	}
}
