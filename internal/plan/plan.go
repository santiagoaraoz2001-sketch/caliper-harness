// Package plan owns the Plan/Approach/Task lifecycle: states, transitions,
// guards, and storage. GraphQL resolvers and the launch tool are thin
// callers over it — planning edits concentrate here.
package plan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
)

// ErrNotFound marks missing-node errors so nullable queries map to null
// instead of failing.
var ErrNotFound = errors.New("not found")

// States. Kept as plain strings (see schema note); only these values are
// legal, enforced below.
const (
	PlanDraft    = "draft"
	PlanActive   = "active"
	PlanComplete = "complete"
	PlanRetired  = "retired"

	ApproachDraft    = "draft"
	ApproachReady    = "ready"
	ApproachRunning  = "running"
	ApproachPaused   = "paused"
	ApproachComplete = "complete"
	ApproachRetired  = "retired"

	TaskQueued    = "queued"
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskStopped   = "stopped"
	TaskCancelled = "cancelled"
)

type stateStore interface {
	Query(string, map[string]any) ([]map[string]any, error)
	Write(string, map[string]any) error
}

// Plan is one planning-graph plan node.
type Plan struct {
	ProjectRoot string
	ID          string
	Title       string
	Goal        string
	State       string
	CreatedAt   time.Time
}

// Approach is an orchestration node with explicit authored Plan scope.
type Approach struct {
	NeedsReady bool
	ID         string
	Title      string
	State      string
	CreatedAt  time.Time
}

// Task is one subagent prompt node. The six block fields hold its lab
// task-block shape; blanks mean undrafted (the ready gate rejects them).
type Task struct {
	ID                string
	Title             string
	Prompt            string
	State             string
	CreatedAt         time.Time
	Current           string
	Target            string
	Sketch            string
	Acceptance        string
	Tests             string
	Verify            string
	CheckCommand      string
	AcceptanceCommand string
	Result            string
	// Review holds the verification pass verdict: empty until a
	// follow-up run checks the result; raw text is exposed in GraphQL.
	Execution          outcome.Status
	ExecutionReason    string
	Verification       outcome.Verification
	VerificationReason string
	checkProjection    *string // private selected-read receipt facts; never public AttemptLog
	AttemptLog         string
	Review             string
	Attempts           int
}

var (
	planNext = map[string][]string{
		PlanDraft:    {PlanActive},
		PlanActive:   {PlanComplete},
		PlanComplete: {PlanRetired},
		PlanRetired:  {},
	}
	approachNext = map[string][]string{
		ApproachDraft:    {ApproachReady},
		ApproachReady:    {ApproachRunning},
		ApproachRunning:  {ApproachComplete, ApproachPaused},
		ApproachPaused:   {ApproachRunning, ApproachReady},
		ApproachComplete: {ApproachRetired},
		ApproachRetired:  {},
	}
	taskNext = map[string][]string{
		TaskQueued:    {TaskRunning, TaskCancelled},
		TaskRunning:   {TaskDone, TaskFailed, TaskStopped, TaskCancelled},
		TaskStopped:   {TaskQueued},
		TaskCancelled: {TaskQueued},
		TaskDone:      {TaskFailed},
		TaskFailed:    {TaskQueued},
	}
)

// NextPlan/NextApproach/NextTask return the legal moves from a state.
// Unknown states fail loudly — garbage in the DB must not pass silently.
func NextPlan(state string) ([]string, error)     { return next("plan", planNext, state) }
func NextApproach(state string) ([]string, error) { return next("approach", approachNext, state) }
func NextTask(state string) ([]string, error)     { return next("task", taskNext, state) }

func next(kind string, table map[string][]string, state string) ([]string, error) {
	moves, ok := table[state]
	if !ok {
		return nil, fmt.Errorf("unknown %s state %q", kind, state)
	}
	return moves, nil
}

