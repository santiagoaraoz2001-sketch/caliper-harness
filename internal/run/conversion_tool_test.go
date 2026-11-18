package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/tool"
	toolslib "specific-harness/internal/tools"
	"specific-harness/internal/workflowlib"
)

const fixtureSkill = "---\nname: demo-skill\ndescription: Demo external skill\n---\n# Demo Skill\n\nIntro text.\n\n## Steps\n\nDo the thing in order.\n\n## Example\n\nInput A produces output B.\n\n## Pitfalls\n\nNever do C.\n"

func conversionCall(t *testing.T, def tool.Definition, args tool.Args) (tool.Result, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	values, gap := tool.ValidateJSON(def, string(raw))
	if gap != "" {
		t.Fatalf("validate %v: %s\n%s", args, gap, def.Schema())
	}
	return def.Execute(context.Background(), values)
}

func conversionFixture(t *testing.T, target string) (*planningFixture, tool.Definition) {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	f := newPlanningFixture(t)
	dir := filepath.Join(f.d.Workspace, "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fixtureSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	return f, conversionDefinition(f.d, "fixture-session", f.d.Prompts)
}

func TestConvertSkillDomainPublishesGraphAndPreservesSource(t *testing.T) {
	f, def := conversionFixture(t, "domain")
	start, err := conversionCall(t, def, tool.Args{"action": "start", "id": "demo-skill", "target": "domain", "source_path": "skills/demo/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(start.Text, `"Steps"`) || !strings.Contains(start.Text, `"Pitfalls"`) {
		t.Fatalf("start did not outline source headings: %s", start.Text)
	}
	for _, piece := range []tool.Args{
		{"action": "add", "id": "demo-skill", "source_heading": "Steps", "kind": "section", "piece_title": "Steps", "body": "Do the thing in order: first prepare, then act, then verify the result against the request."},
		{"action": "add", "id": "demo-skill", "source_heading": "Example", "kind": "example", "piece_title": "Worked example", "body": "Input A produces output B when the steps are followed in order."},
		{"action": "add", "id": "demo-skill", "source_heading": "Pitfalls", "kind": "negative", "piece_title": "Pitfalls", "body": "Never do C: it corrupts the result while looking successful."},
	} {
		if _, err := conversionCall(t, def, piece); err != nil {
			t.Fatalf("add %v: %v", piece["piece_title"], err)
		}
	}
	pub, err := conversionCall(t, def, tool.Args{"action": "publish", "id": "demo-skill"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project/skills/demo-skill/source", "project/guides/demo-skill/index", `"status":"published"`} {
		if !strings.Contains(pub.Text, want) {
			t.Fatalf("publish result missing %q: %s", want, pub.Text)
		}
	}
	_ = f
}

func TestConvertSkillPublishRefusesUncoveredHeading(t *testing.T) {
	_, def := conversionFixture(t, "domain")
	if _, err := conversionCall(t, def, tool.Args{"action": "start", "id": "demo-skill", "target": "domain", "source_path": "skills/demo/SKILL.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversionCall(t, def, tool.Args{"action": "add", "id": "demo-skill", "source_heading": "Steps", "kind": "section", "piece_title": "Steps", "body": "Do the thing in order."}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversionCall(t, def, tool.Args{"action": "publish", "id": "demo-skill"}); err == nil || !strings.Contains(err.Error(), "not mapped or preserved") {
		t.Fatalf("publish with uncovered headings err=%v", err)
	}
}

func TestConvertSkillWorkflowPublishesBlocksAndWorkflow(t *testing.T) {
	f, def := conversionFixture(t, "workflow")
	if _, err := conversionCall(t, def, tool.Args{"action": "start", "id": "demo-skill", "target": "workflow", "source_path": "skills/demo/SKILL.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversionCall(t, def, tool.Args{"action": "add", "id": "demo-skill", "source_heading": "Steps", "kind": "section", "piece_title": "Do steps", "body": "Do the thing in order and record the result.", "evidence": "recorded step output"}); err != nil {
		t.Fatal(err)
	}
	pub, err := conversionCall(t, def, tool.Args{"action": "publish", "id": "demo-skill", "preserve": "Example\nPitfalls"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pub.Text, `"status":"published"`) {
		t.Fatalf("publish result: %s", pub.Text)
	}
	s, err := workflowlib.Open(toolslib.LibraryPath(f.d.Workspace))
	if err != nil {
		t.Fatal(err)
	}
	w, ok := s.GetWorkflow("demo-skill", 0)
	if !ok || w.Status != workflowlib.StatusPublished || len(w.Steps) != 1 {
		t.Fatalf("published workflow = %+v ok=%t", w, ok)
	}
	if act, enabled := s.Activation("demo-skill"); enabled && act.Enabled {
		t.Fatal("conversion must not auto-enable the workflow")
	}
}
