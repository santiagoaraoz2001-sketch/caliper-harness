package tui

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/decision"
)

func TestDecisionsNewestEventSurvivesDisplayAllowance(t *testing.T) {
	m := decisionsModel(t)
	m.Cfg.UI.MaxDecisionRows = 1
	m = loadDecisions(m)
	if len(m.Decisions.Rows) != 1 || m.Decisions.Rows[0].Source != decision.SourceCoherence ||
		m.Decisions.BySource[decision.SourceCoherence] != 1 || m.Decisions.Buckets != [4]int{0, 0, 0, 1} {
		t.Fatalf("newest judgment hidden or accounting changed: %+v", m.Decisions)
	}
}

func TestCapturedGuidedBriefReadableInJourney(t *testing.T) {
	var captured struct{ Goal, Approach string }
	raw, err := os.ReadFile("testdata/guided-briefs.json")
	if err != nil || json.Unmarshal(raw, &captured) != nil {
		t.Fatal("cannot load captured planning brief", err)
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, target := range []string{"plan-one", "approach-one"} {
			m := journeyFixture(size[0], size[1])
			m.Journey.Tree.Plans[0].P.Goal = captured.Goal
			m.Journey.Tree.Plans[0].Approaches[0].A.Title = captured.Approach
			m.Journey.Tree.rebuild("")
			before, err := json.Marshal(m.Journey.Tree.Plans)
			if err != nil {
				t.Fatal(err)
			}
			contextBefore := m.Chat.Context.Clone()
			m.openJourney(journeyChoice{frame: journeyFrame{Kind: "brief"}, target: target})
			seen := ""
			for range 50 {
				frame := viewportFrame(t, m)
				seen += frame + "\n"
				m = viewportKey(m, tea.KeyPgDown)
				if frame == viewportFrame(t, m) {
					break
				}
			}
			needles := []string{"goal:", "rules:", "articles:", "1. Cardinal", "2. Nonnegative", "completion evidence:"}
			if target == "approach-one" {
				needles = []string{"Article 1:", "strategy:", "Number.isInteger", "independent review"}
			}
			for _, needle := range needles {
				if !strings.Contains(seen, needle) {
					t.Fatalf("%dx%d %s cannot reach %q", size[0], size[1], target, needle)
				}
			}
			if strings.Contains(seen, `"article_index"`) || strings.Contains(seen, `"articles"`) {
				t.Fatal("guided brief still exposes serialized scaffolding")
			}
			after, _ := json.Marshal(m.Journey.Tree.Plans)
			if string(before) != string(after) || !reflect.DeepEqual(contextBefore, m.Chat.Context) || m.Chat.Running {
				t.Fatal("reading the brief changed authored facts or execution")
			}
		}
	}
}

func TestGuidedBriefUnknownOrFreeformStaysLiteral(t *testing.T) {
	for _, raw := range []string{
		"Keep the existing implementation.",
		`{"outcome":"x","rules":"","articles":["one"],"proof":"tests","future":"preserve me"}`,
		`{"outcome":null,"rules":"","articles":["one"],"proof":"tests"}`,
		`{"article_index":null,"article":"x","strategy":"y"}`,
		`{"article_index":-1,"article":"x","strategy":"y"}`,
		`{"article_index":0,"article":"x","strategy":"y","future":"preserve me"}`,
		`{"article_index":0,"article":"x","strategy":"y"} trailing`,
	} {
		for _, approach := range []bool{false, true} {
			if _, rows := guidedBrief(raw, approach); rows != nil {
				t.Fatalf("nonstandard authored document interpreted instead of retained: %s", raw)
			}
		}
		if approachLabel(raw) != raw {
			t.Fatal("freeform label changed")
		}
	}
}

func TestGuidedMultiArticleBriefPreservesEveryAuthoredValue(t *testing.T) {
	raw := `{"coverage":[{"article_index":0,"article":"First requirement"},{"article_index":1,"article":"Second requirement"}],"strategy":"One shared implementation"}`
	label, rows := guidedBrief(raw, true)
	if label != "Articles 1, 2" || !reflect.DeepEqual(rows, []string{"Article 1: First requirement", "Article 2: Second requirement", "strategy:", "One shared implementation"}) {
		t.Fatalf("lost coverage: %s %v", label, rows)
	}
	for _, unknown := range []string{
		`{"coverage":[{"article_index":0,"article":"one","future":"keep me"}],"strategy":"fix"}`,
		`{"coverage":[{"article_index":null,"article":"one"}],"strategy":"fix"}`,
		`{"coverage":[],"strategy":"fix"}`,
		`{"coverage":[{"article_index":0,"article":"one"}],"strategy":"fix","future":"keep me"}`,
	} {
		if _, rows := guidedBrief(unknown, true); rows != nil {
			t.Fatalf("unknown metadata hidden: %s", unknown)
		}
	}
}
