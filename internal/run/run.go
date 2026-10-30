// Package run builds configured agent Runners: one wiring path for
// the headless CLI and the TUI. Callers differ only in transport:
// stdin/stdout versus dialogs, stdout versus discard+tokens.
package run

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"specific-harness/internal/pathutil"
	"strings"
	"sync"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/files"
	graphTool "specific-harness/internal/tools/graph"
	"specific-harness/internal/tools/judge"
	"specific-harness/internal/tools/recall"
)

// ModelParams maps configured sampler values to the model wire parameters.
func ModelParams(p config.ParamsConfig) llm.Params {
	return llm.Params{Temperature: p.Temperature, TopK: p.TopK,
		RepetitionPenalty: p.RepetitionPenalty, Thinking: p.Thinking, MaxOutputTokens: int(p.MaxOutputTokens)}
}

// Deps carries everything a Runner needs. AskYesNo answers y/n
// prompts (auth + loop confirms); Ask answers ask-tool questions;
// In/Out back subagent registries (TUI passes denied stdin);
// ReplyOut sinks reply text; OnToken streams deltas (nil = off);
// History continues a chat (nil = fresh); Btw pops interjected
// user notes, one per step (nil = off).
type Deps struct {
	Services         *bash.ServiceOwner
	Tools            string // coding (default) or full exposure
	Cfg              config.Config
	Server           string
	Model            string
	MaxSteps         int
	Window           int
	Store            *store.Store
	Session          agent.SessionLog
	SessionID        string
	AskYesNo         func(ctx context.Context, prompt string) bool
	Ask              ask.Querier
	In               io.Reader
	Out              io.Writer
	ReplyOut         io.Writer
	OnToken          func(string)
	OnRegistryChange func([]string)
	History          []llm.Message
	Continuation     session.Continuation
	Btw              func() string
	BtwNote          func() (session.UserNote, bool)
	BtwAcknowledge   func(context.Context, session.UserNote) error
}

