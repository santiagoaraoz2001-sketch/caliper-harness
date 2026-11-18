// Package workflowtools exposes the reusable workflow library as model
// tools. Every call opens the library file fresh, so a run sees user
// edits made through the TUI without restarting.
package workflowtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/workflowlib"
)

func text(v any) tool.Result {
	data, _ := json.MarshalIndent(v, "", "  ")
	return tool.Result{Text: string(data)}
}

func open(path string) (*workflowlib.Store, error) {
	s, err := workflowlib.Open(path)
	if err != nil {
		return nil, err
	}
	if err := workflowlib.EnsureSeed(s); err != nil {
		return nil, err
	}
	if err := workflowlib.EnsureSeedPack(s); err != nil {
		return nil, err
	}
	return s, nil
}

// Discovery summaries are the hard-scoped list shape: identity, purpose
// and applies_to tags only. Full bodies (instructions, steps, execution
// records) load one at a time through get/status, so listing the library
// at task start stays cheap as the library grows.
type blockSummary struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Purpose string `json:"purpose"`
}

type workflowSummary struct {
	ID             string   `json:"id"`
	Version        int      `json:"version"`
	Status         string   `json:"status"`
	Title          string   `json:"title"`
	Purpose        string   `json:"purpose"`
	AppliesTo      []string `json:"applies_to,omitempty"`
	BasedOn        string   `json:"based_on,omitempty"`
	RevisionReason string   `json:"revision_reason,omitempty"`
	RevisionKind   string   `json:"revision_kind,omitempty"`
}

type executionSummary struct {
	ID              string `json:"id"`
	WorkflowID      string `json:"workflow_id"`
	WorkflowVersion int    `json:"workflow_version"`
	Status          string `json:"status"`
	Current         int    `json:"current"`
	StepsTotal      int    `json:"steps_total"`
}

func blockSummaries(blocks []workflowlib.Block) []blockSummary {
	out := make([]blockSummary, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, blockSummary{b.ID, b.Version, b.Status, b.Title, b.Purpose})
	}
	return out
}

func workflowSummaries(workflows []workflowlib.Workflow) []workflowSummary {
	out := make([]workflowSummary, 0, len(workflows))
	for _, w := range workflows {
		out = append(out, workflowSummary{w.ID, w.Version, w.Status, w.Title, w.Purpose, w.AppliesTo, w.BasedOn, w.RevisionReason, w.RevisionKind})
	}
	return out
}

func lensSnapshot(s *workflowlib.Store, w workflowlib.Workflow, stepIndex int) (*tool.MethodLens, workflowlib.Block, error) {
	lens := &tool.MethodLens{
		WorkflowID: w.ID, WorkflowVersion: w.Version, Title: w.Title, Purpose: w.Purpose,
		AppliesTo: append([]string(nil), w.AppliesTo...), StepIndex: stepIndex,
	}
	for _, step := range w.Steps {
		block, ok := s.GetBlock(step.BlockID, step.BlockVersion)
		if !ok || block.Status != workflowlib.StatusPublished {
			return nil, workflowlib.Block{}, fmt.Errorf("workflow step %s@%d is not a published block", step.BlockID, step.BlockVersion)
		}
		lens.Steps = append(lens.Steps, tool.MethodStep{BlockID: step.BlockID, BlockVersion: step.BlockVersion, Title: block.Title})
	}
	if stepIndex < 0 {
		return lens, workflowlib.Block{}, nil
	}
	if stepIndex >= len(w.Steps) {
		return nil, workflowlib.Block{}, fmt.Errorf("step %d exceeds workflow %s steps %d", stepIndex+1, w.ID, len(w.Steps))
	}
	step := w.Steps[stepIndex]
	block, _ := s.GetBlock(step.BlockID, step.BlockVersion)
	lens.BlockID, lens.BlockVersion = block.ID, block.Version
	lens.PhaseTitle, lens.PhasePurpose = block.Title, block.Purpose
	lens.PhaseInputs = append([]string(nil), block.Inputs...)
	lens.PhaseOutputs = append([]string(nil), block.Outputs...)
	lens.CompletionEvidence, lens.FailureHandling = block.CompletionEvidence, block.FailureHandling
	switch block.Implementation.Type {
	case "tool":
		lens.Instructions = "Use the " + block.Implementation.Tool + " tool for this phase."
		if block.Implementation.Instructions != "" {
			lens.Instructions += " " + block.Implementation.Instructions
		}
	case "workflow":
		lens.Instructions = fmt.Sprintf("Follow nested published workflow %s@%d for this phase. Keep this lens unless you intentionally attach the nested workflow as a replacement method.", block.Implementation.WorkflowID, block.Implementation.WorkflowVersion)
		if block.Implementation.Instructions != "" {
			lens.Instructions += " " + block.Implementation.Instructions
		}
	default:
		lens.Instructions = block.Implementation.Instructions
	}
	return lens, block, nil
}

