package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTemplatesOmittedSaveRemainsPortable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.yaml")
	cfg := Defaults()
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Templates map[string]any }
	if err := yaml.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Templates["path"]; ok {
		t.Fatalf("Save pins omitted default templates path: %v", saved.Templates)
	}
	got, used, err := Load(p)
	if err != nil || !used || got.Templates.Path != "templates" {
		t.Fatalf("reload=%+v used=%v err=%v", got.Templates, used, err)
	}
	if cfg.Templates.Path != "templates" {
		t.Fatal("Save mutated default")
	}
}

func TestTemplatesOriginExplicitAndCustomPaths(t *testing.T) {
	bundle := t.TempDir()
	assets := filepath.Join(bundle, "templates")
	if err := os.Mkdir(assets, 0700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bundle, "specific-harness")
	if got := Defaults().templatesDir(exe); got != assets {
		t.Fatalf("omitted default=%q want%q", got, assets)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"omitted", "auth: {mode: yolo}\n", assets},
		{"empty mapping", "templates: {}\n", assets},
		{"explicit default", "templates: {path: templates}\n", "templates"},
		{"custom relative", "templates: {path: missing/custom}\n", "missing/custom"},
		{"absolute", "templates: {path: /abs/missing/templates}\n", "/abs/missing/templates"},
		{"explicit null overlay", "templates: {path: null}\n", "templates"},
		{"merged explicit", "templates: {<<: &T {path: templates}}\n", "templates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "cfg.yaml")
			if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			c, used, err := Load(p)
			if err != nil || !used {
				t.Fatalf("load used%v err%v", used, err)
			}
			if got := c.templatesDir(exe); got != tc.want {
				t.Fatalf("resolved%q want%q", got, tc.want)
			}
			out := filepath.Join(t.TempDir(), "saved.yaml")
			if err := Save(out, c); err != nil {
				t.Fatal(err)
			}
			next, _, err := Load(out)
			if err != nil {
				t.Fatal(err)
			}
			if got := next.templatesDir(exe); got != tc.want {
				t.Fatalf("Save changed explicit origin: %q", got)
			}
			if next.Behavior.Path != c.Behavior.Path || next.Store.Path != c.Store.Path || next.Sessions.Path != c.Sessions.Path {
				t.Fatal("other paths changed")
			}
		})
	}
	c := Defaults()
	c.Templates.Path = "../../custom"
	if got := c.templatesDir(exe); got != c.Templates.Path {
		t.Fatal("custom programmatic path resolved into bundle")
	}
	if got := Defaults().templatesDir(filepath.Join(t.TempDir(), "no-assets")); got != "templates" {
		t.Fatalf("missing bundle fallback=%q", got)
	}
	p := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(p, []byte("templates: {paht: x}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(p); err == nil {
		t.Fatal("origin decode relaxed strict unknown-key refusal")
	}
}

func TestTemplatesConfigOutputKeepsOmittedOrigin(t *testing.T) {
	cfg := Defaults()
	raw, err := yaml.Marshal(cfg) // Same public serialization used by config CLI.
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Templates map[string]any }
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if _, present := out.Templates["path"]; present {
		t.Fatal("config output pins default path")
	}
	p := filepath.Join(t.TempDir(), "generated.yaml")
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(p)
	if err != nil || !got.templatesDefault {
		t.Fatalf("origin lost: %v", err)
	}
}
