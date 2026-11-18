package agent

import (
	"fmt"
	"sort"
	"strings"

	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func (r *Runner) effectiveMethodLens() *session.MethodLens {
	if r.pendingMethodLensSet {
		return r.pendingMethodLens.Clone()
	}
	return r.Continuation.MethodLens.Clone()
}

func validMethodLensCandidate(lens *session.MethodLens) bool {
	if lens == nil || lens.WorkflowID == "" || lens.WorkflowVersion < 1 || len(lens.Steps) == 0 || lens.StepIndex < -1 || lens.StepIndex >= len(lens.Steps) {
		return false
	}
	if lens.StepIndex >= 0 {
		step := lens.Steps[lens.StepIndex]
		if step.BlockID == "" || step.BlockVersion < 1 || lens.BlockID != step.BlockID || lens.BlockVersion != step.BlockVersion {
			return false
		}
	}
	return true
}

// queueMethodLens validates one model-tool method update and stages the
// resulting state for the same durable settlement boundary as focus. A
// refused update changes nothing and is reported in the tool receipt.
func (r *Runner) queueMethodLens(update *tool.MethodLensUpdate) string {
	if update == nil {
		return ""
	}
	if r.Reviewing {
		return "method lens update refused: reviewers cannot attach or change a method lens"
	}
	switch update.Mode {
	case "attach":
		if !validMethodLensCandidate(update.Lens) {
			return "method lens update refused: invalid attached workflow snapshot"
		}
		candidate := update.Lens.Clone()
		candidate.ScopeKind, candidate.ScopeID, candidate.TaskID, candidate.Attempt = "", "", "", 0
		if current := r.effectiveMethodLens(); current != nil {
			candidate.Revision = current.Revision + 1
		} else {
			candidate.Revision = 1
		}
		r.pendingMethodLens, r.pendingMethodLensSet = candidate, true
		return ""
	case "phase":
		if !validMethodLensCandidate(update.Lens) {
			return "method lens update refused: invalid phase snapshot"
		}
		current := r.effectiveMethodLens()
		if current == nil || current.WorkflowID != update.Lens.WorkflowID || current.WorkflowVersion != update.Lens.WorkflowVersion {
			return "method lens update refused: phase does not match the attached workflow version"
		}
		if !r.pendingMethodLensSet && !r.methodLensActive(current) {
			return "method lens update refused: lens is outside its bound scope"
		}
		candidate := update.Lens.Clone()
		candidate.ScopeKind, candidate.ScopeID = current.ScopeKind, current.ScopeID
		candidate.TaskID, candidate.Attempt = current.TaskID, current.Attempt
		if candidate.Inputs == nil {
			candidate.Inputs = current.Inputs
		}
		if candidate.Adaptation == "" {
			candidate.Adaptation = current.Adaptation
		}
		if candidate.Observations == "" {
			candidate.Observations = current.Observations
		}
		if candidate.Risks == "" {
			candidate.Risks = current.Risks
		}
		if candidate.NextCheck == "" {
			candidate.NextCheck = current.NextCheck
		}
		candidate.Revision = current.Revision + 1
		r.pendingMethodLens, r.pendingMethodLensSet = candidate, true
		return ""
	case "detach":
		r.pendingMethodLens, r.pendingMethodLensSet = nil, true
		return ""
	default:
		return fmt.Sprintf("method lens update refused: unknown mode %q", update.Mode)
	}
}

func methodLensScopeMatchesAssignment(lens *session.MethodLens, a *session.Assignment) bool {
	if lens == nil || a == nil {
		return false
	}
	switch lens.ScopeKind {
	case "session", "root":
		return true
	case "plan":
		return lens.ScopeID == a.PlanID
	case "approach":
		return lens.ScopeID == a.ApproachID
	case "task":
		return lens.ScopeID == a.TaskID
	default:
		return false
	}
}

func (r *Runner) bindMethodLens(candidate *session.Continuation) {
	lens := candidate.MethodLens
	if lens == nil || lens.ScopeKind != "" {
		return
	}
	if a := candidate.Assignment; a != nil && r.assignedClaim && r.settlement == nil {
		lens.ScopeKind, lens.ScopeID, lens.TaskID, lens.Attempt = "task", a.TaskID, a.TaskID, a.Attempt
		return
	}
	if scope := candidate.Workflow; scope != nil {
		lens.ScopeKind, lens.ScopeID = scope.Kind, scope.ID
		return
	}
	lens.ScopeKind, lens.ScopeID = "session", r.SessionID
}

func (r *Runner) methodLensActive(lens *session.MethodLens) bool {
	if lens == nil || r.Reviewing {
		return false
	}
	if r.settlement != nil && lens.ScopeKind == "task" && lens.ScopeID == r.settlement.TaskID {
		return false
	}
	switch lens.ScopeKind {
	case "session", "root", "":
		return true
	case "plan", "approach", "task":
		if a := r.Continuation.Assignment; a != nil && r.assignedClaim && r.settlement == nil && methodLensScopeMatchesAssignment(lens, a) {
			return true
		}
		scope := r.Continuation.Workflow
		return scope != nil && scope.Kind == lens.ScopeKind && scope.ID == lens.ScopeID
	default:
		return false
	}
}

func (r *Runner) methodLensPrompt() string {
	lens := r.Continuation.MethodLens
	if !r.methodLensActive(lens) {
		return ""
	}
	pc := r.Prompts
	parts := []string{pc.Format("continuation.method_lens.intro", lens.WorkflowID, lens.WorkflowVersion, lens.Title, lens.Purpose)}
	if len(lens.AppliesTo) > 0 {
		parts = append(parts, pc.Format("continuation.method_lens.applies_to", strings.Join(lens.AppliesTo, ", ")))
	}
	if len(lens.Inputs) > 0 {
		keys := make([]string, 0, len(lens.Inputs))
		for key := range lens.Inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, key := range keys {
			pairs = append(pairs, key+"="+lens.Inputs[key])
		}
		parts = append(parts, pc.Format("continuation.method_lens.inputs", strings.Join(pairs, "; ")))
	}
	steps := make([]string, 0, len(lens.Steps))
	for i, step := range lens.Steps {
		label := step.Title
		if label == "" {
			label = step.BlockID
		}
		if i == lens.StepIndex {
			label += " [current]"
		}
		steps = append(steps, fmt.Sprintf("%d. %s", i+1, label))
	}
	parts = append(parts, pc.Format("continuation.method_lens.steps", strings.Join(steps, "; ")))
	if lens.StepIndex >= 0 {
		parts = append(parts, pc.Format("continuation.method_lens.phase", lens.StepIndex+1, len(lens.Steps), lens.PhaseTitle, lens.PhasePurpose, lens.Instructions, lens.CompletionEvidence, lens.FailureHandling))
	} else {
		parts = append(parts, pc.Text("continuation.method_lens.complete"))
	}
	for _, item := range []struct{ key, value string }{
		{"continuation.method_lens.adaptation", lens.Adaptation},
		{"continuation.method_lens.observations", lens.Observations},
		{"continuation.method_lens.risks", lens.Risks},
		{"continuation.method_lens.next_check", lens.NextCheck},
	} {
		if strings.TrimSpace(item.value) != "" {
			parts = append(parts, pc.Format(item.key, item.value))
		}
	}
	return strings.Join(parts, "\n")
}
