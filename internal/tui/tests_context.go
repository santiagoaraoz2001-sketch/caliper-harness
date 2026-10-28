package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/run"
	"specific-harness/internal/store"
	"strings"
	"unicode/utf8"
)

type testDesignSpec struct {
	Suite   string `json:"-"`
	Key     string `json:"key"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Body    string `json:"body,omitempty"`
}

func testDesignConfig(c config.Config) string {
	b, _ := json.Marshal(struct {
		Bash   config.BashConfig
		Suites []config.TestSuite
	}{c.Tools.Bash, run.TestSuites(c)})
	return string(b)
}

func testSuiteJSON(s config.TestSuite) string { b, _ := json.Marshal(s); return string(b) }

// Refresh owns these reads. Rendering and design draft construction never open stores.
func testDesignSpecs(ctx context.Context, cfg config.Config, s *store.Store, root string, selected config.TestSuite) map[string]testDesignSpec {
	out := map[string]testDesignSpec{}
	remaining := cfg.Tools.GraphQL.MaxResultBytes
	rows := append([]config.TestSuite{selected}, run.TestSuites(cfg)...)
	for _, suite := range rows {
		if _, ok := out[suite.ID]; ok {
			continue
		}
		spec := testDesignSpec{Suite: testSuiteJSON(suite), Key: suite.SpecKey, Version: suite.SpecVersion, Status: "not configured"}
		if suite.SpecKey != "" {
			spec.Status = "unavailable"
			if remaining <= 0 {
				spec.Status = "omitted-budget"
			} else if s != nil {
				entries, err := memory.Knowledge(ctx, s, root, "", suite.SpecVersion, 1, remaining, true, []string{"key"})
				switch {
				case errors.Is(err, memory.ErrSourceBudget):
					spec.Status = "omitted-budget"
				case err != nil:
				case len(entries) == 0:
					spec.Status = "absent"
				case entries[0].Key != suite.SpecKey:
					spec.Status = "key/version mismatch"
				case cfg.Recall.MaxChars > 0 && utf8.RuneCountInString(entries[0].Body) > cfg.Recall.MaxChars:
					spec.Status = "omitted-budget"
				default:
					spec.Status = "supplied immutable version"
					spec.Body = entries[0].Body
					remaining -= len(spec.Body)
				}
			}
		}
		out[suite.ID] = spec
	}
	return out
}

func (m Model) testDesignContext(s config.TestSuite) string {
	loaded := m.Tests.ContextConfig == testDesignConfig(m.Cfg)
	spec, ok := m.Tests.Specs[s.ID]
	if !loaded || !ok || spec.Suite != testSuiteJSON(s) {
		spec = testDesignSpec{Key: s.SpecKey, Version: s.SpecVersion, Status: "not loaded for this selection; refresh /tests or read the exact version"}
	}
	if s.SpecKey == "" {
		spec.Status = "not configured"
	}
	b, _ := json.Marshal(spec)
	text := "\n\nSelected spec (authored evidence, not execution authority):\n" + string(b)
	if s.SpecKey != "" {
		text += fmt.Sprintf("\nExact spec lookup: { knowledge(id:%q) { id key body } }", s.SpecVersion)
	}
	if ready := m.Tests.Readiness[s.ID]; loaded && ready != "" {
		text += "\nReadiness at last refresh: " + ready
	}
	found := false
	for _, history := range m.Tests.History {
		if !loaded {
			break
		}
		for _, prior := range history.Selection.Suites {
			if prior.ID != s.ID {
				continue
			}
			fresh := m.Tests.Freshness[history.Selection.ID]
			if fresh == "" {
				fresh = "unknown"
			}
			for _, result := range history.Results {
				if result.SuiteID != s.ID {
					continue
				}
				summary := struct {
					Session, Status, Freshness string
					SameDefinition             bool
					Coverage                   run.TestCoverage
				}{history.Selection.ID, result.Status, fresh, testSuiteJSON(prior) == testSuiteJSON(s), result.Coverage}
				raw, _ := json.Marshal(summary)
				text += "\nLatest matching suite history (standalone; never Chat/PAT qualification): " + string(raw)
			}
			text += fmt.Sprintf("\nRead recorded evidence with the model GraphQL tool, following nextOffset: { sessionEvidence(sessionId:%q,offset:0,limit:3) { historical nextOffset groups { reference records { kind source workspace text } } } }", history.Selection.ID)
			found = true
			break
		}
		if found {
			break
		}
	}
	if !found {
		text += "\nPrior matching Tests evidence: none loaded; this does not prove no runs exist."
	}
	if m.Cfg.Testing.Guidance {
		guide := memory.BuiltinKnowledge(memory.TestsStandardKey, "", "")
		for _, part := range memory.Sections(guide.Body, "", "oracle") {
			refs, _ := json.Marshal(memory.SectionReferences(memory.BuiltinReferences(guide.Key, false), part.ID))
			if len(part.Body)+len(refs) <= m.Cfg.Tools.GraphQL.MaxResultBytes {
				text += "\n\nDefault testing advice " + guide.Key + "#" + part.ID + " @ " + guide.ID + ":\n" + part.Body + "\nSection links: " + string(refs)
			} else {
				text += "\nTesting oracle advice omitted-budget; read caliper/standards/tests section oracle."
			}
		}
		text += "\nUse the fresh project/testing/strategy and default intent supplied by the project context; follow linked sections only when needed."
	}
	return strings.TrimRight(text, "\n")
}
