package tools

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	generated "specific-harness/internal/graph/execution"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func TestKnowledgeHelpProgressiveDisclosure(t *testing.T) {
	reg, cfg := catalogTools(t, prompts.Builtin())
	h := helpDefinition(cfg, reg, prompts.Builtin())
	res, err := h.Execute(t.Context(), tool.Args{"topic": "graphql"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "prepareTask(input:") || strings.Contains(res.Text, "__type(name:") {
		t.Fatal("basic knowledge help contains advanced lifecycle recipes")
	}
	if len(res.Text) > 5500 {
		t.Fatalf("basic help grew to %d bytes", len(res.Text))
	}
	schema := generated.NewExecutableSchema(generated.Config{}).Schema()
	for remaining := res.Text; ; {
		_, tail, found := strings.Cut(remaining, "```graphql\n")
		if !found {
			break
		}
		query, rest, closed := strings.Cut(tail, "```")
		if !closed {
			t.Fatal("unclosed example")
		}
		if _, errors := gqlparser.LoadQuery(schema, query); len(errors) != 0 {
			t.Fatal(errors)
		}
		remaining = rest
	}
	for _, topic := range []string{"graphql.knowledge", "graphql.lifecycle", "schema.graphql.knowledge"} {
		res, err := h.Execute(t.Context(), tool.Args{"topic": topic})
		if err != nil {
			t.Fatalf("%s: %v", topic, err)
		}
		if !strings.Contains(res.Text, "knowledge") {
			t.Fatal("missing knowledge guidance", topic)
		}
		if topic == "schema.graphql.knowledge" {
			query := nativeHelpTemplate(t, res.Text).Arguments["query"].(string)
			if !strings.Contains(query, `limit:3`) || !strings.Contains(query, "sections { id title }") || strings.Contains(query, "body") {
				t.Fatal("unhelpful generated lookup", query)
			}
		}
	}
}
