package prompts

import (
	"os"
	"strings"
	"testing"
)

func TestCoverageGuidanceRetainsDetailedPlanningAndClaim(t *testing.T) {
	pc := Builtin()
	if !pc.Has("planning.answer.coverage") {
		t.Fatal("missing optional coverage question")
	}
	for _, key := range []string{"planning.goal", "planning.approach", "help.plan", "help.reference.pat"} {
		text := strings.Join(strings.Fields(pc.Text(key)), " ")
		for _, obsolete := range []string{"One Approach serves one article", "Other articles get their own Approaches", "Separate articles need separate Approaches", "an article is a delivery scope with its own Approach"} {
			if strings.Contains(text, obsolete) {
				t.Errorf("%s retains obsolete coverage constraint %q", key, obsolete)
			}
		}
		if !strings.Contains(strings.ToLower(text), "coverage") {
			t.Errorf("%s omits explicit coverage", key)
		}
	}
	if text := pc.Text("planning.select_approach"); !strings.Contains(text, "This Plan already has unresolved work") || strings.Contains(text, "This article already has work") {
		t.Fatal("shared owner selection is mislabelled as article-only", text)
	}
	text := strings.Join(strings.Fields(pc.Text("help.plan")), " ")
	if strings.Index(text, "finish all Task briefs") > strings.Index(text, "While executing a claimed task") {
		t.Fatal("drafting instruction follows execution direction")
	}
	for _, wanted := range []string{"all Task briefs", "readiness", "claim", "independent review"} {
		if !strings.Contains(text, wanted) {
			t.Errorf("detailed lifecycle lost %q", wanted)
		}
	}
}

func TestCoverageAdditionPreservesOlderCustomCatalog(t *testing.T) {
	path := catalogFile(t, func(m map[string]string) {
		delete(m, "planning.answer.coverage")
		m["planning.approach"] = "My existing customized Approach question"
	})
	pc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if pc.Has("planning.answer.coverage") || pc.Text("planning.approach") != "My existing customized Approach question" {
		t.Fatal("missing optional field silently replaced custom catalog")
	}
	if !Builtin().Has("planning.answer.coverage") {
		t.Fatal("owner fallback has no shipped coverage guide")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompatibleWith(path, catalogFile(t, nil)); err != nil {
		t.Fatal("older customized catalog blocks release activation", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("compatibility check rewrote the customized catalog", err)
	}
	for _, value := range []string{"", " \n"} {
		if _, err := Load(catalogFile(t, func(m map[string]string) { m["planning.answer.coverage"] = value })); err == nil {
			t.Fatal("explicit empty coverage guide accepted")
		}
	}
	path = catalogFile(t, func(m map[string]string) { m["planning.answer.coverage"] = "My optional coverage wording" })
	pc, err = Load(path)
	if err != nil || pc.Text("planning.answer.coverage") != "My optional coverage wording" {
		t.Fatal("custom coverage guide lost", err)
	}
}
