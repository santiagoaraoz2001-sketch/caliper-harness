// Package graph is the embedded state API: schema-first GraphQL executing
// in-process over the Kuzu store. Nothing here listens on a port — loop,
// TUI, and decision model all call Exec as a Go function.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/executor"

	"specific-harness/internal/graph/generated"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// API executes GraphQL operations against one store.
type API struct {
	exec            *executor.Executor
	scope           *projectScope
	lifecycle       func(context.Context, string, string, string) error
	approveContract func(context.Context, string, string) error
}

// New builds the executable schema over s. Version is served by the
// version query; sessionsDir backs the session queries and mutations.
func New(s *store.Store, version, sessionsDir string) *API {
	return NewWithReady(s, version, sessionsDir, nil)
}

// NewWithReady explicitly wires the owning readiness gate; nil refuses Ready.
func NewWithReady(s *store.Store, version, sessionsDir string, ready func(context.Context, string) (*plan.Approach, error)) *API {
	es := generated.NewExecutableSchema(generated.Config{
		Resolvers: &Resolver{store: s, version: version, sessionsDir: sessionsDir, ready: ready},
	})
	exec := executor.New(es)
	exec.Use(introspectionHook{})
	exec.AroundRootFields(func(ctx context.Context, next graphql.RootResolver) graphql.Marshaler {
		if b := budget(ctx); b != nil {
			b.mu.Lock()
			defer b.mu.Unlock()
		}
		if err := ctx.Err(); err != nil {
			graphql.AddError(ctx, err)
			return graphql.Null
		}
		if b := budget(ctx); b != nil {
			b.effect(ctx, "unknown", "")
		}
		return next(ctx)
	})
	exec.AroundFields(func(ctx context.Context, next graphql.Resolver) (any, error) {
		value, err := next(ctx)
		b := budget(ctx)
		fc := graphql.GetFieldContext(ctx)
		if err != nil || b == nil || (!strings.HasPrefix(fc.Object, "__") && !strings.HasPrefix(fc.Field.Name, "__")) {
			return value, err
		}
		b.metadataMu.Lock()
		defer b.metadataMu.Unlock()
		v := reflect.ValueOf(value)
		if !v.IsValid() {
			return value, nil
		}
		rows := 0
		var text string
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return value, nil
			}
			v = v.Elem()
			if v.Kind() == reflect.Struct {
				rows = 1
			}
		}
		switch v.Kind() {
		case reflect.String:
			text = v.String()
		case reflect.Slice, reflect.Array:
			rows = v.Len()
		case reflect.Struct:
			rows = 1
		}
		if err := charge(ctx, rows, text); err != nil {
			return nil, err
		}
		return value, ctx.Err()
	})

	return &API{exec: exec}
}

