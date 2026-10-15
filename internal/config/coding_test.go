package config

import (
	"path/filepath"
	"testing"
)

func TestCodingCommandYAMLRoundTrip(t *testing.T) {
	cfg := Defaults()
	if cfg.Coding.CheckCommand != "" {
		t.Fatal("invented check default")
	}
	cfg.Coding.CheckCommand = "  printf 'café\\n'\ntrue  "
	p := filepath.Join(t.TempDir(), "config.yaml")
	if e := Save(p, cfg); e != nil {
		t.Fatal(e)
	}
	got, _, e := Load(p)
	if e != nil || got.Coding.CheckCommand != cfg.Coding.CheckCommand {
		t.Fatalf("command bytes %q %v", got.Coding.CheckCommand, e)
	}
}