// New wires a ready Runner for prompt. Notes carry pre-prefixed
// non-fatal observations ("note: …", "warning: …") for the caller
// to disclose on its own channel.
func New(d Deps, prompt string) (*agent.Runner, []string, error) {
	var notes []string
	pc, err := d.Cfg.LoadPrompts()
	if err != nil {
		return nil, notes, err
	}
	d.Cfg.PromptCatalog = pc
	if err := d.Cfg.Auth.ValidateLists(); err != nil {
		return nil, notes, err
	}
	w, err := files.Capture(d.Cfg.Tools.Bash, pc)
	if err != nil {
		return nil, notes, err
	}
	if d.Tools != "" && d.Tools != "coding" && d.Tools != "full" && d.Tools != "workflow" {
		return nil, notes, fmt.Errorf("tools must be coding, full or explicitly selected workflow")
	}
	if d.Continuation.ToolView != "" && d.Continuation.ToolView != "coding" && d.Continuation.ToolView != "full" && d.Continuation.ToolView != "workflow" {
		return nil, notes, fmt.Errorf("invalid restored user tool view")
	}
	if d.Tools == "" {
		d.Tools = d.Continuation.ToolView
	}
	if d.Tools != "" {
		d.Continuation.ToolView = d.Tools
	}
	if d.Tools == "workflow" && d.Continuation.Workflow == nil {
		return nil, notes, fmt.Errorf("explicit /workflow scope is required")
	}
	reg := tool.NewRegistry(pc)
	var r *agent.Runner
	tools.RegisterAt(reg, d.Cfg, d.In, d.Out, d.Ask, w, func() *tool.Registry { return r.Registry })
	if d.Services != nil {
		reg = bindServiceOwner(reg, d.Cfg, w.Path, d.Services)
	}
	judgeFunc := configuredJudge(d.Cfg)
	if d.Cfg.Decision.Disabled {
		notes = append(notes, "note: decision model disabled; progress advice and call recovery off; PAT readiness and authorize-risky non-free tools ask for your approval; summary compaction also asks for your approval.")
	}
	gate := &auth.Gate{DecisionDisabled: d.Cfg.Decision.Disabled, Prompts: pc, Mode: d.Cfg.Auth.Mode, In: d.In, Out: d.Out, AskFunc: d.AskYesNo,
		Judge: judgeFunc, Threshold: d.Cfg.Decision.RiskThreshold,
		Judged: d.Cfg.Auth.JudgedTools, Free: d.Cfg.Auth.FreeTools, MaxArgChars: d.Cfg.Auth.MaxArgChars}
	// Judgment logging is best-effort observability: a failed append
	// must never break judging, so errors stay here, not in the run.
	logFn := func(kind, text string) {}
	if d.Session != nil {
		logFn = func(kind, text string) {
			_ = d.Session.Append(context.Background(), d.SessionID, kind, text)
		}
	}
	gate.Log = logFn
	gate.JudgmentContextChars = d.Cfg.Decision.JudgmentContextChars
	behavior, note, err := loadBehavior(d.Cfg)
	if err != nil {
		return nil, notes, err
	}
	behavior, guide, styleNote := projectStyle(d.Store, w.Path, behavior, pc)
	if styleNote != "" {
		if note != "" {
			note += "\n"
		}
		note += styleNote
	}
	if note != "" {
		notes = append(notes, note)
	}
	ld := launchDeps(d.Cfg, d.Store, d.Server, d.Model, d.MaxSteps, d.Window, d.In, d.Out, w, behavior, d.Services)
	ld = bindLaunchAuth(d.Cfg, ld, gate, d.Session, d.SessionID)
	ld.Log = logFn
	ld.JudgmentContextChars = d.Cfg.Decision.JudgmentContextChars
	client := llm.New(d.Server, d.Model).WithPlainTimeout(time.Duration(d.Cfg.Model.PlainRequestTimeoutSeconds) * time.Second).WithRuntime(d.Cfg.Model.Runtime).WithToolProtocol(d.Cfg.Model.ToolProtocol).WithParams(ModelParams(d.Cfg.Params))
	protocolNotice := "text compatibility; schema-first parser active"
	if client.ToolProtocol() == "structured" {
		protocolNotice = "native template support unconfirmed until actual call/receipt qualification"
	}
	notice := "prompt catalog " + pc.Source + "; " + client.ControlNotice() + "; tool protocol " + client.ToolProtocol() + "; " + protocolNotice + "; inactivity guard5m, agent.max_run_seconds=" + fmt.Sprint(d.Cfg.Agent.MaxRunSeconds)
	notice += fmt.Sprintf("; tool-output-only pruning after %d estimated context tokens (0 disables)", d.Cfg.Context.OutputPruneAfterTokens)
	if d.Cfg.Context.InflightOutputs != 0 || d.Cfg.Schemas.InflightCap != 0 {
		notice += "; legacy context.inflight_outputs and schemas.inflight_cap percentages are ignored; use context.output_prune_after_tokens"
	}
	notes = append(notes, "note: "+notice)
	r = &agent.Runner{Prompts: pc,
		WorkspaceExcludes: workspaceExcludes(d.Cfg, d.Store),
		Model:             client, Registry: reg, CodingRequired: true, CodingOnEffects: true, Workspace: w.Path, CheckCommand: d.Cfg.Coding.CheckCommand, CheckReport: d.Cfg.Coding.ReportContract(),
		Exec:     gate.Wrap(agent.Direct),
		MaxSteps: d.MaxSteps, MaxRunSeconds: int(d.Cfg.Agent.MaxRunSeconds), Out: d.ReplyOut,
		SessionID:       d.SessionID,
		Session:         d.Session,
		MaxTokens:       d.Window,
		ManualDecisions: d.Cfg.Decision.Disabled,
		Judge:           judgeFunc, JudgePreflight: configuredJudgePreflight(d.Cfg), Threshold: d.Cfg.Decision.RiskThreshold,
		LoopRepeat: d.Cfg.Agent.LoopRepeat, LoopStall: d.Cfg.Agent.LoopStall,
		ThinkingLoop:         ThinkingLoopPolicy(d.Cfg),
		Ask:                  gate.Confirm,
		JudgmentContextChars: d.Cfg.Decision.JudgmentContextChars,
		Compaction:           compactionFromConfig(d.Cfg),
		TemplatesDir:         d.Cfg.TemplatesDir(),
		Prune:                pruneFromConfig(d.Cfg),
		Behavior:             behavior,
		BehaviorNote:         note,
		OnToken:              d.OnToken,
		OnRegistryChange:     d.OnRegistryChange,
		CheckTimeoutSeconds:  d.Cfg.Tools.Bash.MaxTimeout,
		History:              d.History,
		Continuation:         d.Continuation,
		ModelKey:             d.Server + "|" + d.Model + "|" + d.Cfg.Model.Runtime + "|" + client.ToolProtocol(),
		Btw:                  d.Btw,
		BtwNote:              d.BtwNote,
		BtwAcknowledge:       d.BtwAcknowledge,
	}

	r.StyleEnforcement, r.Styleguide = d.Cfg.Decision.StyleEnforcement && guide != "", guide
	r.ProjectContext = projectContext(d.Cfg, d.Store, w.Path, client.CountTokens, &projectRecall{query: prompt, warn: func(err error) {
		logFn("warning", fmt.Sprintf("memory recall refresh failed: %v", err))
	}, publish: func(ctx context.Context, text string) error {
		if r.Session != nil {
			return r.Session.Append(ctx, r.SessionID, "recall", text)
		}
		return nil
	}})
	if r.BtwAcknowledge == nil && r.Session != nil {
		r.BtwAcknowledge = func(ctx context.Context, note session.UserNote) error {
			raw, err := json.Marshal(note)
			if err != nil {
				return err
			}
			return r.Session.Append(ctx, note.SessionID, "btw_ack", string(raw))
		}
	}
	ld.ReviewGate = gate.Wrap
	ld.ReadinessRequirements = r.ReadinessRequirements
	assigned := &launch.Assigned{Deps: ld, Root: w.Path, Command: d.Cfg.Coding.CheckCommand}
	r.TaskComments = assigned.Comments
	r.CommentAppend = func(ctx context.Context, text string) error { return assigned.AppendComment(ctx, text, r.SessionID) }
	r.CommentAppendNote = func(ctx context.Context, note session.UserNote) error {
		return assigned.AppendComment(ctx, note.Text, note.SessionID, note.ID)
	}
	assigned.Deps.ConfigureReviewer = func(review *agent.Runner) {
		// The main runner is synchronously settling this exact claim. Sharing
		// its queue here transfers the boundary consumer; fan-out workers never
		// receive this callback and cannot steal another task's comments.
		review.Btw = r.Btw
		review.BtwNote = r.BtwNote
		review.BtwAcknowledge = r.BtwAcknowledge
		review.GenerationInterrupt = r.GenerationInterrupt
		review.UserCommentPending = r.UserCommentPending
		review.CommentAppend = r.CommentAppend
		review.CommentAppendNote = r.CommentAppendNote
	}
	r.CommentReceipt = func(ctx context.Context, comment outcome.TaskComment) error {
		if r.Session == nil {
			return fmt.Errorf("task comment delivery needs a durable sink")
		}
		if err := plan.RecordCommentDelivery(plan.WithProject(ctx, w.Path), d.Store, comment, r.SessionID); err != nil {
			return err
		}
		raw, err := json.Marshal(comment)
		if err != nil {
			return err
		}
		return r.Session.Append(ctx, r.SessionID, "task_comment", string(raw))
	}
	r.Assigned = &agent.AssignedOps{Store: d.Store.Identity(), ConfiguredCommand: d.Cfg.Coding.CheckCommand, Reset: assigned.Reset, AcceptanceCommand: assigned.AcceptanceCommand, ClaimedBrief: assigned.ClaimedBrief, Admit: assigned.Admit, Settle: assigned.Settle,
		Start: func(ctx context.Context, id string) (*session.Assignment, string, error) {
			value, command, e := assigned.Start(ctx, id)
			if e == nil {
				r.CheckCommand = command
				bash, _ := reg.Lookup("bash")
				check := r.CheckDefinition(bash, assigned.AcceptanceCommand())
				previous, _ := reg.Lookup("check")
				if previous.Schema() != check.Schema() {
					updated := tool.NewRegistry(pc)
					for _, name := range reg.Names() {
						def, _ := reg.Lookup(name)
						if name == "check" {
							def = check
						}
						updated.Register(def)
					}
					reg = updated
					r.Registry, _, e = r.WorkflowView(r.Continuation.Workflow)
				}
				r.RetainHostCheck(check)
			}
			return value, command, e
		},
		ProjectCoding: func(ctx context.Context) error {
			return planningProjectCoding(ctx, ld, r.Continuation.Workflow)
		}, Facts: assigned.Facts}

	if err := registerOwning(reg, d.Cfg, ld, judgeFunc, d.Store, logFn, r); err != nil {
		return nil, notes, err
	}
	if err := auth.ValidateRegistry(d.Cfg.Auth, reg); err != nil {
		return nil, notes, err
	}
	r.Exec = gate.Wrap(r.CodingExec(agent.Direct))
	r.PlanningCompletion = func(scope *session.WorkflowScope) (*outcome.Report, error) {
		return planningCompletion(ld, scope)
	}
	r.WorkflowView = func(scope *session.WorkflowScope) (*tool.Registry, string, error) {
		guided, err := planningDefinition(ld, reg, scope, d.Cfg.Coding.CheckCommand)
		if err != nil {
			return nil, "", err
		}
		withPlan := func(source *tool.Registry) *tool.Registry {
			view := tool.NewRegistry(pc)
			a := r.Continuation.Assignment
			claimed := a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID})
			for _, name := range source.Names() {
				if name != "plan" {
					def, _ := source.Lookup(name)
					if name == "workflow_focus" && !claimed {
						def = planningFocusReturn(ld, scope, def)
					}
					view.Register(def)
				}
			}
			view.Register(guided)
			return view
		}
		if scope == nil {
			if d.Tools == "full" {
				return withPlan(reg), "", nil
			}
			return withPlan(reg.View(append(agent.ToolNames(d.Tools), "graphql"))), "", nil
		}
		return launch.FocusView(reg, d.Store, scope, func(view *tool.Registry) (*tool.Registry, error) {
			view = withPlan(view)
			for _, name := range []string{"graphql", "prepare_task", "ready_task", "task_start"} {
				def, ok := reg.Lookup(name)
				if !ok {
					return nil, fmt.Errorf("missing owning %s", name)
				}
				view.Register(def)
			}
			names := []string{"read", "read_outside", "find_outside", "find_file", "find_dir", "help", "grep", "ask"}
			a := r.Continuation.Assignment
			claimed := a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID})
			if r.ClaimedTaskScope(scope) || !claimed && planningProjectCoding(context.Background(), ld, scope) == nil {
				names = agent.ToolNames("coding")
			}
			for _, name := range names {
				if _, present := view.Lookup(name); present {
					continue
				}
				def, ok := reg.Lookup(name)
				if !ok {
					return nil, fmt.Errorf("missing owning %s", name)
				}
				view.Register(def)
			}
			if d.Tools != "workflow" && d.Tools != "full" && !r.ClaimedTaskScope(scope) {
				view = view.View(append(agent.ToolNames("coding"), "plan", "graphql", "workflow_focus"))
			}
			return r.ProjectPhaseRegistry(view), nil
		})
	}
	var viewErr error
	r.Registry, _, viewErr = r.WorkflowView(d.Continuation.Workflow)
	if viewErr != nil {
		return nil, notes, viewErr
	}

	view := d.Tools
	if view == "" {
		view = "coding plus project PAT/knowledge"
	}
	if d.Continuation.Workflow != nil {
		view += " (selected workflow node)"
	}
	notes[len(notes)-1] += "; tools: " + view
	return r, notes, nil
}

