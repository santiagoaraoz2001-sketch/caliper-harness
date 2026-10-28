package graph

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	api "specific-harness/internal/graph"
)

// Use the validator's exact failing location and the executable schema's type.
// This is help selection only: no repaired request, graph read or effect replay.
func schemaReference(query, operation string, problems gqlerror.List) string {
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return "schema.graphql"
	}
	locations := map[gqlerror.Location]bool{}
	for _, problem := range problems {
		if problem.Rule == "FieldsOnCorrectType" {
			for _, location := range problem.Locations {
				locations[location] = true
			}
		}
	}
	if len(locations) > 0 {
		reference := ""
		observers := &validator.Events{}
		observers.OnField(func(_ *validator.Walker, field *ast.Field) {
			parent := field.ObjectDefinition
			if reference != "" || field.Definition != nil || parent == nil || field.Position == nil || parent.Name == "Query" || parent.Name == "Mutation" || strings.HasPrefix(parent.Name, "__") {
				return
			}
			if locations[gqlerror.Location{Line: field.Position.Line, Column: field.Position.Column}] {
				reference = "schema.graphql." + parent.Name
			}
		})
		validator.Walk(api.ModelSchema(), doc, observers)
		if reference != "" {
			return reference
		}
	}
	return api.ModelReference(query, operation)
}
