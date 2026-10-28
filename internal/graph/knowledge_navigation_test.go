package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestKnowledgeNavigationAndSourceFreshness(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	a := projectAPI(t, s, root)
	path := filepath.Join(root, "engine.go")
	if e := os.WriteFile(path, []byte("package engine\n"), 0600); e != nil {
		t.Fatal(e)
	}
	first := projectExec(t, a, `mutation { appendKnowledge(key:"project/architecture/engine",body:"## ownership\nThe engine owns thrust.\n## tests\nMotion fixture.",filePath:"engine.go",expectedPreviousId:"") { id freshness fileSHA256 } }`)
	if !strings.Contains(string(first.Data), `"freshness":"unchanged"`) {
		t.Fatal(string(first.Data))
	}
	feature := projectExec(t, a, `mutation { appendKnowledge(key:"project/features/flight",body:"## purpose\nMove ship\n## wiring\nThrottle to engine",expectedPreviousId:"",references:[{key:"project/architecture/engine",relation:"implements",section:"ownership"},{key:"caliper/standards/ui",relation:"explains",section:"behavior"}]) { id references { key status } } }`)
	if strings.Count(string(feature.Data), `"resolved"`) != 2 {
		t.Fatal(string(feature.Data))
	}
	for _, q := range []string{`{ knowledge(text:"THRUST",limit:2) { key sections { id body } } }`, `{ knowledge(linkedFrom:"project/features/flight",relation:"implements",limit:2) { key } }`} {
		got := projectExec(t, a, q)
		if !strings.Contains(string(got.Data), "project/architecture/engine") {
			t.Fatal(string(got.Data))
		}
	}
	back := projectExec(t, a, `{ knowledge(linkedTo:"project/architecture/engine",limit:2) { key } }`)
	if !strings.Contains(string(back.Data), "project/features/flight") {
		t.Fatal(string(back.Data))
	}
	part := projectExec(t, a, `{ knowledge(key:"project/architecture/engine",section:"ownership") { body sections { id } } }`)
	if strings.Contains(string(part.Data), "Motion fixture") || !strings.Contains(string(part.Data), "owns thrust") {
		t.Fatal(string(part.Data))
	}
	if e := os.WriteFile(path, []byte("package changed\n"), 0600); e != nil {
		t.Fatal(e)
	}
	changed := projectExec(t, a, `{ knowledge(key:"project/architecture/engine") { freshness } }`)
	if !strings.Contains(string(changed.Data), `"changed"`) {
		t.Fatal(string(changed.Data))
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	missing := projectExec(t, a, `{ knowledge(key:"project/architecture/engine") { freshness } }`)
	if !strings.Contains(string(missing.Data), `"missing"`) {
		t.Fatal(string(missing.Data))
	}
	// Updating prose preserves edges when references is omitted; [] clears them.
	next := projectExec(t, a, fmt.Sprintf(`mutation { appendKnowledge(key:"project/features/flight",body:"Updated purpose",expectedPreviousId:%q) { id references { status } } }`, feature.Effects[0].ID))
	if strings.Count(string(next.Data), `"resolved"`) != 2 {
		t.Fatal(string(next.Data))
	}
	cleared := projectExec(t, a, fmt.Sprintf(`mutation { appendKnowledge(key:"project/features/flight",body:"No links",expectedPreviousId:%q,references:[]) { references { key } } }`, next.Effects[0].ID))
	if !strings.Contains(string(cleared.Data), `"references":[]`) {
		t.Fatal(string(cleared.Data))
	}
	foreign := projectExec(t, projectAPI(t, s, t.TempDir()), `{ knowledge(linkedTo:"project/architecture/engine") { key } }`)
	if string(foreign.Data) != `{"knowledge":[]}` {
		t.Fatal(string(foreign.Data))
	}
}

func TestBuiltinKnowledgeSearchAndWriteProtection(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	r := projectExec(t, a, `{ knowledge(key:"caliper/standards/ui",text:"fonts") { source sections { id body } } }`)
	if !strings.Contains(string(r.Data), `"caliper-default"`) || !strings.Contains(string(r.Data), `"typography"`) || strings.Contains(string(r.Data), "## motion") {
		t.Fatal(string(r.Data))
	}
	for _, q := range []string{`mutation { appendKnowledge(key:"caliper/standards/ui",body:"override",expectedPreviousId:"") { id } }`, `{ knowledge(offset:-1) { id } }`, `{ knowledge(linkedFrom:"a",linkedTo:"b") { id } }`} {
		if b, e := a.ExecBounded(t.Context(), q, "", nil, config.Defaults().Tools.GraphQL); e == nil {
			t.Fatalf("admitted invalid request %s", b)
		}
	}
	// Guidance is available on demand, not silently added to every inventory.
	all := projectExec(t, a, `{ knowledge { key } }`)
	if string(all.Data) != `{"knowledge":[]}` {
		t.Fatal(string(all.Data))
	}
}

func TestKnowledgeLinksCyclesPaginationAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	a := projectAPI(t, s, root)
	projectExec(t, a, `mutation { appendKnowledge(key:"project/a",body:"## state\nalpha",expectedPreviousId:"",references:[{key:"project/b",relation:"depends_on"},{key:"caliper/standards/code",relation:"explains",section:"state"},{key:"missing",relation:"explains"}]){id} }`)
	projectExec(t, a, `mutation { appendKnowledge(key:"project/b",body:"## other\nbeta",expectedPreviousId:"",references:[{key:"project/a",relation:"depends_on",section:"not-present"}]){id} }`)
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a = projectAPI(t, s, root)
	result := projectExec(t, a, `{ knowledge(key:"project/a"){references{key status}} }`)
	if !strings.Contains(string(result.Data), `"missing"`) || strings.Count(string(result.Data), `"resolved"`) != 2 {
		t.Fatal(string(result.Data))
	}
	result = projectExec(t, a, `{knowledge(key:"project/b"){references{status}}}`)
	if !strings.Contains(string(result.Data), `"missing-section"`) {
		t.Fatal(string(result.Data))
	}
	result = projectExec(t, a, `{knowledge(linkedFrom:"project/a",limit:1){key}}`)
	if !strings.Contains(string(result.Data), "caliper/standards/code") {
		t.Fatal(string(result.Data))
	}
	result = projectExec(t, a, `{knowledge(linkedFrom:"project/a",limit:1,offset:1){key}}`)
	if !strings.Contains(string(result.Data), "project/b") {
		t.Fatal(string(result.Data))
	}
	result = projectExec(t, a, `{knowledge(linkedFrom:"project/b",limit:1){key}}`)
	if !strings.Contains(string(result.Data), "project/a") {
		t.Fatal(string(result.Data))
	}
	g := config.Defaults().Tools.GraphQL
	g.MaxResultBytes = 200
	if _, err := a.ExecBounded(t.Context(), `{knowledge(key:"caliper/standards/ui"){body}}`, "", nil, g); err == nil {
		t.Fatal("oversized guidance bypassed budget")
	}
}

func TestKnowledgeReferenceSelectionAvoidsUnrequestedTargetBodies(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:"target",body:%q,expectedPreviousId:""){id}}`, "## state\n"+strings.Repeat("large source ", 400)))
	projectExec(t, a, `mutation{appendKnowledge(key:"source",body:"Links",expectedPreviousId:"",references:[{key:"target",relation:"explains",section:"state"}]){id}}`)
	g := config.Defaults().Tools.GraphQL
	g.MaxResultBytes = 1500
	// Alias and fragment selections must preserve the demand-driven behavior.
	query := `query{knowledge(key:"source"){links:references{...Link}}} fragment Link on KnowledgeReference{key section}`
	if _, err := a.ExecBounded(t.Context(), query, "", nil, g); err != nil {
		t.Fatal(err)
	}
	query = `query{knowledge(key:"source"){links:references{...Link}}} fragment Link on KnowledgeReference{key state:status}`
	if _, err := a.ExecBounded(t.Context(), query, "", nil, g); err == nil {
		t.Fatal("status resolution must retain target source budget")
	}
}
