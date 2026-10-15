package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchCaptureBudgetYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	for _, tc := range []struct {
		value string
		want  int
		bad   bool
	}{{"", 16777216, false}, {"max_capture_bytes: 12345", 12345, false}, {"max_capture_bytes: 0", 0, true}, {"max_capture_bytes: -1", 0, true}} {
		body := "{}\n"
		if tc.value != "" {
			body = "tools:\n  search:\n    " + tc.value + "\n"
		}
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		cfg, _, err := Load(p)
		if tc.bad {
			if err == nil || !strings.Contains(err.Error(), "tools.search.max_capture_bytes") {
				t.Fatalf("value %q error=%v", tc.value, err)
			}
			continue
		}
		if err != nil || cfg.Tools.Search.MaxCaptureBytes != tc.want {
			t.Fatalf("value %q cfg=%+v err=%v", tc.value, cfg.Tools.Search, err)
		}
		if err := Save(p, cfg); err != nil {
			t.Fatal(err)
		}
		reopened, _, err := Load(p)
		if err != nil || reopened.Tools.Search.MaxCaptureBytes != tc.want {
			t.Fatalf("roundtrip %+v %v", reopened.Tools.Search, err)
		}
	}
}
