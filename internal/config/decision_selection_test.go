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
