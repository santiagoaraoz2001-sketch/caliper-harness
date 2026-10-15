package graph

import (
	"context"
	"reflect"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// This isolates receipt selection, not a live prepareTask failure injection.
func TestRollbackReceiptPreservesSettledStepsAndOtherAliases(t *testing.T) {
	b := &requestBudget{effects: []Effect{
		{Key: "sibling", Field: "appendKnowledge", Status: "applied", ID: "saved"},
		{Key: "fresh", Field: "prepareTask", Step: "createPlan", Status: "applied", ID: "plan"},
		{Key: "fresh", Field: "prepareTask", Step: "createApproach", Status: "applied", ID: "approach"},
		{Key: "fresh", Field: "prepareTask", Step: "createTask", Status: "unknown"},
		{Key: "fresh", Field: "prepareTask", Step: "setTaskBlock", Status: "unexecuted"},
		{Key: "other", Field: "transitionPlan", Status: "unknown"},
	}}
	want := append([]Effect(nil), b.effects...)
	want[3].Status = "rolled_back"
	ctx := graphql.WithRootFieldContext(context.Background(), &graphql.RootFieldContext{Object: "Mutation", Field: graphql.CollectedField{Field: &ast.Field{Alias: "fresh", Name: "prepareTask"}}})
	b.rolledBack(ctx)
	if !reflect.DeepEqual(b.effects, want) {
		t.Fatalf("receipt selection: got %+v want %+v", b.effects, want)
	}
}
