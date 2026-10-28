package graph

import (
	"sort"
	"strings"

	"github.com/agnivade/levenshtein"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"specific-harness/internal/graph/execution"
)

// ModelSchema is the same parsed SDL used by the executable API.
func ModelSchema() *ast.Schema {
	return execution.NewExecutableSchema(execution.Config{}).Schema()
}

// ModelFieldAllowed is shared by documentation and request admission.
func ModelFieldAllowed(name string) bool {
	switch name {
	case "sessions", "session", "addSessionEvent", "addMemory", "__schema", "__type":
		return false
	}
	return true
}

// ModelFields omits host-only operations and compatibility aliases from the index.
// Deprecated aliases remain directly queryable and documented by name.
func ModelFields() []string {
	s := ModelSchema()
	var names []string
	for _, root := range []*ast.Definition{s.Query, s.Mutation} {
		for _, f := range root.Fields {
			if ModelFieldAllowed(f.Name) && !strings.HasPrefix(f.Name, "__") && f.Directives.ForName("deprecated") == nil {
				names = append(names, f.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// ModelOperation resolves a permitted root field without a duplicate API catalog.
func ModelOperation(name string) (*ast.Definition, *ast.FieldDefinition) {
	s := ModelSchema()
	for _, root := range []*ast.Definition{s.Query, s.Mutation} {
		if f := root.Fields.ForName(name); f != nil && ModelFieldAllowed(name) {
			return root, f
		}
	}
	return nil, nil
}

// ModelReference points a malformed request to the closest actual operation.
// It does not repair the request or perform any graph effect.
func ModelReference(query, operationName string) string {
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return "schema.graphql"
	}
	op := doc.Operations.ForName(operationName)
	// Without the validator's failing location, the first field of a mixed
	// document is not evidence of which operation needs repair.
	if op == nil || len(op.SelectionSet) != 1 {
		return "schema.graphql"
	}
	f, ok := op.SelectionSet[0].(*ast.Field)
	if !ok || !ModelFieldAllowed(f.Name) {
		return "schema.graphql"
	}
	if _, field := ModelOperation(f.Name); field != nil {
		return "schema.graphql." + f.Name
	}
	names := ModelFields()
	best := names[0]
	for _, name := range names[1:] {
		if levenshtein.ComputeDistance(f.Name, name) < levenshtein.ComputeDistance(f.Name, best) {
			best = name
		}
	}
	if levenshtein.ComputeDistance(f.Name, best) > 2 {
		return "schema.graphql"
	}
	return "schema.graphql." + best
}
