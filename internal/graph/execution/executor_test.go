package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/executor"
	gqlparser "github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"specific-harness/internal/graph/model"
	"specific-harness/schema"
)

type roots struct {
	query    *queryProbe
	mutation *mutationProbe
}

func (r roots) Query() QueryResolver       { return r.query }
func (r roots) Mutation() MutationResolver { return r.mutation }

type queryProbe struct {
	QueryResolver
	calls int
	plans []*model.Plan
}

func (r *queryProbe) Version(context.Context) (string, error) {
	r.calls++
	return "version", nil
}

func (r *queryProbe) Plans(context.Context, *string) ([]*model.Plan, error) {
	return r.plans, nil
}

type mutationProbe struct {
	MutationResolver
	calls int
}

func (r *mutationProbe) CreatePlan(_ context.Context, title, goal string) (*model.Plan, error) {
	r.calls++
	return &model.Plan{Title: title, Goal: goal}, nil
}

func execute(t *testing.T, cfg Config, query string) *graphql.Response {
	t.Helper()
	exec := executor.New(NewExecutableSchema(cfg))
	ctx := graphql.StartOperationTrace(context.Background())
	op, errors := exec.CreateOperationContext(ctx, &graphql.RawParams{Query: query})
	if len(errors) != 0 {
		t.Fatalf("query rejected: %s", errors)
	}
	handler, ctx := exec.DispatchOperation(ctx, op)
	return handler(ctx)
}

func TestSDLArgumentOrderCannotSwapSameTypedResolverArguments(t *testing.T) {
	sdl := strings.Replace(schema.SDL, "createPlan(title: String!, goal: String!)", "createPlan(goal: String!, title: String!)", 1)
	if sdl == schema.SDL {
		t.Fatal("fixture did not reorder the SDL arguments")
	}
	r := roots{query: &queryProbe{}, mutation: &mutationProbe{}}
	cfg := Config{Schema: gqlparser.MustLoadSchema(&ast.Source{Input: sdl}), Resolvers: r}
	response := execute(t, cfg, `mutation { createPlan(title:"TITLE", goal:"GOAL") { title goal } }`)
	if len(response.Errors) != 0 || string(response.Data) != `{"createPlan":{"title":"TITLE","goal":"GOAL"}}` || r.mutation.calls != 1 {
		t.Fatalf("resolver argument identity changed: %s errors=%v calls=%d", response.Data, response.Errors, r.mutation.calls)
	}
}

func TestMergedSelectionsAndDirectivesDispatchOnlyOnce(t *testing.T) {
	r := roots{query: &queryProbe{}, mutation: &mutationProbe{}}
	cfg := Config{Resolvers: r}
	response := execute(t, cfg, `query($show:Boolean=false) { same:version ...V hidden:version @include(if:$show) skipped:version @skip(if:true) } fragment V on Query { same:version }`)
	if len(response.Errors) != 0 || string(response.Data) != `{"same":"version"}` || r.query.calls != 1 {
		t.Fatalf("merged/directive reads dispatched incorrectly: %s errors=%v calls=%d", response.Data, response.Errors, r.query.calls)
	}
	response = execute(t, cfg, `mutation { same:createPlan(title:"T", goal:"G") {title} same:createPlan(title:"T", goal:"G") {goal} skipped:createPlan(title:"SKIP",goal:"SKIP") @skip(if:true) {title} }`)
	if len(response.Errors) != 0 || string(response.Data) != `{"same":{"title":"T","goal":"G"}}` || r.mutation.calls != 1 {
		t.Fatalf("merged/directive writes dispatched incorrectly: %s errors=%v calls=%d", response.Data, response.Errors, r.mutation.calls)
	}
}

func TestNonNullListElementBubblesWithExactIndex(t *testing.T) {
	r := roots{query: &queryProbe{plans: []*model.Plan{{ID: "one"}, nil}}, mutation: &mutationProbe{}}
	response := execute(t, Config{Resolvers: r}, `{plans {id}}`)
	if string(response.Data) != "null" || len(response.Errors) != 1 {
		t.Fatalf("missing non-null propagation: %s errors=%v", response.Data, response.Errors)
	}
	path, err := json.Marshal(response.Errors[0].Path)
	if err != nil || string(path) != `["plans",1]` {
		t.Fatalf("wrong error path: %s err=%v", path, err)
	}
}
