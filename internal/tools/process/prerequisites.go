package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"specific-harness/internal/checkreport"
)

// PrerequisitePresence uses exact declarations, never probes, scans or executes.
// Presence is not evidence of runtime health; manual requirements remain unknown.
func PrerequisitePresence(root string, p checkreport.Prerequisite) (status, reason string) {
	if err := p.Validate(); err != nil {
		return "unknown", err.Error()
	}
	switch p.Kind {
	case "manual":
		return "unknown", "manual prerequisite is unverified; resolve it and update the declaration before running"
	case "path":
		path := p.Value
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if _, err := os.Stat(path); err != nil {
			return "missing", err.Error()
		}
	case "executable":
		name := p.Value
		if !filepath.IsAbs(name) && strings.ContainsAny(name, `/\`) {
			name = filepath.Join(root, name)
		}
		path, err := exec.LookPath(name)
		if err != nil || WindowsPythonAlias(path) {
			return "missing", "executable unavailable (including Windows Python execution aliases)"
		}
	}
	return "present", "declared prerequisite present; runtime health unverified until execution"
}

func ObservePrerequisites(root, runID string, contract checkreport.Contract) checkreport.PrerequisiteReport {
	report := checkreport.PrerequisiteReport{Workspace: root, RunID: runID}
	for _, stage := range contract.Stages {
		for _, p := range stage.Prerequisites {
			status, reason := PrerequisitePresence(root, p)
			report.Observations = append(report.Observations, checkreport.PrerequisiteObservation{StageID: stage.ID, Prerequisite: p, Status: status, Reason: reason})
		}
	}
	return report
}
