package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThinkingLoopConfig(t *testing.T) {
	c := Defaults()
	c.Model.Name = "fixture"
	if c.Agent.ThinkingLoop != (ThinkingLoopConfig{16, 8, 256, 4096, 8192}) {
		t.Fatal("owner defaults differ")
	}
	for _, tc := range []struct {
		name, field string
		change      func(*ThinkingLoopConfig)
	}{
		{"repeat", "repeat", func(p *ThinkingLoopConfig) { p.Repeat = -1 }},
		{"min", "min_unit_bytes", func(p *ThinkingLoopConfig) { p.MinUnitBytes = 0 }},
		{"max", "max_unit_bytes", func(p *ThinkingLoopConfig) { p.MaxUnitBytes = 0 }},
		{"sample", "sample_bytes", func(p *ThinkingLoopConfig) { p.SampleBytes = 4095 }},
		{"input", "max_input_bytes", func(p *ThinkingLoopConfig) { p.MaxInputBytes = 0 }},
		{"order", "min_unit_bytes", func(p *ThinkingLoopConfig) { p.MinUnitBytes = 257 }},
		{"overflow", "sample_bytes", func(p *ThinkingLoopConfig) { p.Repeat = int(^uint(0) >> 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := c
			tc.change(&v.Agent.ThinkingLoop)
			if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "agent.thinking_loop."+tc.field) {
				t.Fatalf("named refusal %v", err)
			}
		})
	}
	p := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(p, []byte("model:\n  name: fixture\nagent:\n  thinking_loop:\n    repeat: 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(p)
	if err != nil || loaded.Agent.ThinkingLoop.Repeat != 0 || loaded.Agent.ThinkingLoop.SampleBytes != 4096 {
		t.Fatalf("disable/load %+v %v", loaded.Agent, err)
	}
	if err := os.WriteFile(p, []byte("model:\n  name: fixture\nagent:\n  thinking_loop:\n    samples_bytes: 4096\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Load(p); err == nil || !strings.Contains(err.Error(), "samples_bytes") {
		t.Fatalf("unknown silently admitted %v", err)
	}
}
