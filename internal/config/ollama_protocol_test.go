package config

import (
	"path/filepath"
	"testing"
)

func TestOllamaDecisionProtocolRoundTrip(t *testing.T) {
	connectionHome(t)
	c := Defaults()
	c.Decision.Runtime, c.Decision.Protocol, c.Decision.Model = "ollama", "system_one", "d1:3b"
	c.Decision.Device = "auto"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := SaveConnections(c); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil || got.Decision.Protocol != "system_one" {
		t.Fatalf("%+v %v", got.Decision, err)
	}
	c.Decision.Runtime = "openai"
	if c.Validate() == nil {
		t.Fatal("accepted incompatible runtime")
	}
	c.Decision.Runtime = "ollama"
	c.Decision.Revision = "native-checkpoint"
	if c.Validate() == nil {
		t.Fatal("accepted native asset override for Ollama")
	}
}
