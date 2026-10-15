package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestServeModelDirsStrictRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Defaults()
	cfg.Serve.ModelDirs = []string{"/existing/path with  spaces", "/another"}
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(p)
	if err != nil || !reflect.DeepEqual(loaded.Serve.ModelDirs, cfg.Serve.ModelDirs) {
		t.Fatalf("%v %v", loaded.Serve.ModelDirs, err)
	}
	os.WriteFile(p, []byte("serve:\n  model_dirz: []\n"), 0600)
	if _, _, err := Load(p); err == nil {
		t.Fatal("unknownkeys silently accepted")
	}
}
