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
		{"missing-mutation-argument-before-result-fields", `mutation{appendKnowledge(key:"project/design/identity",body:"cream"){id version status}}`, "schema.graphql.appendKnowledge"},
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

func TestGraphQLInputAndAnonymousOperationRecovery(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	def, err := New(api.New(s, "test", t.TempDir()), config.Defaults().Tools.GraphQL, t.TempDir(), "schema-errors", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	badInput := `mutation {appendKnowledge(key:"project/example",body:"x",expectedPreviousId:"",references:[{key:"other"}]){id}}`
	for _, tc := range []struct {
		name, query, operation, want string
		hint                         bool
	}{
		{"missing-input", badInput, "", "schema.graphql.KnowledgeReferenceInput", false},
		{"missing-input-and-name", badInput, "AppendKnowledge", "schema.graphql.KnowledgeReferenceInput", true},
		{"anonymous-name", `mutation {appendKnowledge(key:"project/example",body:"x",expectedPreviousId:""){id}}`, "AppendKnowledge", "operation AppendKnowledge not found", true},
		{"named-mismatch", `mutation Actual {appendKnowledge(key:"project/example",body:"x",expectedPreviousId:""){id}}`, "Wrong", "operation Wrong not found", false},
		{"multiple-named", `query One {version} query Two {version}`, "Wrong", "operation Wrong not found", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := def.Execute(t.Context(), tool.Args{"query": tc.query, "operation_name": tc.operation})
			if err == nil || !result.Failed || result.StateChanged || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "omit operation_name for this anonymous operation") != tc.hint {
				t.Fatalf("wrong recovery: %+v %v", result, err)
			}
		})
	}
	result, err := def.Execute(t.Context(), tool.Args{"query": `{knowledge(key:"project/example"){id}}`})
	if err != nil || !strings.Contains(result.Text, `"knowledge":[]`) {
		t.Fatalf("failed mutation had effects: %+v %v", result, err)
	}
}
