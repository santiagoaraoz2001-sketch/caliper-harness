package tools

import (
	"strings"
	"testing"

	"specific-harness/internal/memory"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func TestNestedKnowledgeRelationHelpUsesValidatorVocabulary(t *testing.T) {
	reg, _ := catalogTools(t, prompts.Builtin())
	helper, _ := reg.Lookup("help")
	result, err := helper.Execute(t.Context(), tool.Args{"topic": "schema.graphql.KnowledgeReferenceInput"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "relation: String! (allowed: "+strings.Join(memory.KnowledgeRelations(), ", ")+")") {
		t.Fatal("missing canonical relation choices", result.Text)
	}
}