// registerOwning reuses the parent definitions for execution and list admission.
func registerOwning(reg *tool.Registry, cfg config.Config, ld launch.Deps, judgeFunc auth.Judge, s *store.Store, logFn func(string, string), r *agent.Runner) error {
	api := graph.NewWithReady(s, "v1", cfg.Sessions.Path, func(ctx context.Context, id string) (*plan.Approach, error) { return launch.Ready(ctx, ld, id) })
	api = api.WithContractApproval(ld.ApproveContract).WithDomainTokenCounter(modelTokenCounter(r.Model))
	if r.Assigned != nil {
		api = api.WithLifecycle(func(ctx context.Context, kind, id, to string) error {
			if kind == "task" && (to == plan.TaskRunning || to == plan.TaskDone) {
				return fmt.Errorf("model execution states are host-owned; use task_start and actual check/review settlement")
			}
			return ctx.Err()
		})
	}

	if r.Assigned != nil {
		captured, e := api.Scoped(ld.Workspace, r.SessionID, true)
		if e != nil {
			return e
		}
		sourcePath := ""
		// Receipt targets are transient advice, never workflow focus or assignment.
		adviceKind, adviceID, adviceTaskID := "", "", ""
		reset := r.Assigned.Reset
		r.Assigned.Reset = func() { reset(); sourcePath, adviceKind, adviceID, adviceTaskID = "", "", "", "" }
		r.Assigned.Observe = func(def tool.Definition, args tool.Args, result tool.Result, e error) {
			if def.Name == "graphql" && e == nil && !result.Failed && !result.Blocked && !result.Truncated {
				var receipt graph.Result
				if json.Unmarshal([]byte(result.Text), &receipt) == nil && !receipt.DataOmitted && len(receipt.Errors) == 0 {
					kind, id := "", ""
					for i, effect := range receipt.Effects {
						if effect.Status != "applied" || effect.ID == "" {
							return // Incomplete effects cannot install a new advice target.
						}
						switch effect.Field {
						case "prepareTask":
							if effect.Step != "setTaskBlock" {
								continue
							}
							if i < 3 {
								return
							}
							for j, step := range []string{"createPlan", "createApproach", "createTask", "setTaskBlock"} {
								part := receipt.Effects[i-3+j]
								if part.Key != effect.Key || part.Field != effect.Field || part.Step != step || part.Status != "applied" || part.ID == "" {
									return
								}
							}
							if receipt.Effects[i-1].ID != effect.ID {
								return
							}
							kind, id = "task", effect.ID
						case "createPlan", "transitionPlan", "retirePlan", "bindPlanProject":
							kind, id = "plan", effect.ID
						case "createApproach", "transitionApproach", "retireApproach":
							kind, id = "approach", effect.ID
						case "createTask", "setTaskBlock", "transitionTask":
							kind, id = "task", effect.ID
						}
					}
					if id != "" {
						adviceKind, adviceID = kind, id
						if kind == "task" {
							adviceTaskID = id
						}
					}
				}
			}
			if e == nil && !result.Failed && (def.Name == "read" || def.Name == "edit" || def.Name == "write") {
				path, _ := args["path"].(string)
				if !filepath.IsAbs(path) {
					path = filepath.Join(ld.Workspace, path)
				}
				parent, err := pathutil.EvalSymlinks(filepath.Dir(path))
				if err == nil {
					relative, err := filepath.Rel(ld.Workspace, filepath.Join(parent, filepath.Base(path)))
					if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
						sourcePath = relative
					}
				}
			}
		}
		owning := r.Assigned.Facts
		r.Assigned.Facts = func(ctx context.Context) (any, error) {
			assigned, e := owning(ctx)
			if e != nil {
				return nil, e
			}
			fields := `{id title goal state approaches{id title state qualified needsReady tasks{id title prompt state qualified current target sketch acceptance tests verify checkCommand acceptanceCommand}}}`
			raw, e := captured.ExecBounded(ctx, `query { draft:plans(state:"draft") `+fields+` active:plans(state:"active") `+fields+` }`, "", nil, cfg.Tools.GraphQL)
			if e != nil {
				return nil, e
			}
			var envelope graph.Result
			if e = json.Unmarshal(raw, &envelope); e != nil {
				return nil, e
			}
			var inventory map[string][]struct {
				ID, State  string
				Approaches []struct {
					ID, State string
					Tasks     []plan.Task
				}
			}
			if e = json.Unmarshal(envelope.Data, &inventory); e != nil {
				return nil, e
			}
			rows := 0
			preparation := cfg.PromptCatalog.Text("preparation.select_existing")
			checkpoint := agent.ProgressCheckpoint{Phase: "PREPARE", PATStep: "inspect-source", MissingFact: "unknown"}
			scope := r.Continuation.Workflow
			if scope == nil && adviceID != "" {
				// Local recipe identity only; never written into Continuation.
				scope = &session.WorkflowScope{Store: s.Identity(), Kind: adviceKind, ID: adviceID}
			}
			matchesStore := scope != nil && scope.Store == s.Identity()
			selected := scope == nil || matchesStore && scope.Kind == "root"
			for _, plans := range inventory {
				rows += len(plans)
				for _, p := range plans {
					if matchesStore && scope.Kind == "plan" && scope.ID == p.ID {
						selected = true
						checkpoint.SelectedID, checkpoint.PATStep = p.ID, "plan-"+p.State
						next, e := plan.NextPlan(p.State)
						if e != nil {
							return nil, e
						}
						if p.State == plan.PlanDraft && len(next) > 0 {
							preparation = cfg.PromptCatalog.Format("preparation.plan_transition", p.ID, next[0])
						} else {
							preparation = cfg.PromptCatalog.Format("preparation.author_approach", p.ID)
						}
					}
					rows += len(p.Approaches)
					for _, a := range p.Approaches {
						if matchesStore && scope.Kind == "approach" && scope.ID == a.ID {
							selected = true
							checkpoint.SelectedID, checkpoint.PATStep = a.ID, "approach-"+a.State
							next, e := plan.NextApproach(a.State)
							if e != nil {
								return nil, e
							}
							preparation = cfg.PromptCatalog.Format("preparation.inspect_approach", next, a.ID)
							if (a.State == plan.ApproachDraft || a.State == plan.ApproachPaused) && len(a.Tasks) == 0 {
								preparation = cfg.PromptCatalog.Format("preparation.author_task", a.ID)
							}
						}
						rows += len(a.Tasks)
						for _, t := range a.Tasks {
							chosenTask := matchesStore && (scope.Kind == "task" && scope.ID == t.ID || r.Continuation.Workflow == nil && scope.Kind == "approach" && scope.ID == a.ID && a.State == plan.ApproachReady && t.ID == adviceTaskID)
							if !chosenTask {
								continue
							}
							selected = true
							checkpoint.SelectedID, checkpoint.PATStep = t.ID, "task-"+t.State
							next, e := plan.NextTask(t.State)
							if e != nil {
								return nil, e
							}
							preparation = cfg.PromptCatalog.Format("preparation.inspect_task", next, t.ID)
							if (a.State == plan.ApproachDraft || a.State == plan.ApproachPaused) && t.State == plan.TaskQueued {
								preparation = cfg.PromptCatalog.Format("preparation.author_task_block", t.ID)
								readiness := plan.ValidateApproachReady(s, a.ID)
								ready := readiness == nil
								if readiness != nil && strings.Contains(readiness.Error(), fmt.Sprintf("%q", t.ID)) {
									checkpoint.MissingFact = readiness.Error()
									preparation += cfg.PromptCatalog.Format("preparation.readiness_refusal", readiness)
								}
								if readiness != nil && !strings.Contains(readiness.Error(), fmt.Sprintf("%q", t.ID)) {
									checkpoint.MissingFact = readiness.Error()
									preparation = cfg.PromptCatalog.Format("preparation.inspect_siblings", readiness, a.ID)
								}
								for _, sibling := range a.Tasks {
									if cfg.Workflows.VerifyFollowup && sibling.State == plan.TaskQueued && (strings.TrimSpace(sibling.CheckCommand) == "" || strings.TrimSpace(sibling.AcceptanceCommand) == "") {
										ready = false
										if sibling.ID != t.ID {
											preparation = cfg.PromptCatalog.Format("preparation.declare_sibling_commands", a.ID)
										}
									}
								}
								if _, available := r.Registry.Lookup("ready_task"); ready && available && (p.State == plan.PlanActive || p.State == plan.PlanDraft) {
									preparation = cfg.PromptCatalog.Format("preparation.ready_task", t.ID)
								} else if ready && p.State == plan.PlanActive {
									preparation = cfg.PromptCatalog.Format("preparation.ready_approach", a.ID)
								} else if ready && p.State == plan.PlanDraft {
									preparation = cfg.PromptCatalog.Format("preparation.activate_then_ready", p.ID, a.ID)
								}
							}
							if (a.State == plan.ApproachDraft || a.State == plan.ApproachPaused) && t.State == plan.TaskQueued {
								size, maximum, overage, err := launch.ReadinessBudget(ld, a.ID)
								if err != nil {
									return nil, err
								}
								if overage > 0 {
									preparation = cfg.PromptCatalog.Text("preparation.readiness_over_budget")
								}
								preparation += "\n" + cfg.PromptCatalog.Format("preparation.readiness_budget", fmt.Sprint(size), fmt.Sprint(maximum), fmt.Sprint(overage))
							}
							if p.State == plan.PlanActive && a.State == plan.ApproachReady && t.State == plan.TaskQueued {
								preparation = cfg.PromptCatalog.Format("preparation.claim_task", t.ID)
							}
						}
					}
				}
			}
			if !selected {
				preparation = cfg.PromptCatalog.Text("preparation.bounded_view_missing")
			}
			if rows == 0 && selected {
				preparation = cfg.PromptCatalog.Text("preparation.fresh_graphql")
				if _, available := r.Registry.Lookup("prepare_task"); available {
					preparation = cfg.PromptCatalog.Text("preparation.fresh_typed")
				}
			}
			if a := r.Continuation.Assignment; a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}) {
				checkpoint.Phase, checkpoint.PATStep, checkpoint.SelectedID = "EXECUTE", "claimed", a.TaskID
				preparation = cfg.PromptCatalog.Text("progress.execute")
				if c := r.Continuation.Coding; c != nil && c.Passed() {
					checkpoint.Phase, checkpoint.PATStep = "VALIDATE", "current-check-observed"
					preparation = cfg.PromptCatalog.Text("progress.validate")
				}
			}
			a := r.Continuation.Assignment
			if scope := r.PlanningScope(); scope != nil && (a == nil || !r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID})) {
				preparation, e = planningNextAction(ld, reg, scope, cfg.Coding.CheckCommand)
				if e != nil {
					return nil, e
				}
			}
			selection := memory.KnowledgeSelection{FilePath: sourcePath, SessionRef: r.SessionID}
			if a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}) {
				selection.TaskID = a.TaskID
			} else if scope := r.PlanningScope(); scope != nil && scope.Store == s.Identity() && scope.Kind == "task" {
				selection.TaskID = scope.ID
			}
			entries, e := memory.SelectedKnowledge(ctx, s, ld.Workspace, selection, cfg.Tools.GraphQL.MaxRows-rows, cfg.Tools.GraphQL.MaxResultBytes-len(raw))
			if e != nil {
				return nil, e
			}
			knowledge := []map[string]string{}
			for _, v := range entries {
				knowledge = append(knowledge, map[string]string{"key": v.Key, "body": v.Body, "task": v.TaskID, "path": v.FilePath})
			}
			projection, e := progressInventory(envelope.Data)
			if e != nil {
				return nil, e
			}
			return agent.BookkeepingFacts{Assignment: assigned, Unfinished: projection, Knowledge: knowledge, Disclosure: "same-project owner state; task briefs/knowledge are authored claims; observations retained separately", Preparation: preparation, Checkpoint: checkpoint}, nil
		}
	}
	def, err := graphTool.New(api, cfg.Tools.GraphQL, ld.Workspace, r.SessionID, s.Identity(), cfg.PromptCatalog)
	if err != nil {
		return err
	}
	if err := graphTool.PublishReferences(context.Background(), api, s, ld.Workspace, cfg.PromptCatalog); err != nil {
		return err
	}
	reg.Register(def)
	facades := graphTool.Preparation(def, s, cfg.Tools.GraphQL, cfg.Coding.CheckCommand, cfg.PromptCatalog)
	if r.Assigned != nil {
		start := r.TaskStartDefinition()
		facades[0], facades[1], start = bindPreparedTask(r, facades[0], facades[1], start)
		reg.Register(start)
	}
	for _, facade := range facades {
		reg.Register(facade)
	}
	reg.Register(launch.FocusDefinitionAt(s, cfg.Tools.Bash, ld.Workspace, cfg.PromptCatalog))
	reg.Register(launch.New(ld))
	for _, def := range launch.NewBuild(ld, cfg.Coding.CheckCommand) {
		reg.Register(def)
	}
	reg.Register(judge.New(judge.Deps{Judge: judgeFunc, Log: logFn, MaxContextChars: cfg.Decision.JudgmentContextChars}, cfg.PromptCatalog))
	reg.Register(recall.New(recall.Deps{Store: s, ProjectRoot: ld.Workspace, TopN: cfg.Recall.TopN, MaxChars: cfg.Recall.MaxChars, MinWord: cfg.Recall.MinWord}, cfg.PromptCatalog))
	bashDef, _ := reg.Lookup("bash")
	check := r.CheckDefinition(bashDef)
	r.RetainHostCheck(check)
	reg.Register(check)
	guided, err := planningDefinition(ld, reg, nil, cfg.Coding.CheckCommand)
	if err != nil {
		return err
	}
	reg.Register(guided)
	return nil
}

