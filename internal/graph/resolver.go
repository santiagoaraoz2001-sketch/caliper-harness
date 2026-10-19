package graph

// Owned by hand (gqlgen.yml: preserve_resolver). Regen never touches this
// file: the Resolver struct keeps its fields, and schema changes surface
// as compiler errors here, not silent stub rewrites.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"specific-harness/internal/graph/generated"
	"specific-harness/internal/graph/model"
	"specific-harness/internal/memory"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// Resolver wires the generated schema to the store. Resolvers stay thin:
// every statement lives in the owning package (memory, session), never here.
type Resolver struct {
	store       *store.Store
	version     string
	sessionsDir string
	ready       func(context.Context, string) (*plan.Approach, error)
}

// AddMemory is the resolver for the addMemory field.
func (r *mutationResolver) AddMemory(ctx context.Context, kind string, body string, sessionRef string) (*model.MemoryEntry, error) {
	e, err := memory.AddContext(ctx, r.store, kind, body, sessionRef)
	if err != nil {
		return nil, err
	}
	applied(ctx, e.ID)
	return toModel(e), nil
}

// Version is the resolver for the version field.
func (r *queryResolver) Version(ctx context.Context) (string, error) {
	return r.version, nil
}

// Memories is the resolver for the memories field.
func (r *queryResolver) Memories(ctx context.Context, kind *string, text *string, limit *int) ([]*model.MemoryEntry, error) {
	var k, t string
	var n int
	if kind != nil {
		k = *kind
	}
	if text != nil {
		t = *text
	}
	if limit != nil {
		n = *limit
	}
	rows, bytes := available(ctx)
	if b := budget(ctx); b != nil {
		if bytes <= 0 {
			return nil, fmt.Errorf("GraphQL source budget exhausted")
		}
		if n <= 0 || n > rows {
			n = rows
		}
	}
	body := false
	for _, f := range selected(ctx, rootSelection(ctx), "MemoryEntry") {
		if f.Name == "body" {
			body = true
		}
	}
	entries, err := memory.GraphList(ctx, r.store, k, t, n, bytes, body)
	if err != nil {
		return nil, err
	}
	if err := charge(ctx, len(entries)); err != nil {
		return nil, err
	}
	out := make([]*model.MemoryEntry, 0, len(entries))
	for _, e := range entries {
		if err := charge(ctx, 0, e.ID, e.Kind, e.Body, e.SessionRef); err != nil {
			return nil, err
		}
		out = append(out, toModel(e))
	}
	return out, nil
}

// AddSessionEvent is the resolver for the addSessionEvent field.
func (r *mutationResolver) AddSessionEvent(ctx context.Context, sessionID string, kind string, text string) (*model.SessionEvent, error) {
	e := session.Event{Time: time.Now().UTC(), Kind: kind, Text: text}
	if err := session.Append(r.sessionsDir, sessionID, e); err != nil {
		return nil, err
	}
	applied(ctx, sessionID)
	return toEvent(e), nil
}

