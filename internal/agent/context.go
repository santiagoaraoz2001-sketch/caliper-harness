package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func contentSize(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		n += m.Size()
	}
	return n
}

// Begin restores provenance or explicitly marks compatibility History as legacy.
func (r *Runner) begin(prompt string) {
	c := r.Continuation.Snapshot()
	r.reviewRequest = ""
	if r.Reviewing && r.TaskSystem != "" && c.Coding != nil && c.Coding.TaskID != "" && c.Coding.Attempt > 0 {
		r.reviewRequest = prompt
		for _, entry := range c.Entries {
			if entry.Kind == "intent" {
				r.reviewRequest = ""
				break
			}
		}
		// Legacy history may carry human intent which begin is about to restore.
		if len(r.History) > 0 {
			r.reviewRequest = ""
		}
	}
	if c.Version == 0 {
		c.Version = 1
		for _, m := range r.History {
			kind := "history"
			if len(c.Entries) == 0 && m.Role == "user" {
				kind = "intent"
			}
			c.Entries = append(c.Entries, session.Entry{Message: m.Clone(), Kind: kind})
		}
	}
	for i := range c.Entries {
		if c.Entries[i].Kind == "task" {
			c.Entries[i].Kind = "history"
		}
	}
	kind := "task"
	hasIntent := false
	for _, e := range c.Entries {
		hasIntent = hasIntent || e.Kind == "intent"
	}
	if !hasIntent {
		kind = "intent"
	}
	c.Add(kind, "user", prompt, false)
	if c.ModelKey != r.ModelKey {
		c.BasisChars, c.BasisTokens = 0, 0
	}
	c.ModelKey = r.ModelKey
	c.Invalidate()
	base := r.basePrompt()
	prefix := []session.Entry{{Message: llm.Message{Role: "system", Content: base}, Kind: "injected"}}
	if r.Behavior != "" {
		prefix = append(prefix, session.Entry{Message: llm.Message{Role: "system", Content: r.Behavior}, Kind: "injected"})
	}
	if r.TaskSystem != "" {
		prefix = append(prefix, session.Entry{Message: llm.Message{Role: "system", Content: r.TaskSystem}, Kind: "injected"})
	}
	c.Entries = append(prefix, c.Entries...)
	if r.Memories != "" {
		c.Add("injected", "user", r.Memories, false)
	}
	r.Continuation = c
	r.codingStart()
}

// refreshWorkflow owns one immutable view per request; snapshots omit its header.
func (r *Runner) refreshWorkflow(ctx context.Context) error {
	if scope := r.PlanningScope(); r.WorkflowView != nil && scope != nil {
		reg, header, err := r.WorkflowView(scope)
		if err != nil {
			return err
		}
		r.Registry, r.workflowHeader = reg, header
	}
	if r.Assigned != nil && r.PlanningActive() && r.Registry != nil {
		if r.Registry != r.phaseView {
			r.phaseRegistry = r.Registry
		}
		r.phaseView = r.ProjectPhaseRegistry(r.phaseRegistry)
		r.Registry = r.phaseView
	}
	r.ensurePlanningPublication()
	// begin owns entry zero; Behavior and legacy system entries are not ours.
	if entries := r.Continuation.Entries; len(entries) > 0 && entries[0].Kind == "injected" && entries[0].Message.Role == "system" {
		entries[0].Message.Content = r.basePrompt()
	}
	if r.OnRegistryChange != nil && r.Registry != nil {
		r.OnRegistryChange(r.Registry.Names())
	}
	return nil
}

// ProjectPhaseRegistry applies selected-work admission before schemas or headers
// are emitted. It changes neither definitions nor lifecycle/approval authority.
func (r *Runner) ProjectPhaseRegistry(reg *tool.Registry) *tool.Registry {
	if r.Assigned == nil || !r.PlanningActive() || reg == nil {
		return reg
	}
	names := reg.Names()
	project := !r.assignedClaim && r.settlement == nil && r.Assigned.ProjectCoding != nil && r.Assigned.ProjectCoding(context.Background()) == nil
	if !r.assignedClaim && !project || r.settlement != nil {
		allowed := names[:0]
		for _, name := range names {
			discovery := name == "read" || name == "read_outside" || name == "find_outside" || name == "find" || name == "find_file" || name == "find_dir" || name == "grep" || name == "help"
			metadata := strings.HasPrefix(name, "workflow_") && !executionTool(name)
			if name == "plan" || name == "ask" || name == "graphql" || name == "prepare_task" || name == "ready_task" || name == "task_start" || metadata || discovery {
				allowed = append(allowed, name)
			}
		}
		names = allowed
	} else if a := r.Continuation.Assignment; a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}) {
		allowed := names[:0]
		for _, name := range names {
			if name != "prepare_task" && name != "ready_task" && name != "task_start" {
				allowed = append(allowed, name)
			}
		}
		names = allowed
	}
	return reg.View(names)
}