// LaunchDeps wires subagent fan-out: shared model client (share-safe),
// per-task registries, and the decision judge. Exported for runLaunch.
func LaunchDeps(cfg config.Config, s *store.Store, server, model string, maxSteps, window int, in io.Reader, out io.Writer, confirms ...func(context.Context, string) bool) (launch.Deps, error) {
	pc, err := cfg.LoadPrompts()
	if err != nil {
		return launch.Deps{Prompts: cfg.PromptCatalog}, err
	}
	cfg.PromptCatalog = pc
	if err := cfg.Auth.ValidateLists(); err != nil {
		return launch.Deps{Prompts: cfg.PromptCatalog}, err
	}
	w, e := files.Capture(cfg.Tools.Bash, pc)
	if e != nil {
		return launch.Deps{Prompts: cfg.PromptCatalog}, e
	}
	behavior, note, e := loadBehavior(cfg)
	if e != nil {
		return launch.Deps{Prompts: cfg.PromptCatalog}, e
	}
	behavior, _, styleNote := projectStyle(s, w.Path, behavior, pc)
	if styleNote != "" {
		if note != "" {
			note += "\n"
		}
		note += styleNote
	}
	d := launchDeps(cfg, s, server, model, maxSteps, window, in, out, w, behavior)
	d.BehaviorNote = note
	gate := &auth.Gate{DecisionDisabled: cfg.Decision.Disabled, Prompts: pc, Mode: cfg.Auth.Mode, In: in, Out: out, Judge: d.Judge, Threshold: cfg.Decision.RiskThreshold, Judged: cfg.Auth.JudgedTools, Free: cfg.Auth.FreeTools, MaxArgChars: cfg.Auth.MaxArgChars}
	if len(confirms) > 0 {
		gate.AskFunc = confirms[0]
	}
	d = bindLaunchAuth(cfg, d, gate, nil, "")
	reg := tool.NewRegistry(pc)
	tools.RegisterAt(reg, cfg, in, out, nil, w)
	if err := registerOwning(reg, cfg, d, d.Judge, s, nil, &agent.Runner{Prompts: pc, CheckCommand: cfg.Coding.CheckCommand, CheckReport: cfg.Coding.ReportContract()}); err != nil {
		return launch.Deps{Prompts: cfg.PromptCatalog}, err
	}
	if err := auth.ValidateRegistry(cfg.Auth, reg); err != nil {
		return launch.Deps{Prompts: cfg.PromptCatalog}, err
	}
	return d, nil
}
func launchDeps(cfg config.Config, s *store.Store, server, model string, maxSteps, window int, in io.Reader, out io.Writer, w *files.Workspace, behavior string, services ...*bash.ServiceOwner) launch.Deps {
	counter := llm.New(server, model).WithRuntime(cfg.Model.Runtime).CountTokens
	return launch.Deps{Prompts: cfg.PromptCatalog, CheckReport: cfg.Coding.ReportContract(),
		KnowledgeLimits:     cfg.Tools.GraphQL,
		ProjectContext:      projectContext(cfg, s, w.Path, counter),
		SessionsDir:         cfg.Sessions.Path,
		CheckTimeoutSeconds: cfg.Tools.Bash.MaxTimeout,
		NewKnowledge: func(sessionRef string, readOnly bool) (tool.Definition, error) {
			if sessionRef == "" {
				return tool.Definition{}, fmt.Errorf("child knowledge requires its actual task session")
			}
			fields := []string{"knowledge"}
			if !readOnly {
				fields = append(fields, "appendKnowledge")
			} else {
				fields = append(fields, "taskEvidence")
			}
			a := graph.New(s, "v1", cfg.Sessions.Path).WithDomainTokenCounter(counter).WithAllowedFields(fields...)
			if readOnly {
				task, err := plan.GetTask(s, sessionRef)
				if err != nil || task.Attempts <= 0 {
					return tool.Definition{}, fmt.Errorf("review evidence requires current task attempt")
				}
				a = a.WithReviewerEvidence(task.ID, task.Attempts)
			}
			return graphTool.New(a, cfg.Tools.GraphQL, w.Path, sessionRef, s.Identity(), cfg.PromptCatalog)
		},
		WorkspaceExcludes: workspaceExcludes(cfg, s),
		Workspace:         w.Path, Store: s, Behavior: behavior,
		NewModel: func() agent.Model {
			return llm.New(server, model).WithPlainTimeout(time.Duration(cfg.Model.PlainRequestTimeoutSeconds) * time.Second).WithRuntime(cfg.Model.Runtime).WithToolProtocol(cfg.Model.ToolProtocol).WithParams(ModelParams(cfg.Params))
		},
		NewRegistry: func() *tool.Registry {
			sub := tool.NewRegistry(cfg.PromptCatalog)
			var current *tool.Registry
			tools.RegisterAt(sub, cfg, in, out, nil, w, func() *tool.Registry { return current })
			if len(services) > 0 && services[0] != nil {
				sub = bindServiceOwner(sub, cfg, w.Path, services[0])
			}
			names := sub.Names()
			for i, name := range names {
				if name == "find" {
					names = append(names[:i], names[i+1:]...)
					break
				}
			}
			current = sub.View(append(names, "check", "graphql"))
			return current
		},
		MaxSteps: maxSteps, MaxRunSeconds: int(cfg.Agent.MaxRunSeconds),
		TemplatesDir:      cfg.TemplatesDir(),
		ParentWindow:      window,
		Sub:               cfg.Subagents,
		Wf:                cfg.Workflows,
		Judge:             configuredJudge(cfg),
		JudgePreflight:    configuredJudgePreflight(cfg),
		ManualReadiness:   cfg.Decision.Disabled,
		Threshold:         cfg.Decision.RiskThreshold,
		In:                in,
		Out:               out,
		CoherenceMaxChars: cfg.Decision.CoherenceMaxChars,
		LoopRepeat:        cfg.Agent.LoopRepeat,
		LoopStall:         cfg.Agent.LoopStall,
		ThinkingLoop:      ThinkingLoopPolicy(cfg),
		Compaction:        compactionFromConfig(cfg),
		Prune:             pruneFromConfig(cfg),
	}
}

