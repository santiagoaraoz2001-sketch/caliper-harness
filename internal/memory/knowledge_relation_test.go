package memory

import (
	"strings"
	"testing"
)

func TestKnowledgeRelationVocabularyMatchesValidation(t *testing.T) {
	for _, relation := range KnowledgeRelations() {
		if err := validateReferences([]Reference{{Key: "project/target", Relation: relation}}); err != nil {
			t.Fatal(relation, err)
		}
	}
	copy := KnowledgeRelations()
	copy[0] = "invented"
	for _, relation := range []string{"invented", "contains", "IMPLEMENTS", ""} {
		err := validateReferences([]Reference{{Key: "project/target", Relation: relation}})
		if err == nil || !strings.Contains(err.Error(), strings.Join(KnowledgeRelations(), ", ")) {
			t.Fatal("invalid relation accepted or help disagrees", relation, err)
		}
	}
}