// Sessions is the resolver for the sessions field.
func (r *queryResolver) Sessions(ctx context.Context) ([]*model.Session, error) {
	var ids []string
	var err error
	if b := budget(ctx); b != nil {
		ids, err = session.ListBounded(ctx, r.sessionsDir, b.rows)
	} else {
		ids, err = session.List(r.sessionsDir)
	}
	if err != nil {
		return nil, err
	}
	out := []*model.Session{}
	for _, id := range ids {
		s, err := r.readSession(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
func (r *queryResolver) Session(ctx context.Context, id string) (*model.Session, error) {
	s, err := r.readSession(ctx, id)
	if err == nil && s != nil {
		err = session.MarkUsed(r.sessionsDir, id)
	}
	return s, err
}

// Aggregate browsing shares the bounded reader without refreshing last use.
func (r *queryResolver) readSession(ctx context.Context, id string) (*model.Session, error) {
	exists, err := session.Exists(ctx, r.sessionsDir, id)
	if err != nil || !exists {
		return nil, err
	}
	if err := charge(ctx, 1, id); err != nil {
		return nil, err
	}
	set, mult := childSelection(ctx, rootSelection(ctx), "Session", "events")
	_ = set
	if mult == 0 {
		return &model.Session{ID: id, Events: []*model.SessionEvent{}}, nil
	}
	var events []session.Event
	if b := budget(ctx); b != nil {
		events, err = session.ReadBounded(ctx, r.sessionsDir, id, b.rows/mult, b.bytes/mult)
	} else {
		events, err = session.Read(r.sessionsDir, id)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		for i := 0; i < mult; i++ {
			if err := charge(ctx, 1, e.Kind, e.Text, e.Time.Format(time.RFC3339)); err != nil {
				return nil, err
			}
		}
	}
	return toSession(id, events), nil
}

func toSession(id string, events []session.Event) *model.Session {
	out := make([]*model.SessionEvent, 0, len(events))
	for _, e := range events {
		out = append(out, toEvent(e))
	}
	return &model.Session{ID: id, Events: out}
}

func toEvent(e session.Event) *model.SessionEvent {
	return &model.SessionEvent{Time: e.Time.Format(time.RFC3339), Kind: e.Kind, Text: e.Text}
}

// PrepareTask performs four existing owning writes; its receipts are not a rollback promise.
func (r *mutationResolver) PrepareTask(ctx context.Context, input model.FreshTaskInput) (*model.PreparedTask, error) {
	for _, f := range []struct{ name, value string }{
		{"planTitle", input.PlanTitle}, {"planGoal", input.PlanGoal}, {"approachTitle", input.ApproachTitle},
		{"taskTitle", input.TaskTitle}, {"taskPrompt", input.TaskPrompt}, {"checkCommand", input.CheckCommand}, {"acceptanceCommand", input.AcceptanceCommand},
	} {
		if strings.TrimSpace(f.value) == "" {
			return nil, fmt.Errorf("prepareTask field %q is blank; author every input before preparation", f.name)
		}
	}
	if err := plan.ValidateTaskBlock(&plan.Task{Current: input.Current, Target: input.Target, Sketch: input.Sketch, Acceptance: input.Acceptance, Tests: input.Tests, Verify: input.Verify, AcceptanceCommand: input.AcceptanceCommand}); err != nil {
		return nil, err
	}
	mark := func(step, status, id string) {
		if b := budget(ctx); b != nil {
			b.effectStep(ctx, step, status, id)
		}
	}
	begin := func(step string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		mark(step, "unknown", "")
		return nil
	}
	if err := begin("createPlan"); err != nil {
		return nil, err
	}
	p, err := plan.CreatePlanContext(ctx, r.store, input.PlanTitle, input.PlanGoal)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	mark("createPlan", "applied", p.ID)
	if err := begin("createApproach"); err != nil {
		return nil, err
	}
	a, err := plan.CreateApproachContext(ctx, r.store, p.ID, input.ApproachTitle)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	mark("createApproach", "applied", a.ID)
	if err := begin("createTask"); err != nil {
		return nil, err
	}
	t, err := plan.CreateTaskContext(ctx, r.store, a.ID, input.TaskTitle, input.TaskPrompt)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	mark("createTask", "applied", t.ID)
	if err := begin("setTaskBlock"); err != nil {
		return nil, err
	}
	if _, err = plan.SetTaskBlockContext(ctx, r.store, t.ID, input.Current, input.Target, input.Sketch, input.Acceptance, input.Tests, input.Verify, input.CheckCommand, input.AcceptanceCommand); err != nil {
		return nil, transactionError(ctx, err)
	}
	mark("setTaskBlock", "applied", t.ID)
	out := &model.PreparedTask{}
	set, n := childSelection(ctx, rootSelection(ctx), "PreparedTask", "plan")
	for i := 0; i < n; i++ {
		out.Plan, err = r.planModel(ctx, p.ID, "", "", set, i == 0)
		if err != nil {
			return nil, err
		}
	}
	set, n = childSelection(ctx, rootSelection(ctx), "PreparedTask", "approach")
	for i := 0; i < n; i++ {
		out.Approach, err = r.approachModel(ctx, a.ID, set, i == 0)
		if err != nil {
			return nil, err
		}
	}
	set, n = childSelection(ctx, rootSelection(ctx), "PreparedTask", "task")
	for i := 0; i < n; i++ {
		out.Task, err = r.taskModel(ctx, t.ID, set, i == 0)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CreatePlan is the resolver for the createPlan field.
func (r *mutationResolver) CreatePlan(ctx context.Context, title string, goal string) (*model.Plan, error) {
	p, err := plan.CreatePlanContext(ctx, r.store, title, goal)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, p.ID)
	return r.planModel(ctx, p.ID, "", "", rootSelection(ctx), true)
}

// CreateApproach is the resolver for the createApproach field.
func (r *mutationResolver) CreateApproach(ctx context.Context, planID string, title string) (*model.Approach, error) {
	a, err := plan.CreateApproachContext(ctx, r.store, planID, title)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, a.ID)
	return r.approachModel(ctx, a.ID, rootSelection(ctx), true)
}

// CreateTask is the resolver for the createTask field.
func (r *mutationResolver) CreateTask(ctx context.Context, approachID string, title string, prompt string) (*model.Task, error) {
	t, err := plan.CreateTaskContext(ctx, r.store, approachID, title, prompt)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, t.ID)
	return r.taskModel(ctx, t.ID, rootSelection(ctx), true)
}

// TransitionPlan is the resolver for the transitionPlan field.
func (r *mutationResolver) TransitionPlan(ctx context.Context, id string, to string) (*model.Plan, error) {
	if to == plan.PlanActive {
		if e := approveContract(ctx, "plan", id); e != nil {
			return nil, e
		}
	}
	if err := lifecycle(ctx, "plan", id, to); err != nil {
		return nil, err
	}
	p, err := plan.TransitionPlanContext(ctx, r.store, id, to)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, p.ID)
	return r.planModel(ctx, p.ID, "", "", rootSelection(ctx), true)
}

// TransitionApproach is the resolver for the transitionApproach field.
func (r *mutationResolver) TransitionApproach(ctx context.Context, id string, to string) (*model.Approach, error) {
	if err := lifecycle(ctx, "approach", id, to); err != nil {
		return nil, err
	}
	var a *plan.Approach
	var err error
	if to == plan.ApproachReady {
		p, e := plan.ApproachPlan(r.store, id)
		if e != nil {
			return nil, e
		}
		if e = approveContract(ctx, "plan", p.ID); e != nil {
			return nil, e
		}
		if e = approveContract(ctx, "approach", id); e != nil {
			return nil, e
		}
		tasks, e := plan.ApproachTasks(r.store, id)
		if e != nil {
			return nil, e
		}
		for _, t := range tasks {
			if e = approveContract(ctx, "task", t.ID); e != nil {
				return nil, e
			}
		}
		if r.ready == nil {
			return nil, fmt.Errorf("ready has no checker — use graph.NewWithReady")
		}
		a, err = r.ready(ctx, id)
	} else {
		a, err = plan.TransitionApproachCheckedContext(ctx, r.store, id, to, "")
		if err != nil {
			return nil, transactionError(ctx, err)
		}
	}
	if err != nil {
		return nil, err
	}
	applied(ctx, a.ID)
	return r.approachModel(ctx, a.ID, rootSelection(ctx), true)
}

// TransitionTask is the resolver for the transitionTask field.
func (r *mutationResolver) TransitionTask(ctx context.Context, id string, to string) (*model.Task, error) {
	if err := lifecycle(ctx, "task", id, to); err != nil {
		return nil, err
	}
	t, err := plan.TransitionTaskContext(ctx, r.store, id, to)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, t.ID)
	return r.taskModel(ctx, t.ID, rootSelection(ctx), true)
}