func pruneFromConfig(cfg config.Config) agent.Prune {
	return agent.Prune{
		OutputsAgeTokens: cfg.Context.OutputPruneAfterTokens,
		SchemasKeep:      cfg.Schemas.KeepLatest,
	}
}

// ThinkingLoopPolicy shares validated limits with ordinary, child and manual summary runs.
func ThinkingLoopPolicy(cfg config.Config) agent.ThinkingLoop {
	if cfg.Decision.Disabled {
		return agent.ThinkingLoop{}
	}
	t := cfg.Agent.ThinkingLoop
	return agent.ThinkingLoop{Repeat: t.Repeat, MinUnitBytes: t.MinUnitBytes, MaxUnitBytes: t.MaxUnitBytes, SampleBytes: t.SampleBytes, MaxInputBytes: t.MaxInputBytes}
}

// loadBehavior reads the behavior document: missing file means no
// behavior (silent); unreadable fails; over-cap tail-truncates with
// a pre-prefixed note. Printing is the caller's job.
func loadBehavior(cfg config.Config) (shaped, note string, err error) {
	raw, err := os.ReadFile(cfg.Behavior.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("cannot read behavior file %s: %v", cfg.Behavior.Path, err)
	}
	shaped, truncated := agent.ShapeBehavior(string(raw), cfg.Behavior.MaxTokens, cfg.Behavior.CharsPerToken)
	if truncated {
		note = fmt.Sprintf("note: behavior file (%d chars) exceeds ~%d tokens — keeping the last %d chars",
			len([]rune(string(raw))), cfg.Behavior.MaxTokens, len([]rune(shaped)))
	}
	return shaped, note, nil
}