// CreatePlan stores a draft plan.
func CreatePlan(s *store.Store, title, goal string) (*Plan, error) {
	return CreatePlanContext(context.Background(), s, title, goal)
}
func CreatePlanContext(ctx context.Context, s *store.Store, title, goal string) (*Plan, error) {
	if title == "" || goal == "" {
		return nil, fmt.Errorf("plan needs a title and a goal")
	}
	p := &Plan{ID: uuid.NewString(), Title: title, Goal: goal, State: PlanDraft, CreatedAt: time.Now().UTC(), ProjectRoot: ProjectRoot(ctx)}
	err := s.WriteContext(ctx,
		`CREATE (p:Plan {id: $id, title: $t, goal: $g, state: $s, created_at: $now, project_root: $root})`,
		map[string]any{"id": p.ID, "t": title, "g": goal, "s": p.State, "now": p.CreatedAt, "root": p.ProjectRoot},
	)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CreateApproach stores a draft approach attached to its plan. The node
// and rel land in one statement: no crash-orphans, no tasks outside plans.
func createApproach(s stateStore, planID, title string) (*Approach, error) {
	if e := requireNoRevision(s, "plan", planID); e != nil {
		return nil, e
	}
	if title == "" {
		return nil, fmt.Errorf("approach needs a title")
	}
	if err := openPlan(s, planID, false); err != nil {
		return nil, err
	}
	a := &Approach{ID: uuid.NewString(), Title: title, State: ApproachDraft, CreatedAt: time.Now().UTC()}
	err := s.Write(
		`MATCH (p:Plan) WHERE p.id = $pid CREATE (a:Approach {id: $id, title: $t, state: $s, created_at: $now}) CREATE (p)-[:HAS_APPROACH]->(a)`,
		map[string]any{"pid": planID, "id": a.ID, "t": title, "s": a.State, "now": a.CreatedAt},
	)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// CreateTask stores a queued task attached to its approach, node and rel
// in one statement.
func createTask(s stateStore, approachID, title, prompt string) (*Task, error) {
	if e := requireNoRevision(s, "approach", approachID); e != nil {
		return nil, e
	}
	if title == "" || prompt == "" {
		return nil, fmt.Errorf("task needs a title and a prompt")
	}
	if err := editableApproach(s, approachID); err != nil {
		return nil, err
	}
	t := &Task{ID: uuid.NewString(), Title: title, Prompt: prompt, State: TaskQueued, CreatedAt: time.Now().UTC(), Execution: outcome.Unknown, Verification: outcome.Unverified, AttemptLog: "[]"}
	err := s.Write(
		`MATCH (a:Approach) WHERE a.id = $aid CREATE (x:Task {id: $id, title: $t, prompt: $p, state: $s, created_at: $now, review: "", execution:"unknown", execution_reason:"", verification:"unverified", verification_reason:"", attempt_log:"[]"}) CREATE (a)-[:HAS_TASK]->(x)`,
		map[string]any{"aid": approachID, "id": t.ID, "t": title, "p": prompt, "s": t.State, "now": t.CreatedAt},
	)
	if err != nil {
		return nil, err
	}
	if err := markEdited(s, approachID); err != nil {
		return nil, err
	}
	return t, nil
}

// SetTaskBlock stores a task's block draft. Blanks are allowed (drafts in
// progress); the ready gate rejects undrafted blocks.
func setTaskBlock(s stateStore, id, current, target, sketch, acceptance, tests, verify string, checkCommand, acceptanceCommand *string) (*Task, error) {
	a, err := taskApproach(s, id)
	if err != nil {
		return nil, err
	}
	if err := editableApproach(s, a.ID); err != nil {
		return nil, err
	}
	t, err := GetTask(s, id)
	if err != nil {
		return nil, err
	}
	command, behaviorCommand := t.CheckCommand, t.AcceptanceCommand
	if checkCommand != nil {
		command = *checkCommand
	}
	if acceptanceCommand != nil {
		behaviorCommand = *acceptanceCommand
	}
	if t.State != TaskQueued && t.State != TaskFailed && t.State != TaskStopped && t.State != TaskCancelled {
		return nil, fmt.Errorf("task %q is %s — edit briefs only while queued, failed, stopped or cancelled", id, t.State)
	}
	if t.Current == current && t.Target == target && t.Sketch == sketch && t.Acceptance == acceptance && t.Tests == tests && t.Verify == verify && t.CheckCommand == command && t.AcceptanceCommand == behaviorCommand {
		return t, nil
	}
	err = s.Write(
		`MATCH (x:Task) WHERE x.id = $id SET x.current = $current, x.target = $target, x.sketch = $sketch, x.acceptance = $acceptance, x.tests = $tests, x.verify = $verify, x.check_command = $command, x.acceptance_command = $acceptance_command`,
		map[string]any{"id": id, "current": current, "target": target, "sketch": sketch, "acceptance": acceptance, "tests": tests, "verify": verify, "command": command, "acceptance_command": behaviorCommand},
	)
	if err != nil {
		return nil, err
	}
	if err := markEdited(s, a.ID); err != nil {
		return nil, err
	}
	return GetTask(s, id)
}

// SetTaskAttempts records how many times the task has been attempted.
func SetTaskAttempts(s *store.Store, id string, n int) error {
	return s.Update(func(tx *store.Tx) error {
		if err := openTask(tx, id); err != nil {
			return err
		}
		return tx.Write(`MATCH (x:Task) WHERE x.id = $id SET x.attempts = $n`,
			map[string]any{"id": id, "n": int64(n)})
	})
}

// SetTaskResult stores a subagent's full output on its task node.
func SetTaskResult(s *store.Store, id, text string) error {
	return s.Update(func(tx *store.Tx) error {
		if err := openTask(tx, id); err != nil {
			return err
		}
		return tx.Write(`MATCH (x:Task) WHERE x.id = $id SET x.result = $text`,
			map[string]any{"id": id, "text": text})
	})
}

// SetTaskReview stores the verification pass verdict on its task node.
func SetTaskReview(s *store.Store, id, text string) error {
	return s.Update(func(tx *store.Tx) error {
		if err := openTask(tx, id); err != nil {
			return err
		}
		return tx.Write(`MATCH (x:Task) WHERE x.id = $id SET x.review = $text`,
			map[string]any{"id": id, "text": text})
	})
}

// GetPlan/ GetApproach / GetTask load one node. Missing ids are plain errors.
func GetPlan(s stateStore, id string) (*Plan, error) {
	rows, err := s.Query(`MATCH (p:Plan) WHERE p.id = $id RETURN p.id AS id, p.title AS title, p.goal AS goal, p.state AS state, p.created_at AS created_at, p.project_root AS project_root`,
		map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no plan %q: %w", id, ErrNotFound)
	}
	return planOf(rows[0])
}

// GetApproach loads one approach node.
func GetApproach(s stateStore, id string) (*Approach, error) {
	rows, err := s.Query(`MATCH (a:Approach) WHERE a.id = $id RETURN a.id AS id, a.title AS title, a.state AS state, a.created_at AS created_at, a.needs_ready AS needs_ready`,
		map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no approach %q: %w", id, ErrNotFound)
	}
	return approachOf(rows[0])
}

// ApproachPlan returns the plan owning an approach.
func ApproachPlan(s stateStore, approachID string) (*Plan, error) {
	rows, err := s.Query(`MATCH (p:Plan)-[:HAS_APPROACH]->(a:Approach) WHERE a.id = $id RETURN p.id AS id, p.title AS title, p.goal AS goal, p.state AS state, p.created_at AS created_at, p.project_root AS project_root`,
		map[string]any{"id": approachID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no plan for approach %q: %w", approachID, ErrNotFound)
	}
	return planOf(rows[0])
}

// GetTask loads one task node.
func GetTask(s stateStore, id string) (*Task, error) {
	rows, err := s.Query(`MATCH (x:Task) WHERE x.id = $id RETURN x.id AS id, x.title AS title, x.prompt AS prompt, x.state AS state, x.created_at AS created_at, x.current AS current, x.target AS target, x.sketch AS sketch, x.acceptance AS acceptance, x.tests AS tests, x.verify AS verify, x.check_command AS check_command, x.acceptance_command AS acceptance_command, x.result AS result, x.review AS review, x.attempts AS attempts, x.execution AS execution, x.execution_reason AS execution_reason, x.verification AS verification, x.verification_reason AS verification_reason, x.attempt_log AS attempt_log`,
		map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no task %q: %w", id, ErrNotFound)
	}
	return taskOf(rows[0])
}

// ListPlans returns plans, optionally filtered by state ("" = all).
func ListPlans(s stateStore, state string) ([]*Plan, error) {
	q := `MATCH (p:Plan)`
	params := map[string]any{}
	if state != "" {
		q += ` WHERE p.state = $state`
		params["state"] = state
	}
	q += ` RETURN p.id AS id, p.title AS title, p.goal AS goal, p.state AS state, p.created_at AS created_at ORDER BY created_at`
	rows, err := s.Query(q, params)
	if err != nil {
		return nil, err
	}
	out := make([]*Plan, 0, len(rows))
	for _, r := range rows {
		p, err := planOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ActivePlans returns non-retired plans: the default list bucket.
// RetiredPlans returns retired plans: the archive bucket.
func ActivePlans(s stateStore) ([]*Plan, error) {
	all, err := ListPlans(s, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Plan, 0, len(all))
	for _, p := range all {
		if p.State != PlanRetired {
			out = append(out, p)
		}
	}
	return out, nil
}

func RetiredPlans(s stateStore) ([]*Plan, error) {
	return ListPlans(s, PlanRetired)
}

// PlanApproaches returns an approach list for tree views.
func PlanApproaches(s stateStore, planID string) ([]*Approach, error) {
	rows, err := s.Query(`MATCH (p:Plan)-[:HAS_APPROACH]->(a:Approach) WHERE p.id = $id RETURN a.id AS id, a.title AS title, a.state AS state, a.created_at AS created_at, a.needs_ready AS needs_ready ORDER BY created_at`,
		map[string]any{"id": planID})
	if err != nil {
		return nil, err
	}
	out := make([]*Approach, 0, len(rows))
	for _, r := range rows {
		a, err := approachOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ActivePlanApproaches / RetiredPlanApproaches split a plan's approaches
// into the default and archive buckets.
func ActivePlanApproaches(s stateStore, planID string) ([]*Approach, error) {
	all, err := PlanApproaches(s, planID)
	if err != nil {
		return nil, err
	}
	out := make([]*Approach, 0, len(all))
	for _, a := range all {
		if a.State != ApproachRetired {
			out = append(out, a)
		}
	}
	return out, nil
}

func RetiredPlanApproaches(s stateStore, planID string) ([]*Approach, error) {
	all, err := PlanApproaches(s, planID)
	if err != nil {
		return nil, err
	}
	out := make([]*Approach, 0, len(all))
	for _, a := range all {
		if a.State == ApproachRetired {
			out = append(out, a)
		}
	}
	return out, nil
}

// ApproachTasks returns a task list for tree views.
func ApproachTasks(s stateStore, approachID string) ([]*Task, error) {
	rows, err := s.Query(`MATCH (a:Approach)-[:HAS_TASK]->(x:Task) WHERE a.id = $id RETURN x.id AS id, x.title AS title, x.prompt AS prompt, x.state AS state, x.created_at AS created_at, x.current AS current, x.target AS target, x.sketch AS sketch, x.acceptance AS acceptance, x.tests AS tests, x.verify AS verify, x.check_command AS check_command, x.acceptance_command AS acceptance_command, x.result AS result, x.review AS review, x.attempts AS attempts, x.execution AS execution, x.execution_reason AS execution_reason, x.verification AS verification, x.verification_reason AS verification_reason, x.attempt_log AS attempt_log ORDER BY created_at`,
		map[string]any{"id": approachID})
	if err != nil {
		return nil, err
	}
	out := make([]*Task, 0, len(rows))
	for _, r := range rows {
		t, err := taskOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// TransitionPlan moves a plan. Illegal moves name the legal ones.
func transitionPlan(s stateStore, id, to string) (*Plan, error) {
	p, err := GetPlan(s, id)
	if err != nil {
		return nil, err
	}
	if err := checkMove("plan", id, planNext, p.State, to); err != nil {
		return nil, err
	}
	if p.State == PlanActive && to == PlanComplete {
		coverage, err := PlanCoverage(s, id)
		if err != nil {
			return nil, err
		}
		if len(coverage.Approaches) == 0 {
			return nil, fmt.Errorf("plan %q has no approaches — cannot qualify completion", id)
		}
		if len(coverage.Unresolved) > 0 {
			return nil, fmt.Errorf("plan %q has open or unverified approach %q", id, coverage.Unresolved[0].ID)
		}
		if len(coverage.Uncovered) > 0 {
			return nil, fmt.Errorf("plan %q has uncovered article %d: %s", id, coverage.Uncovered[0].Index+1, coverage.Uncovered[0].Text)
		}
	} else if p.State == PlanComplete && to == PlanRetired {
		// Archiving preserves the existing historical qualification rule; it
		// does not manufacture new coverage or rewrite old associations.
		if err := requireApproachesComplete(s, id); err != nil {
			return nil, err
		}
	}
	if err := setState(s, "Plan", id, to); err != nil {
		return nil, err
	}
	p.State = to
	return p, nil
}

// TransitionApproach moves an approach.
func transitionApproach(s stateStore, id, to string) (*Approach, error) {
	a, err := GetApproach(s, id)
	if err != nil {
		return nil, err
	}
	if err := checkMove("approach", id, approachNext, a.State, to); err != nil {
		return nil, err
	}
	p, err := ApproachPlan(s, id)
	if err != nil {
		return nil, err
	}
	if err = openPlan(s, p.ID, to == ApproachRetired); err != nil {
		return nil, err
	}
	switch {
	case (a.State == ApproachDraft || a.State == ApproachPaused) && to == ApproachReady:
		tasks, err := ApproachTasks(s, id)
		if err != nil {
			return nil, err
		}
		if len(tasks) == 0 {
			return nil, fmt.Errorf("approach %q has no tasks — add at least one before ready", id)
		}
		if err := ValidateApproachReady(s, id); err != nil {
			return nil, err
		}
	case a.State == ApproachPaused && to == ApproachRunning && a.NeedsReady:
		return nil, fmt.Errorf("approach %q has edited briefs — check readiness again before running", id)
	case a.State == ApproachRunning && to == ApproachComplete || a.State == ApproachComplete && to == ApproachRetired:
		if err := requireTasksDone(s, id); err != nil {
			return nil, err
		}
	}
	if err := setState(s, "Approach", id, to); err != nil {
		return nil, err
	}
	if to == ApproachReady {
		if err := s.Write(`MATCH (a:Approach) WHERE a.id=$id SET a.needs_ready=false`, map[string]any{"id": id}); err != nil {
			return nil, err
		}
		a.NeedsReady = false
	}
	a.State = to
	return a, nil
}

// TransitionTask moves a task.
func transitionTask(s stateStore, id, to string, rework bool) (*Task, error) {
	if to == TaskRunning || to == TaskDone {
		if e := requireNoRevision(s, "task", id); e != nil {
			return nil, e
		}
	}
	t, err := GetTask(s, id)
	if err != nil {
		return nil, err
	}
	if to == TaskDone && outcome.RevisionInvalidates(t.AttemptLog, t.Attempts) {
		return nil, fmt.Errorf("task revision needs a fresh attempt before completion")
	}
	if err := checkMove("task", id, taskNext, t.State, to); err != nil {
		return nil, err
	}
	if rework && t.State == TaskDone && to == TaskFailed {
		if err := reopenForRework(s, t); err != nil {
			return nil, err
		}
	}
	a, err := taskApproach(s, id)
	if err != nil {
		return nil, err
	}
	if to == TaskRunning && a.NeedsReady {
		return nil, fmt.Errorf("approach %q has edited briefs — check readiness again before starting task %q", a.ID, id)
	}
	if err := openApproach(s, a.ID); err != nil {
		return nil, err
	}
	if err := setState(s, "Task", id, to); err != nil {
		return nil, err
	}
	t.State = to
	return t, nil
}

// checkMove rejects illegal and unknown states, naming the legal moves.
func checkMove(kind, id string, table map[string][]string, from, to string) error {
	moves, err := next(kind, table, from)
	if err != nil {
		return err
	}
	for _, m := range moves {
		if m == to {
			return nil
		}
	}
	if len(moves) == 0 {
		return fmt.Errorf("cannot move %s %q from %q — it is terminal", kind, id, from)
	}
	return fmt.Errorf("cannot move %s %q from %q to %q — legal moves: %s",
		kind, id, from, to, strings.Join(moves, ", "))
}

func requireApproachesComplete(s stateStore, planID string) error {
	approaches, err := PlanApproaches(s, planID)
	if err != nil {
		return err
	}
	if len(approaches) == 0 {
		return fmt.Errorf("plan %q has no approaches — cannot qualify completion or retirement", planID)
	}
	for _, a := range approaches {
		q, err := ApproachQualified(s, a.ID)
		if err != nil {
			return err
		}
		if !q {
			return fmt.Errorf("plan %q has open or unverified approach %q", planID, a.ID)
		}
	}
	return nil
}

func requireTasksDone(s stateStore, approachID string) error {
	tasks, err := ApproachTasks(s, approachID)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return fmt.Errorf("approach %q has no tasks — cannot qualify completion or retirement", approachID)
	}
	open := 0
	for _, t := range tasks {
		if !t.Qualified() {
			open++
		}
	}
	if open > 0 {
		return fmt.Errorf("approach %q has %d open or unverified tasks", approachID, open)
	}
	return nil
}

// setState flips one node's state. The label interpolates, but labels come
// only from the three Transition call sites above — never from user input.
func setState(s stateStore, label, id, to string) error {
	return s.Write(`MATCH (n:`+label+`) WHERE n.id = $id SET n.state = $to`,
		map[string]any{"id": id, "to": to})
}

func planOf(r map[string]any) (*Plan, error) {
	id, _ := r["id"].(string)
	ts, ok := r["created_at"].(time.Time)
	if id == "" || !ok {
		return nil, fmt.Errorf("plan row has wrong shape: %v", r)
	}
	title, _ := r["title"].(string)
	goal, _ := r["goal"].(string)
	state, _ := r["state"].(string)
	return &Plan{ID: id, Title: title, Goal: goal, State: state, CreatedAt: ts, ProjectRoot: stringField(r, "project_root")}, nil
}

func approachOf(r map[string]any) (*Approach, error) {
	id, _ := r["id"].(string)
	ts, ok := r["created_at"].(time.Time)
	if id == "" || !ok {
		return nil, fmt.Errorf("approach row has wrong shape: %v", r)
	}
	title, _ := r["title"].(string)
	state, _ := r["state"].(string)
	needsReady, _ := r["needs_ready"].(bool)
	return &Approach{ID: id, Title: title, State: state, CreatedAt: ts, NeedsReady: needsReady}, nil
}

func taskOf(r map[string]any) (*Task, error) {
	id, _ := r["id"].(string)
	ts, ok := r["created_at"].(time.Time)
	if id == "" || !ok {
		return nil, fmt.Errorf("task row has wrong shape: %v", r)
	}
	title, _ := r["title"].(string)
	prompt, _ := r["prompt"].(string)
	state, _ := r["state"].(string)
	current, _ := r["current"].(string)
	target, _ := r["target"].(string)
	sketch, _ := r["sketch"].(string)
	acceptance, _ := r["acceptance"].(string)
	tests, _ := r["tests"].(string)
	verify, _ := r["verify"].(string)
	result, _ := r["result"].(string)
	review, _ := r["review"].(string)
	attempts, _ := r["attempts"].(int64)
	t := &Task{ID: id, Title: title, Prompt: prompt, State: state, CreatedAt: ts,
		Current: current, Target: target, Sketch: sketch,
		Acceptance: acceptance, Tests: tests, Verify: verify, CheckCommand: stringField(r, "check_command"), AcceptanceCommand: stringField(r, "acceptance_command"), Result: result,
		Review: review, Attempts: int(attempts)}
	t.Execution = outcome.Status(stringField(r, "execution"))
	t.ExecutionReason = stringField(r, "execution_reason")
	t.Verification = outcome.Verification(stringField(r, "verification"))
	t.VerificationReason = stringField(r, "verification_reason")
	t.AttemptLog = stringField(r, "attempt_log")
	if t.AttemptLog == "" {
		t.AttemptLog = "[]"
	}
	if t.Execution == "" {
		t.Execution = outcome.Unknown
		t.ExecutionReason = "legacy execution outcome not recorded"
	}
	if t.Verification == "" {
		t.Verification = outcome.Unverified
		t.VerificationReason = "verification policy/evidence not recorded"
	}
	return t, nil
}

func CreateApproach(s *store.Store, planID, title string) (*Approach, error) {
	return CreateApproachContext(context.Background(), s, planID, title)
}
func CreateApproachContext(ctx context.Context, s *store.Store, planID, title string) (*Approach, error) {
	var out *Approach
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "plan", planID, ProjectRoot(ctx)); err != nil {
			return err
		}
		var err error
		out, err = createApproach(tx, planID, title)
		return err
	})
	return out, err
}

func CreateTask(s *store.Store, approachID, title, prompt string) (*Task, error) {
	return CreateTaskContext(context.Background(), s, approachID, title, prompt)
}
func CreateTaskContext(ctx context.Context, s *store.Store, approachID, title, prompt string) (*Task, error) {
	var out *Task
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "approach", approachID, ProjectRoot(ctx)); err != nil {
			return err
		}
		var err error
		out, err = createTask(tx, approachID, title, prompt)
		return err
	})
	return out, err
}

func SetTaskBlock(s *store.Store, id, current, target, sketch, acceptance, tests, verify string, checkCommand ...string) (*Task, error) {
	return SetTaskBlockContext(context.Background(), s, id, current, target, sketch, acceptance, tests, verify, checkCommand...)
}
func SetTaskBlockContext(ctx context.Context, s *store.Store, id, current, target, sketch, acceptance, tests, verify string, checkCommand ...string) (*Task, error) {
	if len(checkCommand) > 2 {
		return nil, fmt.Errorf("task block accepts check and acceptance commands")
	}
	var check, behavior *string
	if len(checkCommand) > 0 {
		check = &checkCommand[0]
	}
	if len(checkCommand) > 1 {
		behavior = &checkCommand[1]
	}
	return SetTaskBlockCommandsContext(ctx, s, id, current, target, sketch, acceptance, tests, verify, check, behavior)
}

// SetTaskBlockCommandsContext preserves omitted commands inside the owning transaction.
func SetTaskBlockCommandsContext(ctx context.Context, s *store.Store, id, current, target, sketch, acceptance, tests, verify string, checkCommand, acceptanceCommand *string) (*Task, error) {
	var out *Task
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "task", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		var err error
		out, err = setTaskBlock(tx, id, current, target, sketch, acceptance, tests, verify, checkCommand, acceptanceCommand)
		return err
	})
	return out, err
}

