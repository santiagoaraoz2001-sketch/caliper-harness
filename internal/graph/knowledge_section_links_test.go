package graph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/store"
)

func TestKnowledgeSourceSectionLinksPersistAndRemainBounded(t *testing.T) {
	path, root := filepath.Join(t.TempDir(), "db"), t.TempDir()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	a := projectAPI(t, s, root)
	projectExec(t, a, `mutation{appendKnowledge(key:"target",body:"## example\nKnown outcome",expectedPreviousId:""){id}}`)
	projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:"large",body:%q,expectedPreviousId:""){id}}`, "## example\n"+strings.Repeat("unrelated content ", 500)))
	created := projectExec(t, a, `mutation{appendKnowledge(key:"source",body:"## choose\nChoose outcome\n## execute\nExecute fixture",expectedPreviousId:"",references:[{key:"target",relation:"explains",section:"example",sourceSection:"choose",label:"Derive the oracle 🚚"},{key:"target",relation:"explains",section:"example",sourceSection:"execute",label:"Apply it"},{key:"target",relation:"depends_on",label:"Legacy whole document"},{key:"large",relation:"explains",section:"example",sourceSection:"execute",label:"Unrelated"}]){id}}`)
	// Omitted references preserve exact source anchor/label; explicit [] clears.
	changed := projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:"source",body:"## choose\nChoose outcome\n## execute\nExecute fixture",expectedPreviousId:%q){id}}`, created.Effects[0].ID))
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a = projectAPI(t, s, root)
	q := `query{knowledge(key:"source",section:"choose"){key links:references{...Link} sections{id links:references{...Link}}}} fragment Link on KnowledgeReference{key section sourceSection label status}`
	g := config.Defaults().Tools.GraphQL
	g.MaxResultBytes = 2200
	got, err := a.ExecBounded(t.Context(), q, "", nil, g)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(got)
	if strings.Contains(raw, "Unrelated") || strings.Contains(raw, "Apply it") || !strings.Contains(raw, "Derive the oracle 🚚") || !strings.Contains(raw, "Legacy whole document") {
		t.Fatal(raw)
	}
	// The unrelated large target must not be loaded just to resolve selected links.
	if _, err = a.ExecBounded(t.Context(), strings.Replace(q, `section:"choose"`, `section:"execute"`, 1), "", nil, g); err == nil {
		t.Fatal("selected large target escaped status source budget")
	}
	foreign := projectExec(t, projectAPI(t, s, t.TempDir()), `{knowledge(key:"source"){sections{references{key}}}}`)
	if string(foreign.Data) != `{"knowledge":[]}` {
		t.Fatal(string(foreign.Data))
	}
	removed := projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:"source",body:"## new-heading\nChanged structure",expectedPreviousId:%q){id references{sourceSection status}}}`, changed.Effects[0].ID))
	if !strings.Contains(string(removed.Data), `"missing-source-section"`) {
		t.Fatal(string(removed.Data))
	}
	cleared := projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:"source",body:"## choose\nNo links",expectedPreviousId:%q,references:[]){references{key} sections{references{key}}}}`, removed.Effects[0].ID))
	if strings.Contains(string(cleared.Data), `"key":"target"`) {
		t.Fatal(string(cleared.Data))
	}
}

