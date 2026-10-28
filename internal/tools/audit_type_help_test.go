package tools

import (
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestReturnedTypeHelpNavigatesOnlyRoleReachableTypes(t *testing.T) {
	pc := prompts.Builtin()
	for _, op := range []string{"task", "approach"} {
		text, err := helpGraphQLSchema("."+op, tool.Definition{Name: "graphql", Prompts: pc}, pc)
		if err != nil || !strings.Contains(text, "schema.graphql."+strings.ToUpper(op[:1])+op[1:]) {
			t.Fatalf("missing type link: %s %v", text, err)
		}
	}
	d := tool.Definition{Name: "graphql", Prompts: pc, GraphQLFields: []string{"knowledge"}}
	for _, name := range []string{"Knowledge", "KnowledgeReference", "KnowledgeSection"} {
		text, err := helpGraphQLSchema("."+name, d, pc)
		if err != nil || text == pc.Text("help.unavailable") {
			t.Fatalf("reachable type %s: %q %v", name, text, err)
		}
	}
	for _, name := range []string{"Task", "Session", "Query", "Mutation", "KnowledgeReferenceInput"} {
		text, err := helpGraphQLSchema("."+name, d, pc)
		if err != nil || text != pc.Text("help.unavailable") {
			t.Fatalf("role leaked %s: %q %v", name, text, err)
		}
	}
}
