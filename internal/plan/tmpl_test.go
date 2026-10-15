package plan

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"specific-harness/internal/prompts"
	"strings"
	"testing"
)

func TestRenderMechanics(t *testing.T) {
	dir := t.TempDir()
	doc := map[string]map[string]string{}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	write := func() {
		t.Helper()
		raw, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "prompts.yaml"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Use the existing Title-based custom fixture for successful rendering.
	doc["texts"]["templates.task"] = "TITLE={{.Title}}"
	write()
	out, err := Render(dir, "task.tmpl", map[string]string{"Title": "bob"})
	if err != nil {
		t.Fatalf("render: %s", err)
	}
	if out != "TITLE=bob" {
		t.Fatalf("wrong render: %q", out)
	}
	if _, err := Render(dir, "missing.tmpl", nil); err == nil {
		t.Fatal("missing template accepted, want error")
	} else if !strings.Contains(err.Error(), "templates.missing") {
		t.Fatalf("error must name the catalog key: %s", err)
	}
	// Preserve the original arbitrary name/data as an explicit rejected contract.
	if _, err := Render(dir, "greet.tmpl", map[string]string{"Name": "bob"}); err == nil || !strings.Contains(err.Error(), "templates.greet") {
		t.Fatalf("unknown template key accepted: %v", err)
	}
	doc["texts"]["templates.task"] = "hi {{.Name}}!"
	write()
	if _, err := Render(dir, "task.tmpl", map[string]string{"Name": "bob"}); err == nil || !strings.Contains(err.Error(), ".Name") {
		t.Fatalf("unsupported owning placeholder accepted: %v", err)
	}
	standalone := t.TempDir()
	if err := os.WriteFile(filepath.Join(standalone, "greet.tmpl"), []byte("hi {{.Name}}!"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(standalone, "greet.tmpl", map[string]string{"Name": "bob"}); err == nil || !strings.Contains(err.Error(), "standalone .tmpl files are no longer prompt sources") {
		t.Fatalf("standalone legacy prompt silently accepted: %v", err)
	}
}

func TestRenderShippedTaskTemplate(t *testing.T) {
	out, err := Render("../../templates", "task.tmpl", map[string]string{"Title": "T1"})
	if err != nil {
		t.Fatalf("render: %s", err)
	}
	want := "Task T1 — one bounded deliverable; answer each question in the existing field:\n" +
		"current: <What was observed, in which runtime path/symbol, and what remains unknown?>\n" +
		"target: <What observable behavior must change?>\n" +
		"sketch: <How will this task achieve its objective? Give necessary steps with purpose, action and validation.>\n" +
		"acceptance: <What observable result establishes the requested behavior?>\n" +
		"tests: <Which existing/proposed assertions distinguish the behavior? Mark unexecuted tests.>\n" +
		"verify: <What responsible source/assertion must an independent reviewer inspect and rerun?>\n"
	if out != want {
		t.Fatalf("wrong render:\n%s", out)
	}
}

func TestRenderShippedPlanAndApproach(t *testing.T) {
	for _, name := range []string{"plan.tmpl", "approach.tmpl"} {
		out, err := Render("../../templates", name, map[string]string{"Title": "X"})
		if err != nil {
			t.Fatalf("%s: %s", name, err)
		}
		if !strings.Contains(out, "X") {
			t.Fatalf("%s ignores title: %q", name, out)
		}
	}
}