func compactionFromConfig(cfg config.Config) agent.Compaction {
	return agent.Compaction{
		Trigger:       cfg.Context.Trigger,
		Keep:          cfg.Context.Keep,
		Summary:       cfg.Context.Summary,
		Step:          cfg.Context.SummaryStep,
		Max:           cfg.Context.SummaryMax,
		WordsPerToken: cfg.Context.SummaryWordsPerToken,
	}
}

// bindLaunchAuth shares one human gate, authority cache and durable session across
// main/CLI/TUI dispatch and every worker/reviewer/check call in that dispatch.
func bindLaunchAuth(cfg config.Config, d launch.Deps, gate *auth.Gate, sink agent.SessionLog, id string) launch.Deps {
	var association sync.Once
	var associationErr error
	if id == "" {
		id = session.NewID()
	}
	if sink == nil {
		sink = graph.NewSessionLog(graph.New(d.Store, "v1", cfg.Sessions.Path))
	}
	gate.Record = func(ctx context.Context, event auth.ApprovalEvent) error {
		if d.Store == nil || d.Store.Identity() == "" {
			return fmt.Errorf("approval recording needs the owning open Store")
		}
		association.Do(func() { associationErr = sink.Append(ctx, id, "workspace", d.Workspace) })
		if associationErr != nil {
			return associationErr
		}
		event.Store, event.Workspace, event.Session = d.Store.Identity(), d.Workspace, id
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return sink.Append(ctx, id, "approval", string(raw))
	}
	if cfg.Auth.Mode == config.AuthRisky {
		// These owner operations enforce their complete contract approvals below.
		gate.Free = append(append([]string(nil), gate.Free...), "task_start", "launch", "workflow_plan", "workflow_approach", "workflow_task", "workflow_block", "workflow_ready", "workflow_transition")
	}
	d.AuthMode, d.Approve, d.Ask, d.ReviewGate = cfg.Auth.Mode, gate.Confirm, gate.Confirm, gate.Wrap
	d.ConfirmReadiness = gate.ConfirmReadiness
	d.ApproveContract = func(ctx context.Context, kind, ref string) error {
		return gate.ApproveContract(ctx, kind, ref, func(c context.Context) (string, string, error) {
			return plan.ContractSnapshot(plan.WithProject(c, d.Workspace), d.Store, kind, ref)
		})
	}
	d.OnTaskSettled = func(ctx context.Context, t *plan.Task) error {
		text := fmt.Sprintf("task %s attempt %d: execution=%s verification=%s qualified=%t", t.ID, t.Attempts, t.Execution, t.Verification, t.Qualified())
		if err := sink.Append(context.WithoutCancel(ctx), id, "task_settled", text); err != nil {
			return err
		}
		if d.Out != nil {
			if _, err := fmt.Fprintln(d.Out, text); err != nil {
				return err
			}
		}
		return nil
	}
	return d
}

