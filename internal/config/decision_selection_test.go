package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDecisionSelectionRoundTripOpenAssets(t *testing.T) {
	c := Defaults()
	c.Decision.Model = "third-org/compatible-model"
	c.Decision.Runtime = "system_one"
	c.Decision.Path = "/downloaded/checkpoint"
	c.Decision.Revision = "pinned"
	c.Decision.TrustRemoteCode = true
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(p)
	if err != nil || !reflect.DeepEqual(got.Decision, c.Decision) {
		t.Fatalf("roundtrip: %+v %v", got.Decision, err)
	}
	c.Decision.Runtime = "chat"
	if c.Validate() == nil {
		t.Fatal("unsupported native runtime accepted")
	}
	c = Defaults()
	c.Decision.Model = "  "
	if c.Validate() == nil {
		t.Fatal("blank model accepted")
	}
}

func TestR3DecisionRuntimeSelectionPreservesCompatibleProtocol(t *testing.T) {
	c := Defaults()
	c.Decision.Runtime, c.Decision.Protocol = "ollama", "system_one"
	before := c.Decision
	if got := before.WithRuntime("ollama"); !reflect.DeepEqual(got, before) {
		t.Fatal("same runtime lost the configured native protocol")
	}
	for _, runtime := range []string{"openai", "rulith", "laya", "system_one"} {
		got := before.WithRuntime(runtime)
		want := before
		want.Runtime, want.Protocol = runtime, ""
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(before, c.Decision) {
			t.Fatalf("runtime selection changed unrelated settings or original: %+v", got)
		}
		c.Decision = got
		c.Decision.Protocol = "system_one" // Explicitly invalid author input must still fail.
		if err := c.Validate(); err == nil {
			t.Fatalf("explicit invalid protocol accepted for %s", runtime)
		}
		c.Decision = before
	}
}
