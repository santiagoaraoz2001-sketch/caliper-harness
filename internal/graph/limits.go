package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"specific-harness/internal/config"
	"specific-harness/internal/store"
	"strings"
	"sync"
)

type Effect struct {
	Key    string `json:"key"`
	Field  string `json:"field"`
	Status string `json:"status"`
	Step   string `json:"step,omitempty"`
	ID     string `json:"id,omitempty"`
}
type Result struct {
	Data        json.RawMessage `json:"data,omitempty"`
	Errors      []string        `json:"errors,omitempty"`
	Effects     []Effect        `json:"effects,omitempty"`
	DataOmitted bool            `json:"data_omitted,omitempty"`
}
type budgetKey struct{}
type requestBudget struct {
	mu          sync.Mutex
	metadataMu  sync.Mutex
	limits      config.GraphQLConfig
	rows, bytes int
	effects     []Effect
	sessionUses []struct{ dir, id string }
}

func budget(ctx context.Context) *requestBudget {
	b, _ := ctx.Value(budgetKey{}).(*requestBudget)
	return b
}

const omittedDetail = "GraphQL selected data and error detail omitted; effect receipts retained."

// Reject oversized raw typed input before the standard encoder allocates escapes.
// The admitted raw strings/structure bound encoded temporary storage to at most
// six times the request ceiling; json.Marshal owns the exact JSON byte count.
func inputSize(v any, remaining int) (int, error) {
	ceiling := remaining
	if err := rawInputSize(v, &remaining); err != nil {
		return 0, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	if len(data) > ceiling {
		return 0, fmt.Errorf("tools.graphql.max_request_bytes exceeded; narrow query or variables")
	}
	return len(data), nil
}
func rawInputSize(v any, remaining *int) error {
	if *remaining < 0 {
		return fmt.Errorf("tools.graphql.max_request_bytes exceeded; narrow query or variables")
	}
	switch x := v.(type) {
	case string:
		*remaining -= len(x) + 2
	case map[string]any:
		*remaining -= 2
		for k, value := range x {
			*remaining -= 1 // colon
			if err := rawInputSize(k, remaining); err != nil {
				return err
			}
			if err := rawInputSize(value, remaining); err != nil {
				return err
			}
		}
		if len(x) > 0 {
			*remaining -= len(x) - 1
		}
	case []any:
		*remaining -= 2
		for _, value := range x {
			if err := rawInputSize(value, remaining); err != nil {
				return err
			}
		}
		if len(x) > 0 {
			*remaining -= len(x) - 1
		}
	case json.Number:
		*remaining -= len(x)
	case nil, bool, int, int64, float64:
		data, err := json.Marshal(x)
		if err != nil {
			return err
		}
		*remaining -= len(data)
	default:
		return fmt.Errorf("GraphQL variables must be typed JSON values, got %T", v)
	}
	if *remaining < 0 {
		return fmt.Errorf("tools.graphql.max_request_bytes exceeded; narrow query or variables")
	}
	return nil
}
func countSelections(op *graphql.OperationContext, set ast.SelectionSet, remaining *int) error {
	for _, sel := range set {
		var dirs ast.DirectiveList
		switch x := sel.(type) {
		case *ast.Field:
			dirs = x.Directives
		case *ast.FragmentSpread:
			dirs = x.Directives
		case *ast.InlineFragment:
			dirs = x.Directives
		}
		skip := false
		for _, d := range dirs {
			if d.Name == "skip" || d.Name == "include" {
				v, err := d.Arguments.ForName("if").Value.Value(op.Variables)
				if err != nil {
					return err
				}
				if (d.Name == "skip" && v == true) || (d.Name == "include" && v == false) {
					skip = true
				}
			}
		}
		if skip {
			continue
		}
		var children ast.SelectionSet
		switch x := sel.(type) {
		case *ast.Field:
			*remaining--
			if *remaining < 0 {
				return fmt.Errorf("tools.graphql.max_selections exceeded; narrow aliases or fragments")
			}
			children = x.SelectionSet
		case *ast.FragmentSpread:
			children = op.Doc.Fragments.ForName(x.Name).SelectionSet
		case *ast.InlineFragment:
			children = x.SelectionSet
		}
		if err := countSelections(op, children, remaining); err != nil {
			return err
		}
	}
	return nil
}
func (b *requestBudget) prepare(op *graphql.OperationContext) error {
	if op.Operation.Operation == ast.Mutation {
		for _, f := range graphql.CollectFields(op, op.Operation.SelectionSet, []string{"Mutation"}) {
			if f.Name != "__typename" {
				id := strings.Repeat("0", 36)
				args := f.ArgumentMap(op.Variables)
				for _, key := range []string{"id", "sessionId"} {
					if value, ok := args[key]; ok {
						var err error
						id, err = graphql.UnmarshalID(value)
						if err != nil {
							return err
						}
						break
					}
				}
				steps := []string{""}
				if f.Name == "prepareTask" {
					steps = []string{"createPlan", "createApproach", "createTask", "setTaskBlock"}
				}
				for _, step := range steps {
					// Reserve the longest possible status before admitting any effect.
					b.effects = append(b.effects, Effect{Key: f.Alias, Field: f.Name, Step: step, Status: "rolled_back", ID: id})
				}
			}
		}
	}
	if len(b.effects) > b.limits.MaxRows {
		return fmt.Errorf("tools.graphql.max_rows cannot admit mutation receipts")
	}
	data, _ := json.Marshal(Result{Effects: b.effects, Errors: []string{omittedDetail}, DataOmitted: true})
	if len(data) > b.limits.MaxResultBytes {
		return fmt.Errorf("tools.graphql.max_result_bytes cannot fit essential receipts; narrow mutations")
	}
	b.bytes = b.limits.MaxResultBytes - len(data)
	b.rows = b.limits.MaxRows - len(b.effects)
	for i := range b.effects {
		b.effects[i].ID = ""
		b.effects[i].Status = "unexecuted"
	}
	return nil
}
func (b *requestBudget) effect(ctx context.Context, status, id string) {
	b.effectStep(ctx, "", status, id)
}

// transactionError is called immediately after one owning store operation,
// never for a multi-operation host readiness/approval hook or output projection.
func transactionError(ctx context.Context, err error) error {
	var txErr *store.TransactionError
	if b := budget(ctx); b != nil && errors.As(err, &txErr) && txErr.RollbackConfirmed() {
		b.rolledBack(ctx)
	}
	return err
}

// Only the unsettled effect in this root field belongs to the failed
// transaction. Earlier prepareTask steps and sibling mutations may be committed.
func (b *requestBudget) rolledBack(ctx context.Context) {
	fc := graphql.GetRootFieldContext(ctx)
	if fc == nil {
		return
	}
	for i := range b.effects {
		if b.effects[i].Key == fc.Field.Alias && b.effects[i].Status == "unknown" {
			b.effects[i].Status = "rolled_back"
		}
	}
}
func (b *requestBudget) effectStep(ctx context.Context, step, status, id string) {
	fc := graphql.GetRootFieldContext(ctx)
	if fc == nil {
		return
	}
	for i := range b.effects {
		if b.effects[i].Key == fc.Field.Alias && b.effects[i].Step == step {
			b.effects[i].Status = status
			b.effects[i].ID = id
			return
		}
	}
}
func applied(ctx context.Context, id string) {
	if b := budget(ctx); b != nil {
		b.effect(ctx, "applied", id)
	}
}
func available(ctx context.Context) (int, int) {
	if b := budget(ctx); b != nil {
		return b.rows + 1, b.bytes
	}
	return 0, 0
}
func charge(ctx context.Context, rows int, values ...string) error {
	b := budget(ctx)
	if b == nil {
		return nil
	}
	b.rows -= rows
	for _, v := range values {
		b.bytes -= len(v)
	}
	if b.rows < 0 {
		return fmt.Errorf("tools.graphql.max_rows exceeded; narrow selected lists")
	}
	if b.bytes < 0 {
		return fmt.Errorf("tools.graphql.max_result_bytes source budget exceeded; select narrower fields")
	}
	return nil
}
func selected(ctx context.Context, set ast.SelectionSet, kind string) []graphql.CollectedField {
	return graphql.CollectFields(graphql.GetOperationContext(ctx), set, []string{kind})
}
func selectedNames(fields []graphql.CollectedField) []string {
	out := []string{}
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}
