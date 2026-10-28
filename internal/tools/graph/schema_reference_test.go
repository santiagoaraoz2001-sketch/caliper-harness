package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestGraphQLNestedFieldErrorNamesActualType(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	def, err := New(api.New(s, "test", t.TempDir()), config.Defaults().Tools.GraphQL, t.TempDir(), "schema-errors", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, query, reference string }{
		{"captured-Plan-children", `{ plan(id:"016c2276-817b-403b-8986-9f60dbfcf248"){ id state title children{ id kind state title } } }`, "schema.graphql.Plan"},
		{"alias", `{ p:plan(id:"missing"){ offspring:children{id} } }`, "schema.graphql.Plan"},
		{"mixed-roots", `{ plans{id} plan(id:"missing"){children{id}} }`, "schema.graphql.Plan"},
		{"fragment", `{plan(id:"missing"){...Broken}} fragment Broken on Plan {children{id}}`, "schema.graphql.Plan"},
		{"inline-fragment", `{plan(id:"missing"){... on Plan {children{id}}}}`, "schema.graphql.Plan"},
		{"nested-Approach", `{plan(id:"missing"){approaches{children{id}}}}`, "schema.graphql.Approach"},
		{"mutation-does-not-apply", `mutation{createPlan(title:"never",goal:"never"){children{id}}}`, "schema.graphql.Plan"},
		{"root-typo-fallback", `{plansz{id}}`, "schema.graphql.plans"},
		{"mixed-unknown-fallback", `{plans{id} unrelatedRoot{id}}`, "schema.graphql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := def.Execute(t.Context(), tool.Args{"query": tc.query})
			if err == nil || !result.Failed || result.StateChanged || !strings.Contains(err.Error(), `help(topic="`+tc.reference+`")`) {
				t.Fatalf("wrong recovery route: %+v %v", result, err)
			}
		})
	}
	result, err := def.Execute(t.Context(), tool.Args{"query": `{plans{id}}`})
	if err != nil || !strings.Contains(result.Text, `"plans":[]`) {
		t.Fatalf("schema repair changed graph: %+v %v", result, err)
	}
}