// Exclude only our live bookkeeping from project-content observations.
func workspaceExcludes(cfg config.Config, s *store.Store) []string {
	paths := []string{cfg.Sessions.Path}
	if s != nil && s.Identity() != "" {
		paths = append(paths, s.Identity(), s.Identity()+".wal", s.Identity()+".lock")
	}
	return paths
}

// A nil judge disables optional advice without inventing a verdict. Required
// admission/authorization owners use explicit manual approval when disabled.
func configuredJudge(cfg config.Config) func(context.Context, decision.Request) (decision.Verdict, error) {
	if cfg.Decision.Disabled {
		return nil
	}
	c := decision.New(cfg.Decision.Server, cfg.Decision.Model, cfg.Decision.Device, time.Duration(cfg.Decision.TimeoutSeconds)*time.Second).WithSelection(decision.SelectionFor(cfg.Decision)).WithPrompts(cfg.PromptCatalog).WithInputLimit(cfg.Decision.MaxInputTokens).WithParams(ModelParams(cfg.Params))
	return c.Judge
}

func configuredJudgePreflight(cfg config.Config) func(decision.Request) error {
	if cfg.Decision.Disabled {
		return nil
	}
	c := decision.New(cfg.Decision.Server, cfg.Decision.Model, cfg.Decision.Device, time.Duration(cfg.Decision.TimeoutSeconds)*time.Second).WithSelection(decision.SelectionFor(cfg.Decision)).WithPrompts(cfg.PromptCatalog).WithInputLimit(cfg.Decision.MaxInputTokens).WithParams(ModelParams(cfg.Params))
	return c.Preflight
}

