package checkreport

import (
	"fmt"
	"slices"
	"strings"
)

type Contract struct {
	Format   string             `json:"format,omitempty"`
	Expected []string           `json:"expected,omitempty"`
	Stages   []StageRequirement `json:"stages,omitempty"`
}

type Prerequisite struct {
	Kind  string `yaml:"kind" json:"kind"` // path, executable, or manual (unknown)
	Value string `yaml:"value" json:"value"`
}

type EvidenceRequirement struct {
	ID       string `yaml:"id" json:"id"`
	Expected string `yaml:"expected" json:"expected"` // Authored observation, not an executable expression.
}

type StageRequirement struct {
	ID            string                `yaml:"id" json:"id"`
	Prerequisites []Prerequisite        `yaml:"prerequisites,omitempty" json:"prerequisites,omitempty"`
	Evidence      []EvidenceRequirement `yaml:"evidence,omitempty" json:"evidence,omitempty"`
}

func (c Contract) Clone() Contract {
	c.Expected = slices.Clone(c.Expected)
	c.Stages = slices.Clone(c.Stages)
	for i := range c.Stages {
		c.Stages[i].Prerequisites = slices.Clone(c.Stages[i].Prerequisites)
		c.Stages[i].Evidence = slices.Clone(c.Stages[i].Evidence)
	}
	return c
}
func (c Contract) IsZero() bool { return c.Format == "" && len(c.Expected) == 0 && len(c.Stages) == 0 }
func (c Contract) Equal(other Contract) bool {
	return c.Format == other.Format && slices.Equal(c.Expected, other.Expected) && slices.EqualFunc(c.Stages, other.Stages, func(a, b StageRequirement) bool {
		return a.ID == b.ID && slices.Equal(a.Prerequisites, b.Prerequisites) && slices.Equal(a.Evidence, b.Evidence)
	})
}

func validText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsRune(value, 0)
}

func (p Prerequisite) Validate() error {
	if p.Kind != "path" && p.Kind != "executable" && p.Kind != "manual" || !validText(p.Value, 8192) {
		return fmt.Errorf("prerequisite needs kind path, executable or manual and a nonblank, NUL-free value of at most 8192 bytes")
	}
	return nil
}

func (c Contract) Validate() error {
	if c.Format != "" && c.Format != "go-test-json" && c.Format != StageFormat && c.Format != EvidenceFormat {
		return fmt.Errorf("report_format must be empty, go-test-json, %s or %s", StageFormat, EvidenceFormat)
	}
	if len(c.Expected) > 256 || len(c.Expected) > 0 && c.Format == "" || (c.Format == StageFormat || c.Format == EvidenceFormat) && len(c.Expected) == 0 {
		return fmt.Errorf("structured reports require 1..256 declared stage IDs; Go expected tests may be empty")
	}
	expected := map[string]bool{}
	for _, id := range c.Expected {
		if !validText(id, 1024) || expected[id] {
			return fmt.Errorf("expected stage IDs must be unique, nonblank, NUL-free and at most 1024 bytes")
		}
		expected[id] = true
	}
	if len(c.Stages) > 0 && c.Format != EvidenceFormat {
		return fmt.Errorf("stage prerequisites and evidence require %s", EvidenceFormat)
	}
	seen := map[string]bool{}
	for _, stage := range c.Stages {
		if !expected[stage.ID] || seen[stage.ID] {
			return fmt.Errorf("stage requirements must uniquely name declared expected stages")
		}
		seen[stage.ID] = true
		if len(stage.Prerequisites) > 16 || len(stage.Evidence) > 256 {
			return fmt.Errorf("stage %q supports at most 16 prerequisites and 256 evidence observations", stage.ID)
		}
		prerequisites := map[Prerequisite]bool{}
		for _, p := range stage.Prerequisites {
			if err := p.Validate(); err != nil {
				return fmt.Errorf("stage %q: %w", stage.ID, err)
			}
			if prerequisites[p] {
				return fmt.Errorf("stage %q has a duplicate prerequisite", stage.ID)
			}
			prerequisites[p] = true
		}
		evidence := map[string]bool{}
		for _, e := range stage.Evidence {
			if !validText(e.ID, 1024) || !validText(e.Expected, 8192) || evidence[e.ID] {
				return fmt.Errorf("stage %q evidence needs unique nonblank IDs and expected observations", stage.ID)
			}
			evidence[e.ID] = true
		}
	}
	return nil
}

type EvidenceObservation struct {
	ID        string `json:"id"`
	Observed  string `json:"observed"`
	Reference string `json:"reference,omitempty"` // Data only; the host never opens this reference.
}

func matchEvidence(stage StageRequirement, observations []EvidenceObservation) error {
	if len(observations) > 256 {
		return fmt.Errorf("too many evidence observations")
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		if !validText(observation.ID, 1024) || !validText(observation.Observed, 8192) || len(observation.Reference) > 8192 || strings.ContainsRune(observation.Reference, 0) || seen[observation.ID] {
			return fmt.Errorf("duplicate or malformed evidence observation")
		}
		seen[observation.ID] = true
	}
	for _, evidence := range stage.Evidence {
		if !seen[evidence.ID] {
			return fmt.Errorf("missing required evidence %q (%s)", evidence.ID, evidence.Expected)
		}
	}
	return nil
}

// PrerequisiteReport is host-recorded presence evidence for one admitted check.
// It is never accepted from the checker's JSON or a user's confirmation.
type PrerequisiteReport struct {
	RunID        string                    `json:"run_id"`
	Workspace    string                    `json:"workspace"`
	Observations []PrerequisiteObservation `json:"observations,omitempty"`
}

type PrerequisiteObservation struct {
	StageID      string       `json:"stage_id"`
	Prerequisite Prerequisite `json:"prerequisite"`
	Status       string       `json:"status"`
	Reason       string       `json:"reason,omitempty"`
}

func (r PrerequisiteReport) Validate(c Contract, runID, workspace string) error {
	if runID == "" || workspace == "" || r.RunID != runID || r.Workspace != workspace {
		return fmt.Errorf("prerequisite observations do not belong to this check and workspace")
	}
	type key struct {
		stage        string
		prerequisite Prerequisite
	}
	wanted := map[key]bool{}
	for _, stage := range c.Stages {
		for _, p := range stage.Prerequisites {
			wanted[key{stage.ID, p}] = false
		}
	}
	for _, observation := range r.Observations {
		k := key{observation.StageID, observation.Prerequisite}
		seen, found := wanted[k]
		if !found || seen {
			return fmt.Errorf("unexpected or duplicate host prerequisite observation")
		}
		if observation.Status != "present" || observation.Prerequisite.Kind == "manual" {
			return fmt.Errorf("stage %q prerequisite %q remains %s: %s", observation.StageID, observation.Prerequisite.Value, observation.Status, observation.Reason)
		}
		wanted[k] = true
	}
	for _, stage := range c.Stages {
		for _, p := range stage.Prerequisites {
			if !wanted[key{stage.ID, p}] {
				return fmt.Errorf("stage %q lacks host prerequisite observation for %q", stage.ID, p.Value)
			}
		}
	}
	return nil
}
