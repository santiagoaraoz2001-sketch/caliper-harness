package graph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/store"
)

// The test standard and authored strategy use the same scoped, versioned
// knowledge path. This does not turn authored test intent into execution proof.
func TestTestingKnowledgeGuideStrategyAndReopen(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	root := t.TempDir()
	a := projectAPI(t, s, root)
	lookup := `{ strategy:knowledge(key:"project/testing/strategy",limit:1) { id body previousId } standard:knowledge(key:"caliper/standards/tests",section:"oracle",limit:1) { id body source } }`
	initial := projectExec(t, a, lookup)
	if !strings.Contains(string(initial.Data), `"strategy":[]`) || !strings.Contains(string(initial.Data), "independently justified") || !strings.Contains(string(initial.Data), "caliper-default") || strings.Contains(string(initial.Data), "## execution") {
		t.Fatalf("bounded test guidance missing or whole guide injected: %s", initial.Data)
	}
	first := projectExec(t, a, `mutation { appendKnowledge(key:"project/testing/strategy",body:"Use existing framework; unit checks cover conversion. Browser prerequisites are explicit; normal UI journey remains unverified.",expectedPreviousId:"",references:[{key:"caliper/standards/tests",section:"oracle",relation:"explains"}]) { id body references { key section status } } }`)
	if len(first.Effects) != 1 || first.Effects[0].ID == "" || !strings.Contains(string(first.Data), `"resolved"`) {
		t.Fatal(string(first.Data), first.Effects)
	}
	firstID := first.Effects[0].ID
	s.Close()
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	a = projectAPI(t, s, root)
	reopened := projectExec(t, a, lookup)
	if !strings.Contains(string(reopened.Data), firstID) || !strings.Contains(string(reopened.Data), "normal UI journey remains unverified") {
		t.Fatal("strategy lost on reopen", string(reopened.Data))
	}
	second := projectExec(t, a, fmt.Sprintf(`mutation { appendKnowledge(key:"project/testing/strategy",body:"Use existing framework; unit checks cover conversion. Browser prerequisites are explicit; normal UI journey remains unverified. Add a supported persistence roundtrip before accepting saved settings.",expectedPreviousId:%q) { id previousId references { key status } } }`, firstID))
	if len(second.Effects) != 1 || second.Effects[0].ID == firstID || !strings.Contains(string(second.Data), firstID) || !strings.Contains(string(second.Data), `"resolved"`) {
		t.Fatal("strategy version/link lost", string(second.Data))
	}
	old := projectExec(t, a, fmt.Sprintf(`{ knowledge(id:%q) { body } }`, firstID))
	if strings.Contains(string(old.Data), "persistence roundtrip") {
		t.Fatal("historical strategy mutated")
	}
	for _, q := range []string{
		`mutation { appendKnowledge(key:"caliper/standards/tests",body:"pretend everything passed",expectedPreviousId:"") { id } }`,
		fmt.Sprintf(`mutation { appendKnowledge(key:"project/testing/strategy",body:"stale overwrite",expectedPreviousId:%q) { id } }`, firstID),
	} {
		if b, err := a.ExecBounded(t.Context(), q, "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatal("invalid knowledge write admitted", string(b))
		}
	}
	foreign := projectExec(t, projectAPI(t, s, t.TempDir()), `{ knowledge(key:"project/testing/strategy") { id body } }`)
	if string(foreign.Data) != `{"knowledge":[]}` {
		t.Fatal("foreign strategy leaked", string(foreign.Data))
	}
	// The standard remains on-demand and does not create stored project notes.
	var list struct{ Knowledge []struct{ Key string } }
	all := projectExec(t, a, `{ knowledge { key } }`)
	if err := json.Unmarshal(all.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Knowledge) != 1 || list.Knowledge[0].Key != "project/testing/strategy" {
		t.Fatalf("guidance or old versions entered current authored inventory: %s", all.Data)
	}
	limits := config.Defaults().Tools.GraphQL
	limits.MaxResultBytes = 200
	if _, err := a.ExecBounded(t.Context(), `{knowledge(key:"caliper/standards/tests"){body}}`, "", nil, limits); err == nil {
		t.Fatal("test standard bypassed existing result budget")
	}
}
