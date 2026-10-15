package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreBufferPoolYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "buffer_pool_bytes: 268435456") {
		t.Fatalf("approved default missing: %s", body)
	}
	for _, value := range []string{"0", "4096", "67108864", "268435456"} {
		if err := os.WriteFile(path, []byte("store:\n  buffer_pool_bytes: "+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, _, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if err = Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		body, _ = os.ReadFile(path)
		if !strings.Contains(string(body), "buffer_pool_bytes: "+value) {
			t.Fatalf("policy lost across Save: %s", body)
		}
	}
	for _, value := range []string{"-1", "1", "4095", "18446744073709551616", "not_bytes", "4096.5"} {
		os.WriteFile(path, []byte("store:\n  buffer_pool_bytes: "+value+"\n"), 0600)
		if _, _, err := Load(path); err == nil {
			t.Fatalf("invalid policy %s accepted", value)
		}
	}
}
