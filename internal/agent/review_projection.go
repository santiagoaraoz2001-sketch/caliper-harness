package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"specific-harness/internal/prompts"
)

// ReviewInputs keeps the existing launch-owned review components separate from
// human intent. No component is inferred from rendered prompt prose. Unknown
// templates retain their complete request instead of using this projection.
type ReviewInputs struct {
	Instructions, PlanContext, ApproachContext string `json:",omitempty"`
	Title, TaskRequest, Target, Acceptance     string `json:",omitempty"`
	Workspace, CheckCommand, AcceptanceCommand string `json:",omitempty"`
	ExecutionRequest                           string `json:",omitempty"`
	Current, Sketch, Tests, Verify             string `json:",omitempty"`
	HostExecutionEvidence                      string `json:",omitempty"`
	UnverifiedWorkerReport                     string `json:",omitempty"`
	FrozenTaskSource, CheckSource              string `json:",omitempty"`
	FileObservations                           json.RawMessage
	InitialCheck                               json.RawMessage `json:",omitempty"`
}

func (p *progressPacket) projectReview(inputs *ReviewInputs) {
	if !p.hostReviewRequest || inputs == nil || p.AuthoredTaskInstructions == "" {
		return
	}
	copy := *inputs
	copy.FileObservations = bytes.Clone(inputs.FileObservations)
	copy.InitialCheck = bytes.Clone(inputs.InitialCheck)
	// The current typed check is already protected in Coding. A different
	// initial receipt remains explicitly historical, not silently replaced.
	current, err := json.Marshal(p.Coding)
	if err == nil && bytes.Equal(current, copy.InitialCheck) {
		copy.InitialCheck = nil
		if p.Coding != nil && copy.Workspace == p.Coding.Workspace && copy.CheckCommand == p.Coding.Command && copy.AcceptanceCommand == p.Coding.AcceptanceCommand {
			copy.Workspace, copy.CheckCommand, copy.AcceptanceCommand = "", "", ""
			copy.CheckSource = "Coding (identical workspace, commands and initial typed check)"
		}
	}
	role := prompts.Builtin().Format("agent.reviewer_role", copy.Title, copy.TaskRequest, copy.Target, copy.Acceptance)
	if role == p.AuthoredTaskInstructions {
		copy.Title, copy.TaskRequest, copy.Target, copy.Acceptance = "", "", "", ""
		copy.FrozenTaskSource = "AuthoredTaskInstructions (identical title, request, target and acceptance)"
	}
	p.Review = &copy
	p.HostReviewAttachment = &progressOmission{
		Field:  "OriginalUserIntent (launch-owned review attachment; components in Review, AuthoredTaskInstructions and Coding)",
		SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(p.OriginalUserIntent))), Bytes: len(p.OriginalUserIntent),
	}
	p.OriginalUserIntent = ""
}