// RetirePlan is the resolver for the retirePlan field.
func (r *mutationResolver) RetirePlan(ctx context.Context, id string) (*model.Plan, error) {
	p, err := plan.TransitionPlanContext(ctx, r.store, id, plan.PlanRetired)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, p.ID)
	return r.planModel(ctx, p.ID, "", "", rootSelection(ctx), true)
}

// RetireApproach is the resolver for the retireApproach field.
func (r *mutationResolver) RetireApproach(ctx context.Context, id string) (*model.Approach, error) {
	a, err := plan.TransitionApproachCheckedContext(ctx, r.store, id, plan.ApproachRetired, "")
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, a.ID)
	return r.approachModel(ctx, a.ID, rootSelection(ctx), true)
}

// SetTaskBlock is the resolver for the setTaskBlock field.
func (r *mutationResolver) SetTaskBlock(ctx context.Context, id string, current string, target string, sketch string, acceptance string, tests string, verify string, checkCommand *string, acceptanceCommand *string) (*model.Task, error) {
	t, err := plan.SetTaskBlockCommandsContext(ctx, r.store, id, current, target, sketch, acceptance, tests, verify, checkCommand, acceptanceCommand)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, t.ID)
	return r.taskModel(ctx, t.ID, rootSelection(ctx), true)
}

