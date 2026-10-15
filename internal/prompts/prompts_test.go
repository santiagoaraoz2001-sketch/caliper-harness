package prompts

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogFile(t *testing.T, change func(map[string]string)) string {
	t.Helper()
	var d document
	if err := yaml.Unmarshal([]byte(Builtin().YAML()), &d); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(d.Texts)
	}
	raw, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prompts.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestCatalogRejectsInvalidSource(t *testing.T) {
	cases := []struct {
		name, key, want string
		change          func(map[string]string)
	}{
		{"missing", "system.native.orientation", "missing key", func(m map[string]string) { delete(m, "system.native.orientation") }},
		{"unknown", "system.native.orientaton", "unknown key", func(m map[string]string) { m["system.native.orientaton"] = "typo" }},
		{"empty", "system.native.orientation", "empty key", func(m map[string]string) { m["system.native.orientation"] = "" }},
		{"syntax", "system.native.orientation", "system.native.orientation", func(m map[string]string) { m["system.native.orientation"] = "{{if}}" }},
		{"field", "system.task.execute", "unknown field .Typo", func(m map[string]string) { m["system.task.execute"] = "{{.Typo}}" }},
		{"printf string as number", "system.task.execute", "incompatible with owning field .TaskID", func(m map[string]string) { m["system.task.execute"] = `{{printf "%d" .TaskID}}` }},
		{"printf number as string", "system.task.execute", "incompatible with owning field .Attempt", func(m map[string]string) { m["system.task.execute"] = `{{printf "%s" .Attempt}}` }},
		{"unused definition", "system.task.execute", "definitions are unsupported", func(m map[string]string) { m["system.task.execute"] = `{{define "unused"}}{{.Typo}}{{end}}valid root` }},
		{"index bypass", "system.task.execute", "unsupported", func(m map[string]string) { m["system.task.execute"] = `{{index . "Typo"}}` }},
		{"variable bypass", "system.task.execute", "unsupported", func(m map[string]string) { m["system.task.execute"] = `{{$x := .}}{{$x.Typo}}` }},
		{"chain bypass", "system.task.execute", "unsupported", func(m map[string]string) { m["system.task.execute"] = `{{(.TaskID).Typo}}` }},
		{"conditional bypass", "system.task.execute", "unsupported", func(m map[string]string) { m["system.task.execute"] = `{{if eq .Attempt 2}}{{.Typo}}{{end}}` }},
	}
	for _, x := range cases {
		t.Run(x.name, func(t *testing.T) {
			_, err := Load(catalogFile(t, x.change))
			if err == nil || !strings.Contains(err.Error(), x.want) {
				t.Fatalf("%s: %v", x.key, err)
			}
		})
	}
	path := catalogFile(t, nil)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{string(raw) + "---\ntexts: {}\n", "texts:\n  a: first\n  a: second\n"} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("ambiguous YAML accepted")
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("explicit missing file fell back")
	}
}
func TestCatalogSnapshotAndNamedFormat(t *testing.T) {
	path := catalogFile(t, func(m map[string]string) { m["system.task.execute"] = "{{printf \"%d\" .Attempt}} {{.TaskID}}" })
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Format("system.task.execute", "actual-id", 4); got != "4 actual-id" {
		t.Fatal(got)
	}
	// Positional owning arguments remain bound by shipped field names even when
	// the editable text reorders them; literal caller data is not reparsed.
	if got := c.Format("system.task.execute", "{{.Typo}}", 4); got != "4 {{.Typo}}" {
		t.Fatal(got)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := c.Format("system.task.execute", "actual-id", 4); got != "4 actual-id" {
		t.Fatal("in-flight snapshot changed")
	}
	if _, err := Load(path); err == nil {
		t.Fatal("new snapshot accepted broken file")
	}
	if _, err := c.Render("system.task.execute", map[string]any{"TaskID": "id"}); err == nil {
		t.Fatal("missing owning field ignored")
	}
}
func TestLegacyTemplateDirectoryRequiresCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "task.tmpl"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Legacy(dir); err == nil || !strings.Contains(err.Error(), "standalone .tmpl") {
		t.Fatalf("%v", err)
	}
}

func TestDefaultEditableAssetNeedsNoOptIn(t *testing.T) {
	path := catalogFile(t, func(m map[string]string) { m["system.native.orientation"] = "EDITED_DEFAULT" })
	t.Chdir(filepath.Dir(path))
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if c.Text("system.native.orientation") != "EDITED_DEFAULT" || c.Source != "prompts.yaml" {
		t.Fatalf("source=%s instruction=%s", c.Source, c.Text("system.native.orientation"))
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	embedded, err := Default()
	if err != nil || embedded.Source != "embedded prompts.yaml" {
		t.Fatalf("fallback source=%v error=%v", embedded, err)
	}
}

func TestCatalogRejectsNonStringYAMLScalars(t *testing.T) {
	path := catalogFile(t, nil)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := "    system.native.orientation: "
	start := strings.Index(string(raw), line)
	if start < 0 {
		t.Fatal("fixture missing key")
	}
	end := strings.IndexByte(string(raw)[start:], '\n') + start
	for _, scalar := range []string{"123", "1.5", "true", "false", "null", "[sequence]", "yes", "no", "on", "off"} {
		bad := string(raw)[:start] + line + scalar + string(raw)[end:]
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || (!strings.Contains(err.Error(), "string key and value") && !strings.Contains(err.Error(), "quoted string")) {
			t.Fatalf("scalar=%s error=%v", scalar, err)
		}
	}
}