// Exec runs one operation in-process — no network, ever — and returns the
// data envelope as JSON. Any GraphQL error (bad query, failed resolver)
// comes back as a Go error; partial data on error is discarded.
func (a *API) Exec(ctx context.Context, query, opName string, vars map[string]any) ([]byte, error) {
	result, err := a.execute(ctx, query, opName, vars, nil)
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ExecBounded uses the same typed executor; host raw SessionLog retains Exec.
// Applied mutations remain visible even when selected output or later work fails.
func (a *API) ExecBounded(ctx context.Context, query, opName string, vars map[string]any, limits config.GraphQLConfig) ([]byte, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	n, err := inputSize(vars, limits.MaxRequestBytes-len(query)-len(opName))
	if err != nil {
		return nil, err
	}
	if len(query)+len(opName)+n > limits.MaxRequestBytes {
		return nil, fmt.Errorf("tools.graphql.max_request_bytes exceeded; narrow query or variables")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(limits.TimeoutSeconds)*time.Second)
	defer cancel()
	b := &requestBudget{limits: limits}
	ctx = context.WithValue(ctx, budgetKey{}, b)
	result, err := a.execute(ctx, query, opName, vars, b)
	data, _ := json.Marshal(result)
	if len(data) > limits.MaxResultBytes {
		result.Data = nil
		result.DataOmitted = true
		result.Errors = []string{omittedDetail}
		data, _ = json.Marshal(result)
		err = errors.Join(err, fmt.Errorf("tools.graphql.max_result_bytes exceeded; selected Data omitted, effect receipts retained"))
	}
	if len(data) > limits.MaxResultBytes {
		return nil, errors.Join(err, fmt.Errorf("tools.graphql.max_result_bytes cannot fit error envelope"))
	}
	if err == nil {
		for _, use := range b.sessionUses {
			if useErr := session.MarkUsed(use.dir, use.id); useErr != nil {
				return data, useErr
			}
		}
	}
	return data, err
}
func (a *API) execute(ctx context.Context, query, opName string, vars map[string]any, b *requestBudget) (Result, error) {
	if a.scope != nil {
		ctx = context.WithValue(ctx, projectScopeKey{}, a.scope)
		if a.scope.model {
			ctx = plan.WithProject(ctx, a.scope.root)
		}
	}
	ctx = graphql.StartOperationTrace(ctx)
	now := graphql.Now()
	op, errs := a.exec.CreateOperationContext(ctx, &graphql.RawParams{Query: query, OperationName: opName, Variables: vars, ReadTime: graphql.TraceTiming{Start: now, End: now}})
	if errs != nil {
		return Result{Errors: []string{errs.Error()}}, errs
	}
	if err := ctx.Err(); err != nil {
		return Result{Errors: []string{err.Error()}}, err
	}
	if b != nil {
		n := b.limits.MaxSelections
		if err := countSelections(op, op.Operation.SelectionSet, &n); err != nil {
			return Result{Errors: []string{err.Error()}}, err
		}
		if err := b.prepare(op); err != nil {
			return Result{Errors: []string{err.Error()}}, err
		}
	}
	if a.scope != nil && a.scope.model {
		kind := "Query"
		if op.Operation.Operation == "mutation" {
			kind = "Mutation"
		}
		for _, f := range graphql.CollectFields(op, op.Operation.SelectionSet, []string{kind}) {
			if !ModelFieldAllowed(f.Name) {
				return Result{Errors: []string{"model GraphQL cannot access unassociated sessions or author host/global receipts; use project knowledge"}}, fmt.Errorf("model GraphQL field %s is not permitted", f.Name)
			}
		}
	}
	handler, ctx2 := a.exec.DispatchOperation(ctx, op)
	resp := handler(ctx2)
	result := Result{}
	if b != nil {
		result.Effects = b.effects
	}
	if resp == nil {
		err := errors.Join(fmt.Errorf("operation cancelled"), ctx.Err())
		result.Errors = []string{err.Error()}
		return result, err
	}
	result.Data = resp.Data
	for _, e := range resp.Errors {
		result.Errors = append(result.Errors, e.Error())
	}
	var err error
	if len(resp.Errors) > 0 {
		err = resp.Errors
	}
	err = errors.Join(err, ctx.Err())
	if ctx.Err() != nil {
		result.Errors = append(result.Errors, ctx.Err().Error())
	}
	return result, err
}

// Only the installed introspection interfaces; optional gqlgen transports stay out.
type introspectionHook struct{}

func (introspectionHook) ExtensionName() string                   { return "Introspection" }
func (introspectionHook) Validate(graphql.ExecutableSchema) error { return nil }
func (introspectionHook) MutateOperationContext(_ context.Context, op *graphql.OperationContext) *gqlerror.Error {
	op.DisableIntrospection = false
	return nil
}

// Scoped retains a host-captured project; it never accepts a GraphQL root argument.
type projectScope struct {
	root, sessionRef string
	model            bool
	lifecycle        func(context.Context, string, string, string) error
	approveContract  func(context.Context, string, string) error
}
type projectScopeKey struct{}

func (a *API) Scoped(root, sessionRef string, modelCaller bool) (*API, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("GraphQL needs an absolute captured project root")
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("GraphQL project capture: %w", err)
	}
	return &API{exec: a.exec, scope: &projectScope{root: physical, sessionRef: sessionRef, model: modelCaller, lifecycle: a.lifecycle, approveContract: a.approveContract}}, nil
}
func scope(ctx context.Context) (*projectScope, error) {
	s, _ := ctx.Value(projectScopeKey{}).(*projectScope)
	if s == nil {
		return nil, fmt.Errorf("project scope unavailable; use a host-captured GraphQL API")
	}
	return s, nil
}

// CapturedProject exposes the owned value for approval/receipt binding only.
func (a *API) CapturedProject() string {
	if a.scope == nil {
		return ""
	}
	return a.scope.root
}

// WithLifecycle binds host execution admission without changing public admin operations.
func (a *API) WithLifecycle(check func(context.Context, string, string, string) error) *API {
	value := *a
	value.lifecycle = check
	if value.scope != nil {
		scope := *value.scope
		scope.lifecycle = check
		value.scope = &scope
	}
	return &value
}
func lifecycle(ctx context.Context, kind, id, to string) error {
	s, _ := ctx.Value(projectScopeKey{}).(*projectScope)
	if s != nil && s.model && s.lifecycle != nil {
		return s.lifecycle(ctx, kind, id, to)
	}
	return nil
}

// WithContractApproval binds the shared host gate; authored GraphQL cannot set it.
func (a *API) WithContractApproval(check func(context.Context, string, string) error) *API {
	value := *a
	value.approveContract = check
	if value.scope != nil {
		scope := *value.scope
		scope.approveContract = check
		value.scope = &scope
	}
	return &value
}
func approveContract(ctx context.Context, kind, id string) error {
	s, _ := ctx.Value(projectScopeKey{}).(*projectScope)
	if s != nil && s.approveContract != nil {
		return s.approveContract(ctx, kind, id)
	}
	return nil
}