func TestBuiltinSectionLinksArePublicNavigableAndBounded(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	// Exercise real authored catalogs, not synthetic nodes inserted by a test.
	for _, root := range []string{memory.UIStandardKey, memory.TestsStandardKey, memory.CodeStandardKey} {
		t.Run(root, func(t *testing.T) {
			refs := memory.BuiltinReferences(root, true)
			if len(refs) == 0 {
				t.Fatalf("%s has no authored deep links", root)
			}
			ref := refs[0]
			if ref.SourceSection == "" || ref.Label == "" || ref.Status != "resolved" {
				t.Fatal(ref)
			}
			query := fmt.Sprintf(`{knowledge(key:%q,section:%q){source sections{id references{key section sourceSection label status}}}}`, root, ref.SourceSection)
			first := projectExec(t, a, query)
			if !strings.Contains(string(first.Data), ref.Key) {
				t.Fatal(string(first.Data))
			}
			var data struct {
				Knowledge []struct {
					Sections []struct {
						References []struct{ SourceSection string }
					}
				}
			}
			if err := json.Unmarshal(first.Data, &data); err != nil {
				t.Fatal(err)
			}
			for _, k := range data.Knowledge {
				for _, section := range k.Sections {
					for _, r := range section.References {
						if r.SourceSection != ref.SourceSection {
							t.Fatalf("unrelated source link: %+v", r)
						}
					}
				}
			}
			second := projectExec(t, a, fmt.Sprintf(`{knowledge(key:%q,section:%q){key sections{id body references{key section label}}}}`, ref.Key, ref.Section))
			if !strings.Contains(string(second.Data), ref.Key) {
				t.Fatal(string(second.Data))
			}
			var hops struct {
				Knowledge []struct {
					Sections []struct {
						References []struct{ Key, Section, Label string }
					}
				}
			}
			if err := json.Unmarshal(second.Data, &hops); err != nil {
				t.Fatal(err)
			}
			followed := false
			for _, node := range hops.Knowledge {
				for _, part := range node.Sections {
					for _, hop := range part.References {
						if followed {
							continue
						}
						third := projectExec(t, a, fmt.Sprintf(`{knowledge(key:%q,section:%q){key source sections{id body}}}`, hop.Key, hop.Section))
						if !strings.Contains(string(third.Data), hop.Key) || !strings.Contains(string(third.Data), `"body":`) || hop.Label == "" {
							t.Fatalf("broken authored second hop: %s", third.Data)
						}
						followed = true
					}
				}
			}
			if !followed {
				t.Fatalf("selected %s detail has no useful next link", root)
			}
			back := projectExec(t, a, fmt.Sprintf(`{knowledge(linkedTo:%q){key}}`, ref.Key))
			if !strings.Contains(string(back.Data), root) {
				t.Fatal(string(back.Data))
			}
			forward := projectExec(t, a, fmt.Sprintf(`{knowledge(linkedFrom:%q){key}}`, root))
			if !strings.Contains(string(forward.Data), ref.Key) {
				t.Fatal(string(forward.Data))
			}
			g := config.Defaults().Tools.GraphQL
			g.MaxResultBytes = 64
			if _, err := a.ExecBounded(t.Context(), query, "", nil, g); err == nil {
				t.Fatal("nested references bypassed output budget")
			}
		})
	}
}

func TestSourceAnchorStatusReadsBodyOnlyWhenRequired(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	projectExec(t, a, `mutation{appendKnowledge(key:"target",body:"Target",expectedPreviousId:""){id}}`)
	large := "## named\n" + strings.Repeat("source content ", 400)
	for _, anchor := range []string{"", "named"} {
		key := "source" + anchor
		projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:%q,body:%q,expectedPreviousId:"",references:[{key:"target",relation:"explains",sourceSection:%q}]){id}}`, key, large, anchor))
		g := config.Defaults().Tools.GraphQL
		g.MaxResultBytes = 1500
		if _, err := a.ExecBounded(t.Context(), fmt.Sprintf(`{knowledge(key:%q){references{key sourceSection}}}`, key), "", nil, g); err != nil {
			t.Fatalf("metadata fetched body: %v", err)
		}
		_, err := a.ExecBounded(t.Context(), fmt.Sprintf(`{knowledge(key:%q){references{status}}}`, key), "", nil, g)
		if anchor == "" && err != nil {
			t.Fatalf("legacy document reference unnecessarily loaded source: %v", err)
		}
		if anchor != "" && err == nil {
			t.Fatal("source heading validation bypassed source budget")
		}
	}
}