func TransitionPlan(s *store.Store, id, to string) (*Plan, error) {
	return TransitionPlanContext(context.Background(), s, id, to)
}
func TransitionPlanContext(ctx context.Context, s *store.Store, id, to string) (*Plan, error) {
	var out *Plan
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "plan", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		var err error
		out, err = transitionPlan(tx, id, to)
		return err
	})
	return out, err
}

func TransitionApproach(s *store.Store, id, to string) (*Approach, error) {
	return TransitionApproachChecked(s, id, to, "")
}

// TransitionApproachChecked admits only the exact brief/state snapshot checked
// outside the writer. Empty snapshots retain the ordinary lifecycle guards.
func TransitionApproachChecked(s *store.Store, id, to, snapshot string) (*Approach, error) {
	return TransitionApproachCheckedContext(context.Background(), s, id, to, snapshot)
}
func TransitionApproachCheckedContext(ctx context.Context, s *store.Store, id, to, snapshot string) (*Approach, error) {
	if to == ApproachReady {
		return nil, fmt.Errorf("ready requires plan.Ready with an explicit checker")
	}
	var out *Approach
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "approach", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		if snapshot != "" {
			current, e := readySnapshot(tx, id)
			if e != nil {
				return e
			}
			if current != snapshot {
				return fmt.Errorf("approach %q changed during readiness — review it again", id)
			}
		}
		var err error
		out, err = transitionApproach(tx, id, to)
		return err
	})
	return out, err
}