// Projections are requested through the owning package, never a graph-side query.
func rootSelection(ctx context.Context) ast.SelectionSet {
	return graphql.GetFieldContext(ctx).Field.Selections
}
func childSelection(ctx context.Context, set ast.SelectionSet, kind, name string) (ast.SelectionSet, int) {
	out := ast.SelectionSet{}
	n := 0
	for _, f := range selected(ctx, set, kind) {
		if f.Name == name {
			out = append(out, f.Selections...)
			n++
		}
	}
	return out, n
}
func (r *Resolver) patRows(ctx context.Context, kind, id, parent, state string, set ast.SelectionSet, root bool, multiple int) ([]map[string]any, error) {
	fields := selectedNames(selected(ctx, set, map[string]string{"plan": "Plan", "approach": "Approach", "task": "Task"}[kind]))
	limit, bytes := available(ctx)
	b := budget(ctx)
	reserved := root && b != nil && graphql.GetOperationContext(ctx).Operation.Operation == ast.Mutation
	if b != nil {
		if bytes <= 0 {
			return nil, fmt.Errorf("GraphQL source byte budget exhausted; narrow query")
		}
		limit = b.rows/multiple + 1
		if reserved {
			limit++
		}
	}
	rows, err := plan.GraphRows(ctx, r.store, kind, id, parent, state, fields, limit, bytes)
	if err != nil {
		return nil, err
	}
	count := len(rows)
	if reserved && count > 0 {
		count--
	}
	if b != nil && count > b.rows/multiple {
		return nil, fmt.Errorf("tools.graphql.max_rows exceeded; narrow selected lists")
	}
	if err := charge(ctx, count*multiple); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row["too_large"] == true {
			return nil, fmt.Errorf("tools.graphql.max_result_bytes selected source exceeded; select narrower fields")
		}
	}
	if b != nil && len(rows) > 0 {
		n, _ := rows[0]["source_size"].(int64)
		if n > int64(b.bytes)/int64(multiple) {
			return nil, fmt.Errorf("GraphQL source budget exceeded")
		}
		b.bytes -= int(n) * multiple
	}
	return rows, nil
}
func (r *queryResolver) Plan(ctx context.Context, id string) (*model.Plan, error) {
	return r.planModel(ctx, id, "", "", rootSelection(ctx), true)
}
func (r *queryResolver) Plans(ctx context.Context, state *string) ([]*model.Plan, error) {
	filter := "!retired"
	if state != nil {
		filter = *state
	}
	set := rootSelection(ctx)
	rows, err := r.patRows(ctx, "plan", "", "", filter, set, true, 1)
	if err != nil {
		return nil, err
	}
	out := []*model.Plan{}
	for _, row := range rows {
		p, err := plan.GraphPlan(row)
		if err != nil {
			return nil, err
		}
		m, err := r.mapPlan(ctx, p, set)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
func (r *queryResolver) Approach(ctx context.Context, id string) (*model.Approach, error) {
	return r.approachModel(ctx, id, rootSelection(ctx), true)
}
func (r *queryResolver) Task(ctx context.Context, id string) (*model.Task, error) {
	return r.taskModel(ctx, id, rootSelection(ctx), true)
}
func (r *Resolver) planModel(ctx context.Context, id, parent, state string, set ast.SelectionSet, root bool) (*model.Plan, error) {
	rows, err := r.patRows(ctx, "plan", id, parent, state, set, root, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	p, err := plan.GraphPlan(rows[0])
	if err != nil {
		return nil, err
	}
	return r.mapPlan(ctx, p, set)
}
func (r *Resolver) mapPlan(ctx context.Context, p *plan.Plan, set ast.SelectionSet) (*model.Plan, error) {
	next, err := plan.NextPlan(p.State)
	if err != nil {
		return nil, err
	}
	var project *string
	if p.ProjectRoot != "" {
		project = &p.ProjectRoot
	}
	out := &model.Plan{ProjectRoot: project, ID: p.ID, Title: p.Title, Goal: p.Goal, State: p.State, CreatedAt: p.CreatedAt.Format(time.RFC3339), Next: next, Approaches: []*model.Approach{}}
	children, mult := childSelection(ctx, set, "Plan", "approaches")
	if mult == 0 {
		return out, nil
	}
	rows, err := r.patRows(ctx, "approach", "", p.ID, "", children, false, mult)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		a, err := plan.GraphApproach(row)
		if err != nil {
			return nil, err
		}
		m, err := r.mapApproach(ctx, a, children)
		if err != nil {
			return nil, err
		}
		out.Approaches = append(out.Approaches, m)
	}
	return out, nil
}
func (r *Resolver) approachModel(ctx context.Context, id string, set ast.SelectionSet, root bool) (*model.Approach, error) {
	rows, err := r.patRows(ctx, "approach", id, "", "", set, root, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	a, err := plan.GraphApproach(rows[0])
	if err != nil {
		return nil, err
	}
	return r.mapApproach(ctx, a, set)
}
func (r *Resolver) mapApproach(ctx context.Context, a *plan.Approach, set ast.SelectionSet) (*model.Approach, error) {
	next, err := plan.NextApproach(a.State)
	if err != nil {
		return nil, err
	}
	out := &model.Approach{ID: a.ID, Title: a.Title, State: a.State, NeedsReady: a.NeedsReady, CreatedAt: a.CreatedAt.Format(time.RFC3339), Next: next, Tasks: []*model.Task{}}
	children, mult := childSelection(ctx, set, "Approach", "tasks")
	qualified := false
	for _, f := range selected(ctx, set, "Approach") {
		if f.Name == "qualified" {
			qualified = true
			children = append(children, &ast.Field{Name: "qualified"})
		}
	}
	if mult == 0 && !qualified {
		return out, nil
	}
	if mult == 0 {
		mult = 1
	}
	rows, err := r.patRows(ctx, "task", "", a.ID, "", children, false, mult)
	if err != nil {
		return nil, err
	}
	out.Qualified = (a.State == plan.ApproachComplete || a.State == plan.ApproachRetired) && len(rows) > 0
	for _, row := range rows {
		t, err := plan.GraphTask(row)
		if err != nil {
			return nil, err
		}
		if !t.Qualified() {
			out.Qualified = false
		}
		m, err := toTaskModel(t)
		if err != nil {
			return nil, err
		}
		out.Tasks = append(out.Tasks, m)
	}
	return out, nil
}
func (r *Resolver) taskModel(ctx context.Context, id string, set ast.SelectionSet, root bool) (*model.Task, error) {
	rows, err := r.patRows(ctx, "task", id, "", "", set, root, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	t, err := plan.GraphTask(rows[0])
	if err != nil {
		return nil, err
	}
	return toTaskModel(t)
}

func toTaskModel(t *plan.Task) (*model.Task, error) {
	next, err := plan.NextTask(t.State)
	if err != nil {
		return nil, err
	}
	return &model.Task{ID: t.ID, Title: t.Title, Prompt: t.Prompt, State: t.State,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
		Current:           t.Current,
		Target:            t.Target,
		Sketch:            t.Sketch,
		Acceptance:        t.Acceptance,
		Tests:             t.Tests,
		Verify:            t.Verify,
		CheckCommand:      t.CheckCommand,
		AcceptanceCommand: t.AcceptanceCommand,
		Result:            t.Result,
		Execution:         string(t.Execution), ExecutionReason: t.ExecutionReason, Verification: string(t.Verification), VerificationReason: t.VerificationReason, Review: t.Review, AttemptLog: t.AttemptLog, Qualified: t.Qualified(),
		Attempts: t.Attempts,
		Next:     next}, nil
}

func toModel(e *memory.Entry) *model.MemoryEntry {
	return &model.MemoryEntry{
		ID:         e.ID,
		Kind:       e.Kind,
		Body:       e.Body,
		CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		SessionRef: e.SessionRef,
	}
}

// Mutation returns generated.MutationResolver implementation.
func (r *Resolver) Mutation() generated.MutationResolver { return &mutationResolver{r} }

// Query returns generated.QueryResolver implementation.
func (r *Resolver) Query() generated.QueryResolver { return &queryResolver{r} }

type (
	mutationResolver struct{ *Resolver }
	queryResolver    struct{ *Resolver }
)

func (r *mutationResolver) BindPlanProject(ctx context.Context, id string) (*model.Plan, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	p, err := plan.BindProject(ctx, r.store, id, s.root)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, p.ID)
	return r.planModel(ctx, p.ID, "", "", rootSelection(ctx), true)
}
func (r *mutationResolver) AppendKnowledge(ctx context.Context, key, body, expectedPreviousID string, taskID, filePath *string) (*model.Knowledge, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	task, file := "", ""
	if taskID != nil {
		task = *taskID
	}
	if filePath != nil {
		file = *filePath
	}
	e, err := memory.PutKnowledge(ctx, r.store, s.root, s.sessionRef, key, body, expectedPreviousID, task, file)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, e.ID)
	// Owner mutation output is selected/bounded just like an explicit read.
	return r.savedKnowledge(ctx, e.ID)
}

func (r *mutationResolver) Noticed(ctx context.Context, approachID, body string, filePath *string) (*model.Knowledge, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	file := ""
	if filePath != nil {
		file = *filePath
	}
	e, err := memory.Noticed(ctx, r.store, s.root, s.sessionRef, approachID, body, file)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, e.ID)
	return r.savedKnowledge(ctx, e.ID)
}

func (r *Resolver) savedKnowledge(ctx context.Context, id string) (*model.Knowledge, error) {
	entries, err := r.knowledge(ctx, "", id, 1, true)
	if err != nil {
		return nil, err
	}
	if len(entries) != 1 {
		return nil, fmt.Errorf("applied knowledge %s could not be read back", id)
	}
	return entries[0], nil
}
func (r *mutationResolver) PutKnowledge(ctx context.Context, key, body, expectedPreviousID string, taskID, filePath *string) (*model.Knowledge, error) {
	return r.AppendKnowledge(ctx, key, body, expectedPreviousID, taskID, filePath)
}
func (r *queryResolver) Knowledge(ctx context.Context, key, id *string, limit *int) ([]*model.Knowledge, error) {
	k, i := "", ""
	if key != nil {
		k = *key
	}
	if id != nil {
		i = *id
	}
	n := 0
	if limit != nil {
		n = *limit
		if n <= 0 {
			return nil, fmt.Errorf("knowledge limit must be positive")
		}
	}
	return r.knowledge(ctx, k, i, n, false)
}
func (r *queryResolver) Notices(ctx context.Context, approachID string, limit *int) ([]*model.Knowledge, error) {
	n := 0
	if limit != nil {
		n = *limit
		if n <= 0 {
			return nil, fmt.Errorf("notices limit must be positive")
		}
	}
	return r.knowledge(ctx, "", "", n, false, approachID)
}

func (r *Resolver) knowledge(ctx context.Context, key, id string, n int, mutation bool, approachID ...string) ([]*model.Knowledge, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	rows, bytes := available(ctx)
	if n <= 0 || n > rows {
		n = rows
	}
	if bytes <= 0 || n <= 0 {
		return nil, fmt.Errorf("GraphQL knowledge source budget exhausted")
	}
	body := false
	for _, f := range selected(ctx, rootSelection(ctx), "Knowledge") {
		if f.Name == "body" {
			body = true
		}
	}
	fields := selectedNames(selected(ctx, rootSelection(ctx), "Knowledge"))
	var entries []*memory.Entry
	if len(approachID) > 0 {
		entries, err = memory.Notices(ctx, r.store, s.root, approachID[0], n, bytes, body, fields)
	} else {
		entries, err = memory.Knowledge(ctx, r.store, s.root, key, id, n, bytes, body, fields)
	}
	if err != nil {
		return nil, err
	}
	count := len(entries)
	if mutation && count > 0 {
		count--
	}
	if err = charge(ctx, count); err != nil {
		return nil, err
	}
	out := []*model.Knowledge{}
	for _, e := range entries {
		if err = charge(ctx, 0, e.ID, e.Key, e.Body, e.SessionRef, e.PreviousID, e.TaskID, e.FilePath); err != nil {
			return nil, err
		}
		out = append(out, &model.Knowledge{ID: e.ID, Key: e.Key, Body: e.Body, CreatedAt: e.CreatedAt.Format(time.RFC3339), SessionRef: e.SessionRef, PreviousID: e.PreviousID, TaskID: e.TaskID, FilePath: e.FilePath})
	}
	return out, nil
}

func (r *mutationResolver) AddTaskComment(ctx context.Context, taskID string, expectedAttempt int, text string) (*model.TaskComment, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	source := "api-authored"
	if s.model {
		source = "model-authored"
	}
	c, err := plan.AppendTaskComment(plan.WithProject(ctx, s.root), r.store, taskID, expectedAttempt, source, s.sessionRef, text)
	if err != nil {
		return nil, transactionError(ctx, err)
	}
	applied(ctx, c.ID)
	return commentModel(ctx, c, false)
}
func (r *queryResolver) TaskComments(ctx context.Context, taskID string, attempt, limit int) ([]*model.TaskComment, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	rows, bytes := available(ctx)
	if attempt <= 0 || limit <= 0 || limit > rows || bytes <= 0 {
		return nil, fmt.Errorf("task comments need positive attempt/limit within remaining GraphQL rows/source bytes")
	}
	comments, err := plan.GraphTaskComments(plan.WithProject(ctx, s.root), r.store, taskID, attempt, limit, bytes)
	if err != nil {
		return nil, err
	}
	out := []*model.TaskComment{}
	for _, c := range comments {
		m, err := commentModel(ctx, &c, true)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
func commentModel(ctx context.Context, c *outcome.TaskComment, query bool) (*model.TaskComment, error) {
	n := 0
	if query {
		n = 1
	}
	if err := charge(ctx, n, c.ID, c.TaskID, c.Source, c.SessionRef, c.Text, c.CreatedAt); err != nil {
		return nil, err
	}
	return &model.TaskComment{ID: c.ID, TaskID: c.TaskID, Attempt: c.Attempt, Source: c.Source, SessionRef: c.SessionRef, Text: c.Text, CreatedAt: c.CreatedAt}, nil
}

func (r *queryResolver) RevisionContract(ctx context.Context, kind, id string) (*model.RevisionContract, error) {
	return r.Resolver.revisionContract(ctx, kind, id)
}
func (r *Resolver) revisionContract(ctx context.Context, kind, id string) (*model.RevisionContract, error) {
	s, e := scope(ctx)
	if e != nil {
		return nil, e
	}
	rows, bytes := available(ctx)
	digest, brief, pending, e := plan.RevisionContract(plan.WithProject(ctx, s.root), r.store, kind, id, rows, bytes)
	if e != nil {
		return nil, e
	}
	if e = charge(ctx, 1, digest, brief); e != nil {
		return nil, e
	}
	out := &model.RevisionContract{Snapshot: digest, Brief: brief, Pending: []*model.Revision{}}
	for _, v := range pending {
		m, e := revisionModel(ctx, v)
		if e != nil {
			return nil, e
		}
		out.Pending = append(out.Pending, m)
	}
	return out, nil
}
func revisionModel(ctx context.Context, v plan.Revision) (*model.Revision, error) {
	if e := charge(ctx, 1, v.ID, v.Kind, v.TargetKind, v.TargetID, v.Text, v.Source, v.SessionRef, v.CreatedAt, v.Resolves); e != nil {
		return nil, e
	}
	if e := charge(ctx, len(v.TaskIDs), v.TaskIDs...); e != nil {
		return nil, e
	}
	ids := append([]string{}, v.TaskIDs...)
	return &model.Revision{ID: v.ID, Kind: v.Kind, TargetKind: v.TargetKind, TargetID: v.TargetID, Text: v.Text, Source: v.Source, SessionRef: v.SessionRef, CreatedAt: v.CreatedAt, Resolves: v.Resolves, TaskIds: ids}, nil
}
func (r *mutationResolver) RequestRevision(ctx context.Context, kind, id, expectedSnapshot, text string) (*model.Revision, error) {
	s, e := scope(ctx)
	if e != nil {
		return nil, e
	}
	source := "api-authored"
	if s.model {
		source = "model-authored"
	}
	v, e := plan.RequestRevision(plan.WithProject(ctx, s.root), r.store, kind, id, expectedSnapshot, source, s.sessionRef, text)
	if e != nil {
		return nil, transactionError(ctx, e)
	}
	applied(ctx, v.ID)
	return revisionModel(ctx, *v)
}
func (r *mutationResolver) EditRevision(ctx context.Context, kind, id, expectedSnapshot, field, value string) (*model.RevisionContract, error) {
	s, e := scope(ctx)
	if e != nil {
		return nil, e
	}
	if e = plan.EditRevision(plan.WithProject(ctx, s.root), r.store, kind, id, expectedSnapshot, field, value); e != nil {
		return nil, transactionError(ctx, e)
	}
	applied(ctx, id)
	return r.Resolver.revisionContract(ctx, kind, id)
}
func (r *mutationResolver) ResolveRevision(ctx context.Context, kind, id, revisionID, expectedSnapshot string) (*model.Revision, error) {
	s, e := scope(ctx)
	if e != nil {
		return nil, e
	}
	v, e := plan.ResolveRevision(plan.WithProject(ctx, s.root), r.store, kind, id, revisionID, expectedSnapshot)
	if e != nil {
		return nil, transactionError(ctx, e)
	}
	applied(ctx, v.ID)
	return revisionModel(ctx, *v)
}