// Only the advisory view omits execution prose from actually qualified done tasks.
// Keep every goal, coverage/strategy, unknown field and current/unqualified brief.
func progressInventory(raw json.RawMessage) (json.RawMessage, error) {
	var groups map[string][]map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&groups); err != nil {
		return nil, err
	}
	for _, plans := range groups {
		for _, p := range plans {
			approaches, _ := p["approaches"].([]any)
			for _, item := range approaches {
				a, _ := item.(map[string]any)
				tasks, _ := a["tasks"].([]any)
				for _, item := range tasks {
					t, _ := item.(map[string]any)
					if t["state"] != plan.TaskDone || t["qualified"] != true {
						continue
					}
					removed := map[string]any{}
					for _, key := range []string{"prompt", "current", "sketch", "acceptance", "tests", "verify", "checkCommand", "acceptanceCommand"} {
						if value, present := t[key]; present {
							removed[key] = value
							delete(t, key)
						}
					}
					if len(removed) > 0 {
						body, err := json.Marshal(removed)
						if err != nil {
							return nil, err
						}
						t["settledBriefOmission"] = fmt.Sprintf("qualified done task execution prose: %d bytes, sha256=%x", len(body), sha256.Sum256(body))
					}
				}
			}
		}
	}
	return json.Marshal(groups)
}