// ReadyChecker judges the exact admitted brief outside the store writer.
type ReadyChecker func(context.Context, *Plan, []*Task) error

// Ready commits only the unchanged brief that this call checked.
func Ready(ctx context.Context, s *store.Store, id string, check ReadyChecker) (*Approach, error) {
	if check == nil {
		return nil, fmt.Errorf("ready has no checker — wire plan.Ready")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot string
	var p *Plan
	var tasks []*Task
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "approach", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		a, err := GetApproach(tx, id)
		if err != nil {
			return err
		}
		if a.State != ApproachDraft && a.State != ApproachPaused {
			return fmt.Errorf("approach %q is %s — ready needs draft or paused", id, a.State)
		}
		p, err = ApproachPlan(tx, id)
		if err != nil {
			return err
		}
		if err = openPlan(tx, p.ID, false); err != nil {
			return err
		}
		tasks, err = ApproachTasks(tx, id)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			return fmt.Errorf("approach %q has no tasks — add at least one before ready", id)
		}
		if err = ValidateApproachReady(tx, id); err != nil {
			return err
		}
		snapshot, err = readySnapshot(tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err = check(ctx, p, tasks); err != nil {
		return nil, err
	}
	var out *Approach
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "approach", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := readySnapshot(tx, id)
		if err != nil {
			return err
		}
		if current != snapshot {
			return fmt.Errorf("approach %q changed during readiness — check it again", id)
		}
		out, err = transitionApproach(tx, id, ApproachReady)
		return err
	})
	return out, err
}