func publishedWorkflow(s *workflowlib.Store, id string, version int64) (workflowlib.Workflow, error) {
	if version == 0 {
		w, ok := s.LatestPublishedWorkflow(id)
		if !ok {
			return workflowlib.Workflow{}, fmt.Errorf("workflow %s has no published version", id)
		}
		return w, nil
	}
	w, ok := s.GetWorkflow(id, int(version))
	if !ok || w.Status != workflowlib.StatusPublished {
		return workflowlib.Workflow{}, fmt.Errorf("workflow %s@%d is not published", id, version)
	}
	return w, nil
}

func executionSummaries(executions []workflowlib.Execution) []executionSummary {
	out := make([]executionSummary, 0, len(executions))
	for _, e := range executions {
		out = append(out, executionSummary{e.ID, e.WorkflowID, e.WorkflowVersion, e.Status, e.Current, len(e.Steps)})
	}
	return out
}

func BuildBlock(path string, pc *prompts.Catalog) tool.Definition {
	return tool.Definition{
		Name:    "build_block",
		Desc:    pc.Text("tools.build_block.description"),
		Purpose: pc.Text("tools.build_block.purpose"),
		Prompts: pc,
		Params: []tool.Param{
			{Name: "action", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.build_block.parameters.action")},
			{Name: "id", Type: tool.TypeString, Desc: pc.Text("tools.build_block.parameters.id")},
			{Name: "title", Type: tool.TypeString, Desc: pc.Text("tools.build_block.parameters.title")},
			{Name: "field", Type: tool.TypeString, Desc: pc.Text("tools.build_block.parameters.field")},
			{Name: "value", Type: tool.TypeString, Desc: pc.Text("tools.build_block.parameters.value")},
			{Name: "fields", Type: tool.TypeString, Desc: pc.Text("tools.build_block.parameters.fields")},
			{Name: "expected_revision", Type: tool.TypeInt, Desc: pc.Text("tools.build_block.parameters.expected_revision")},
			{Name: "version", Type: tool.TypeInt, Desc: pc.Text("tools.build_block.parameters.version")},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			s, err := open(path)
			if err != nil {
				return tool.Result{}, err
			}
			id := strings.TrimSpace(strArg(args, "id"))
			switch strArg(args, "action") {
			case "start":
				b, err := s.CreateBlockDraft(id, strArg(args, "title"))
				return wrapBlock(b, err), err
			case "edit":
				b, err := s.EditBlock(id)
				return wrapBlock(b, err), err
			case "set":
				fields, err := fieldSet(args)
				if err != nil {
					return tool.Result{}, err
				}
				b, missing, err := s.SetBlockFields(id, intArg(args, "expected_revision"), fields)
				if err != nil {
					return tool.Result{}, err
				}
				return text(map[string]any{"block": b, "missing": missing, "next_field": first(missing)}), nil
			case "publish":
				b, err := s.PublishBlock(id, intArg(args, "expected_revision"))
				return wrapBlock(b, err), err
			case "get":
				b, ok := s.GetBlock(id, int(intArg(args, "version")))
				if !ok {
					return tool.Result{}, fmt.Errorf("unknown block %s", id)
				}
				return wrapBlock(b, nil), nil
			case "list":
				blocks, _ := s.List()
				return text(map[string]any{"blocks": blockSummaries(blocks)}), nil
			default:
				return tool.Result{}, fmt.Errorf("action must be start, edit, set, publish, get, or list")
			}
		},
	}
}

func wrapBlock(b workflowlib.Block, err error) tool.Result {
	if err != nil {
		return tool.Result{}
	}
	missing := workflowlib.MissingBlockFields(b)
	return text(map[string]any{"block": b, "missing": missing, "next_field": first(missing)})
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func BuildWorkflow(path string, pc *prompts.Catalog) tool.Definition {
	return tool.Definition{
		Name:    "build_workflow",
		Desc:    pc.Text("tools.build_workflow.description"),
		Purpose: pc.Text("tools.build_workflow.purpose"),
		Prompts: pc,
		Params: []tool.Param{
			{Name: "action", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.build_workflow.parameters.action")},
			{Name: "id", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.id")},
			{Name: "title", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.title")},
			{Name: "field", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.field")},
			{Name: "value", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.value")},
			{Name: "fields", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.fields")},
			{Name: "expected_revision", Type: tool.TypeInt, Desc: pc.Text("tools.build_workflow.parameters.expected_revision")},
			{Name: "version", Type: tool.TypeInt, Desc: pc.Text("tools.build_workflow.parameters.version")},
			{Name: "search_token", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.search_token")},
			{Name: "user_requested", Type: tool.TypeBool, Desc: pc.Text("tools.build_workflow.parameters.user_requested")},
			{Name: "source_id", Type: tool.TypeString, Desc: pc.Text("tools.build_workflow.parameters.source_id")},
			{Name: "source_version", Type: tool.TypeInt, Desc: pc.Text("tools.build_workflow.parameters.source_version")},
			{Name: "user_approved", Type: tool.TypeBool, Desc: pc.Text("tools.build_workflow.parameters.user_approved")},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			s, err := open(path)
			if err != nil {
				return tool.Result{}, err
			}
			id := strings.TrimSpace(strArg(args, "id"))
			switch strArg(args, "action") {
			case "start":
				if !boolArg(args, "user_requested") && strArg(args, "search_token") != s.SearchToken() {
					return tool.Result{}, fmt.Errorf("read the library first with library_workflow or build_workflow action=list and pass its current search_token; set user_requested=true only when the user explicitly requested a new workflow")
				}
				w, err := s.CreateWorkflowDraft(id, strArg(args, "title"))
				if err != nil {
					return tool.Result{}, err
				}
				return text(map[string]any{"workflow": w, "missing": workflowlib.MissingWorkflowFields(w), "next_field": first(workflowlib.MissingWorkflowFields(w)), "similar_workflows": workflowSummaries(s.SimilarWorkflows(w))}), nil
			case "fork":
				sourceID := strings.TrimSpace(strArg(args, "source_id"))
				if sourceID == "" {
					return tool.Result{}, fmt.Errorf("action=fork needs source_id")
				}
				source, err := publishedWorkflow(s, sourceID, intArg(args, "source_version"))
				if err != nil {
					return tool.Result{}, err
				}
				w, err := s.ForkWorkflow(source.ID, source.Version, id, strArg(args, "title"))
				return wrapWorkflow(w, err), err
			case "edit":
				w, err := s.EditWorkflow(id)
				return wrapWorkflow(w, err), err
			case "set":
				fields, err := fieldSet(args)
				if err != nil {
					return tool.Result{}, err
				}
				w, missing, err := s.SetWorkflowFields(id, intArg(args, "expected_revision"), fields)
				if err != nil {
					return tool.Result{}, err
				}
				return text(map[string]any{"workflow": w, "missing": missing, "next_field": first(missing)}), nil
			case "publish":
				current, ok := s.GetWorkflow(id, 0)
				if !ok {
					return tool.Result{}, fmt.Errorf("unknown workflow %s", id)
				}
				if current.Version > 1 && current.RevisionKind == "preference" && !boolArg(args, "user_approved") {
					return tool.Result{}, fmt.Errorf("publishing a preference revision needs user approval for this exact change; inform the user of revision_reason and set user_approved=true only after approval")
				}
				w, err := s.PublishWorkflow(id, intArg(args, "expected_revision"))
				if err != nil {
					return tool.Result{}, err
				}
				out := map[string]any{"workflow": w, "missing": workflowlib.MissingWorkflowFields(w), "next_field": ""}
				if w.Version > 1 {
					out["revision_notice"] = "Inform the user what changed and why: " + w.RevisionKind + " revision — " + w.RevisionReason
				}
				return text(out), nil
			case "get":
				w, ok := s.GetWorkflow(id, int(intArg(args, "version")))
				if !ok {
					return tool.Result{}, fmt.Errorf("unknown workflow %s", id)
				}
				return wrapWorkflow(w, nil), nil
			case "list":
				_, workflows := s.List()
				return text(map[string]any{"workflows": workflowSummaries(workflows), "search_token": s.SearchToken()}), nil
			default:
				return tool.Result{}, fmt.Errorf("action must be start, fork, edit, set, publish, get, or list")
			}
		},
	}
}

func wrapWorkflow(w workflowlib.Workflow, err error) tool.Result {
	if err != nil {
		return tool.Result{}
	}
	missing := workflowlib.MissingWorkflowFields(w)
	return text(map[string]any{"workflow": w, "missing": missing, "next_field": first(missing)})
}

func LibraryWorkflow(path string, pc *prompts.Catalog) tool.Definition {
	return tool.Definition{
		Name:    "library_workflow",
		Desc:    pc.Text("tools.library_workflow.description"),
		Purpose: pc.Text("tools.library_workflow.purpose"),
		Prompts: pc,
		Params: []tool.Param{
			{Name: "action", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.library_workflow.parameters.action")},
			{Name: "id", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.id")},
			{Name: "version", Type: tool.TypeInt, Desc: pc.Text("tools.library_workflow.parameters.version")},
			{Name: "execution_id", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.execution_id")},
			{Name: "step", Type: tool.TypeInt, Desc: pc.Text("tools.library_workflow.parameters.step")},
			{Name: "adaptation", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.adaptation")},
			{Name: "observations", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.observations")},
			{Name: "risks", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.risks")},
			{Name: "next_check", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.next_check")},
			{Name: "inputs", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.inputs")},
			{Name: "tag", Type: tool.TypeString, Desc: pc.Text("tools.library_workflow.parameters.tag")},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			s, err := open(path)
			if err != nil {
				return tool.Result{}, err
			}
			id := strings.TrimSpace(strArg(args, "id"))
			switch strArg(args, "action") {
			case "list":
				blocks, workflows := s.List()
				if tag := strings.TrimSpace(strArg(args, "tag")); tag != "" {
					workflows = s.WorkflowsByTag(tag)
				}
				return text(map[string]any{"blocks": blockSummaries(blocks), "workflows": workflowSummaries(workflows), "executions": executionSummaries(s.ListExecutions()), "search_token": s.SearchToken()}), nil
			case "enable", "disable":
				version := int(intArg(args, "version"))
				if version == 0 {
					w, ok := s.GetWorkflow(id, 0)
					if !ok || w.Status != workflowlib.StatusPublished {
						return tool.Result{}, fmt.Errorf("workflow %s has no published version to activate", id)
					}
					version = w.Version
				}
				if err := s.SetActivation(id, version, strArg(args, "action") == "enable"); err != nil {
					return tool.Result{}, err
				}
				act, _ := s.Activation(id)
				return text(map[string]any{"activation": act}), nil
			case "attach":
				w, err := publishedWorkflow(s, id, intArg(args, "version"))
				if err != nil {
					return tool.Result{}, err
				}
				act, ok := s.Activation(w.ID)
				if !ok || !act.Enabled || act.Version != w.Version {
					return tool.Result{}, fmt.Errorf("workflow %s@%d is not enabled; enable this exact published version before attaching it as a method lens", w.ID, w.Version)
				}
				inputs, err := parseInputs(strArg(args, "inputs"))
				if err != nil {
					return tool.Result{}, err
				}
				lens, block, err := lensSnapshot(s, w, 0)
				if err != nil {
					return tool.Result{}, err
				}
				lens.Inputs = inputs
				lens.Adaptation = strings.TrimSpace(strArg(args, "adaptation"))
				res := text(map[string]any{"method_lens": lens, "current_block": block, "evidence_status": "method guidance only; never task approval, settlement, configured check, or independent review"})
				res.MethodLens = &tool.MethodLensUpdate{Mode: "attach", Lens: lens}
				return res, nil
			case "phase":
				w, err := publishedWorkflow(s, id, intArg(args, "version"))
				if err != nil {
					return tool.Result{}, err
				}
				step64 := intArg(args, "step")
				if step64 < 1 || step64 > int64(len(w.Steps)+1) {
					return tool.Result{}, fmt.Errorf("step must be 1..%d for workflow %s; %d completes the lens", len(w.Steps)+1, w.ID, len(w.Steps)+1)
				}
				stepIndex := int(step64) - 1
				if int(step64) == len(w.Steps)+1 {
					stepIndex = -1
				}
				lens, block, err := lensSnapshot(s, w, stepIndex)
				if err != nil {
					return tool.Result{}, err
				}
				lens.Adaptation = strings.TrimSpace(strArg(args, "adaptation"))
				lens.Observations = strings.TrimSpace(strArg(args, "observations"))
				lens.Risks = strings.TrimSpace(strArg(args, "risks"))
				lens.NextCheck = strings.TrimSpace(strArg(args, "next_check"))
				res := text(map[string]any{"method_lens": lens, "current_block": block, "evidence_status": "method guidance only; never task approval, settlement, configured check, or independent review"})
				res.MethodLens = &tool.MethodLensUpdate{Mode: "phase", Lens: lens}
				return res, nil
			case "detach":
				res := text(map[string]any{"method_lens": nil})
				res.MethodLens = &tool.MethodLensUpdate{Mode: "detach"}
				return res, nil
			case "status":
				exec, ok := s.GetExecution(strArg(args, "execution_id"))
				if !ok {
					return tool.Result{}, fmt.Errorf("unknown execution %s", strArg(args, "execution_id"))
				}
				return text(map[string]any{"execution": exec, "current_block": currentBlock(s, exec), "evidence_status": "unverified model record; workflow steps never replace the task check or independent review"}), nil
			default:
				return tool.Result{}, fmt.Errorf("action must be list, enable, disable, attach, phase, detach, or status")
			}
		},
	}
}

func currentBlock(s *workflowlib.Store, exec workflowlib.Execution) any {
	if exec.Status != workflowlib.StatusActive || exec.Current >= len(exec.Steps) {
		return nil
	}
	step := exec.Steps[exec.Current]
	b, ok := s.GetBlock(step.BlockID, step.BlockVersion)
	if !ok {
		return step
	}
	return b
}

// fieldSet resolves action=set arguments into the fields to apply: either
// the single field/value pair or `fields` carrying a JSON object of
// field-name to value strings for a one-call multi-field set. Supplying
// both forms is ambiguous and refuses.
func fieldSet(args tool.Args) (map[string]string, error) {
	single := strings.TrimSpace(strArg(args, "field"))
	raw := strings.TrimSpace(strArg(args, "fields"))
	var obj map[string]string
	switch v := args["fields"].(type) {
	case map[string]any:
		obj = make(map[string]string, len(v))
		for k, val := range v {
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("fields values must be strings; field %q is not", k)
			}
			obj[k] = s
		}
	case string:
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &obj); err != nil {
				return nil, fmt.Errorf("fields must be a JSON object of field names to value strings: %w", err)
			}
		}
	}
	if len(obj) > 0 {
		if single != "" {
			return nil, fmt.Errorf("pass either field/value or fields, not both")
		}
		return obj, nil
	}
	if single == "" {
		return nil, fmt.Errorf("action=set needs field/value or fields")
	}
	return map[string]string{single: strArg(args, "value")}, nil
}

func strArg(args tool.Args, name string) string { v, _ := args[name].(string); return v }
func intArg(args tool.Args, name string) int64 {
	switch v := args[name].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
func boolArg(args tool.Args, name string) bool { v, _ := args[name].(bool); return v }

func parseInputs(raw string) (map[string]string, error) {
	out := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, fmt.Errorf("inputs must be a JSON object of strings or name=value lines: %w", err)
		}
		return out, nil
	}
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' }) {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("input %q must use name=value", strings.TrimSpace(line))
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}