func (r *Runner) basePrompt() string {
	a := r.Continuation.Assignment
	direct := a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID})
	orientation := r.Prompts.Text("system.native.orientation")
	base := systemPrompt(r.Registry)
	if structured(r.Model) {
		base = orientation + " " + r.Prompts.Text("system.native.call_protocol")
	} else {
		base += "\n" + r.Prompts.Text("system.text.authority")
	}
	if r.Registry != nil {
		if _, available := r.Registry.Lookup("help"); available {
			key := "system.help_available"
			if graph, ok := r.Registry.Lookup("graphql"); ok && graph.GraphQLFields != nil {
				key = "system.help_available.knowledge"
			}
			base += "\n" + r.Prompts.Text(key)
		}
	}
	if r.ProjectContext != nil {
		base += "\n" + r.Prompts.Text("system.project_context")
	}
	if r.Registry != nil {
		_, construction := r.Registry.Lookup("workflow_plan")
		_, execution := r.Registry.Lookup("launch")
		if construction && execution && !direct {
			base += "\n" + r.Prompts.Text("system.workflow.hierarchy")
		}
	}
	if r.workflowHeader != "" {
		_, guided := r.Registry.Lookup("plan")
		if !guided || !r.PlanningActive() || direct || r.settlement != nil {
			base += "\n" + r.workflowHeader
		}
		if !direct && r.settlement == nil && (r.Assigned == nil || !r.PlanningActive()) {
			base += "\n" + r.Prompts.Text("system.workflow.focused")
		}
	}
	if direct {
		base += "\n" + r.Prompts.Format("system.task.execute", a.TaskID, a.Attempt)
		if r.Assigned.ClaimedBrief != nil {
			b := r.Assigned.ClaimedBrief()
			base += "\n" + r.Prompts.Format("system.task.brief", b["title"], b["prompt"], b["current"], b["target"], b["sketch"], b["acceptance"], b["tests"], b["verify"], b["acceptance_command"], b["check_command"])
		}
		base += "\n" + r.Prompts.Text("system.task.handoff")
	} else if r.settlement != nil {
		s := r.settlement
		base += fmt.Sprintf("\nSettled task %s attempt%d state=%s qualified=%t.", s.TaskID, s.Attempt, s.State, s.Qualified) + r.Prompts.Text("system.task.settled")
	} else if r.Assigned != nil && r.PlanningActive() {
		base += "\n" + r.Prompts.Text("system.task.prepare")
	}
	if r.CodingRequired {
		if r.CodingObligation() && (direct || !r.PlanningActive()) {
			// Dispatched workers have a host-installed protected role plus the
			// current task's bound coding state. Historical state alone is not
			// worker provenance and cannot select role instructions.
			boundWorker := r.TaskSystem != "" && !r.Reviewing && r.Assigned == nil && r.Continuation.Coding != nil && r.Continuation.Coding.Attempt > 0
			key := "system.coding.requirements"
			if !r.PlanningActive() && !boundWorker {
				key = "system.coding.direct"
			}
			base += "\n" + r.Prompts.Text(key)
		}
		base += "\n" + r.Prompts.Text("system.coding.knowledge")
		check := r.CheckCommand
		if r.Assigned != nil {
			check = r.Assigned.ConfiguredCommand
		}
		if strings.TrimSpace(check) == "" {
			check = r.Prompts.Text("system.coding.unset_check")
		}
		base += " Workspace: " + r.Workspace + ". Configured check: " + check
		if direct {
			base += " Bound task check: " + r.CheckCommand
		}
	}
	budget, _ := json.Marshal(struct {
		StepLimit        int
		RemainingSteps   *int
		RunLimitSeconds  int
		RemainingSeconds *int
	}{r.MaxSteps, r.remainingSteps(0), r.MaxRunSeconds, r.remainingRunSeconds(0)})
	base += "\n" + r.Prompts.Format("continuation.run_budget", string(budget))
	return base
}

// Claim readmission still belongs to this request boundary. Preparation
// exchanges and their reasoning remain exact. After successful readmission,
// only tagged standalone host preparation publications have become obsolete.
func (r *Runner) archiveClaimPreparation(ctx context.Context) error {
	if r.claimPreparationEnd <= 0 {
		return nil
	}
	if err := r.Continuation.Snapshot().Validate(); err != nil {
		return err
	}
	if err := r.Assigned.Admit(ctx); err != nil {
		return err
	}
	candidate := r.Continuation.Clone()
	candidate.Entries = nil
	for _, e := range r.Continuation.Entries {
		if !r.ownsPreparationPublication(e) {
			candidate.Entries = append(candidate.Entries, e)
		}
	}
	if len(candidate.Entries) != len(r.Continuation.Entries) {
		candidate.Invalidate()
		raw, err := candidate.Snapshot().Encode()
		if err != nil {
			return err
		}
		if r.Session == nil {
			return fmt.Errorf("claimed preparation retirement needs a durable session")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.Session.Append(ctx, r.SessionID, "continuation", raw); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		r.Continuation = candidate
	}
	r.claimPreparationEnd = 0
	return nil
}