func TransitionTask(s *store.Store, id, to string) (*Task, error) {
	return TransitionTaskContext(context.Background(), s, id, to)
}
func TransitionTaskContext(ctx context.Context, s *store.Store, id, to string) (*Task, error) {
	var out *Task
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "task", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		var err error
		out, err = transitionTask(tx, id, to, true)
		return err
	})
	return out, err
}

func openPlan(s stateStore, id string, archive bool) error {
	p, err := GetPlan(s, id)
	if err != nil {
		return err
	}
	if p.State == PlanRetired || p.State == PlanComplete && !archive {
		return fmt.Errorf("plan %q is %s — reopen work explicitly before changing children", id, p.State)
	}
	return nil
}
func openApproach(s stateStore, id string) error {
	a, err := GetApproach(s, id)
	if err != nil {
		return err
	}
	if a.State == ApproachComplete || a.State == ApproachRetired {
		return fmt.Errorf("approach %q is %s — reopen work explicitly before changing tasks", id, a.State)
	}
	p, err := ApproachPlan(s, id)
	if err != nil {
		return err
	}
	return openPlan(s, p.ID, false)
}
func taskApproach(s stateStore, id string) (*Approach, error) {
	if _, err := GetTask(s, id); err != nil {
		return nil, err
	}
	rows, err := s.Query(`MATCH(a:Approach)-[:HAS_TASK]->(t:Task) WHERE t.id=$id RETURN a.id AS id`, map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("task %q needs exactly one owning approach", id)
	}
	return GetApproach(s, stringField(rows[0], "id"))
}
func openTask(s stateStore, id string) error {
	a, err := taskApproach(s, id)
	if err != nil {
		return err
	}
	return openApproach(s, a.ID)
}
func editableApproach(s stateStore, id string) error {
	if err := openApproach(s, id); err != nil {
		return err
	}
	a, err := GetApproach(s, id)
	if err != nil {
		return err
	}
	if a.State != ApproachDraft && a.State != ApproachPaused {
		return fmt.Errorf("approach %q is %s — edit tasks only in draft or paused", id, a.State)
	}
	return nil
}
func markEdited(s stateStore, id string) error {
	return s.Write(`MATCH (a:Approach) WHERE a.id=$id AND a.state='paused' SET a.needs_ready=true`, map[string]any{"id": id})
}
func reopenForRework(s stateStore, t *Task) error {
	a, err := taskApproach(s, t.ID)
	if err != nil {
		return err
	}
	p, err := ApproachPlan(s, a.ID)
	if err != nil {
		return err
	}
	if a.State == ApproachRetired || p.State == PlanRetired {
		return fmt.Errorf("task %q has a retired ancestor — rework refused", t.ID)
	}
	if a.State == ApproachComplete {
		if err := setState(s, "Approach", a.ID, ApproachPaused); err != nil {
			return err
		}
	}
	if p.State == PlanComplete {
		if err := setState(s, "Plan", p.ID, PlanActive); err != nil {
			return err
		}
	}
	t.Verification, t.VerificationReason = outcome.Unverified, "explicit rework invalidated previous verification"
	return s.Write(`MATCH (t:Task) WHERE t.id=$id SET t.verification=$v,t.verification_reason=$reason`, map[string]any{"id": t.ID, "v": string(t.Verification), "reason": t.VerificationReason})
}

// ReadinessSnapshot captures only readiness facts in one read transaction.
// Model judgment belongs outside this callback; artifacts are never copied.
func ReadinessSnapshot(s *store.Store, id string) (snapshot string, err error) {
	err = s.Update(func(tx *store.Tx) error {
		snapshot, err = readySnapshot(tx, id)
		return err
	})
	return
}
func readySnapshot(s stateStore, id string) (string, error) {
	a, err := GetApproach(s, id)
	if err != nil {
		return "", err
	}
	p, err := ApproachPlan(s, id)
	if err != nil {
		return "", err
	}
	ts, err := ApproachTasks(s, id)
	if err != nil {
		return "", err
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	type brief struct{ ID, Title, Prompt, State, Current, Target, Sketch, Acceptance, Tests, Verify, CheckCommand, AcceptanceCommand string }
	briefs := make([]brief, len(ts))
	for i, t := range ts {
		briefs[i] = brief{t.ID, t.Title, t.Prompt, t.State, t.Current, t.Target, t.Sketch, t.Acceptance, t.Tests, t.Verify, t.CheckCommand, t.AcceptanceCommand}
	}
	revisionIDs := []string{}
	for _, t := range ts {
		rs, e := scopeRevisions(s, "task", t.ID)
		if e != nil {
			return "", e
		}
		for _, r := range rs {
			revisionIDs = append(revisionIDs, r.ID)
		}
	}
	sort.Strings(revisionIDs)
	b, err := json.Marshal(struct {
		Revisions                                          []string
		PlanID, Goal, PlanState, ApproachID, ApproachState string
		NeedsReady                                         bool
		Tasks                                              []brief
	}{revisionIDs, p.ID, p.Goal, p.State, a.ID, a.State, a.NeedsReady, briefs})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// SameBrief binds check receipts to the admitted task; artifacts are excluded.
func (t *Task) SameBrief(other *Task) bool {
	return t.ID == other.ID && t.Title == other.Title && t.Prompt == other.Prompt && t.Current == other.Current && t.Target == other.Target && t.Sketch == other.Sketch && t.Acceptance == other.Acceptance && t.Tests == other.Tests && t.Verify == other.Verify && t.CheckCommand == other.CheckCommand && t.AcceptanceCommand == other.AcceptanceCommand
}

// Project scope is captured by the host; empty context retains the public owner API.
type projectKey struct{}

func WithProject(ctx context.Context, root string) context.Context {
	return context.WithValue(ctx, projectKey{}, root)
}
func ProjectRoot(ctx context.Context) string {
	root, _ := ctx.Value(projectKey{}).(string)
	return root
}
func CheckProject(s stateStore, kind, id, root string) error {
	if root == "" {
		return nil
	}
	match := map[string]string{"plan": "(p:Plan)", "approach": "(p:Plan)-[:HAS_APPROACH]->(x:Approach)", "task": "(p:Plan)-[:HAS_APPROACH]->(:Approach)-[:HAS_TASK]->(x:Task)"}[kind]
	if match == "" {
		return fmt.Errorf("unknown project node kind %q", kind)
	}
	node := "x"
	if kind == "plan" {
		node = "p"
	}
	rows, err := s.Query("MATCH "+match+" WHERE "+node+".id=$id RETURN p.project_root AS root", map[string]any{"id": id})
	if err != nil {
		return err
	}
	if len(rows) != 1 || stringField(rows[0], "root") != root {
		return fmt.Errorf("%s %q is missing, unbound or outside captured project — bind an unscoped plan explicitly", kind, id)
	}
	return nil
}
func BindProject(ctx context.Context, s *store.Store, id, root string) (*Plan, error) {
	if root == "" {
		return nil, fmt.Errorf("project scope unavailable")
	}
	var p *Plan
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		var err error
		p, err = GetPlan(tx, id)
		if err != nil {
			return err
		}
		if p.ProjectRoot != "" && p.ProjectRoot != root {
			return fmt.Errorf("plan already belongs to another project; moving projects is not supported")
		}
		if err = tx.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.project_root=$root`, map[string]any{"id": id, "root": root}); err != nil {
			return err
		}
		p.ProjectRoot = root
		return nil
	})
	return p, err
}

// ClaimTaskContext admits one direct executor against the judged owning snapshot.
func ClaimTaskContext(ctx context.Context, s *store.Store, id, snapshot, command string) (*Task, error) {
	return claimTaskContext(ctx, s, id, snapshot, command, false)
}

// ClaimTaskContractContext binds approval to this task rather than sibling lifecycle state.
func ClaimTaskContractContext(ctx context.Context, s *store.Store, id, snapshot, command string) (*Task, error) {
	return claimTaskContext(ctx, s, id, snapshot, command, true)
}
func claimTaskContext(ctx context.Context, s *store.Store, id, snapshot, command string, contract bool) (*Task, error) {
	var out *Task
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "task", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		t, err := GetTask(tx, id)
		if err != nil {
			return err
		}
		a, err := taskApproach(tx, id)
		if err != nil {
			return err
		}
		p, err := ApproachPlan(tx, a.ID)
		if err != nil {
			return err
		}
		current, err := readySnapshot(tx, a.ID)
		if contract {
			current, _, err = contractSnapshot(tx, "task", id, true)
		}
		if err != nil {
			return err
		}
		if snapshot == "" || current != snapshot {
			return fmt.Errorf("readiness changed before task claim; check again")
		}
		if p.State != PlanActive || a.NeedsReady || (a.State != ApproachReady && a.State != ApproachRunning) || t.State != TaskQueued {
			return fmt.Errorf("task_start needs Active plan, Ready/Running unchanged approach and Queued task")
		}
		if strings.TrimSpace(t.CheckCommand) == "" || command != "" && command != t.CheckCommand {
			return fmt.Errorf("task check %q must be nonblank and match configured check %q", t.CheckCommand, command)
		}
		if err := ValidateApproachReady(tx, a.ID); err != nil {
			return err
		}
		if a.State == ApproachReady {
			if _, err = transitionApproach(tx, a.ID, ApproachRunning); err != nil {
				return err
			}
		}
		out, err = transitionTask(tx, id, TaskRunning, false)
		if err != nil {
			return err
		}
		out.Attempts++
		out.Execution = outcome.Unknown
		out.Verification = outcome.Unverified
		if err = tx.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempts=$n`, map[string]any{"id": id, "n": out.Attempts}); err != nil {
			return err
		}
		return setTaskOutcome(tx, out)
	})
	return out, err
}

// TaskApproach reads the actual owning parent; callers cannot supply one.
func TaskApproach(s stateStore, id string) (*Approach, error) { return taskApproach(s, id) }
