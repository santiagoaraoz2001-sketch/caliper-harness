package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	"specific-harness/internal/checkreport"
)

// TestingConfig is project-owned selection and advice, never verification state.
// Required task/check contracts keep their existing owners.
type TestingConfig struct {
	Guidance bool        `yaml:"guidance" json:"guidance"`
	Suites   []TestSuite `yaml:"suites,omitempty" json:"suites,omitempty"`
}

type TestPrerequisite = checkreport.Prerequisite

type TestSuite struct {
	ID             string                         `yaml:"id" json:"id"`
	Name           string                         `yaml:"name" json:"name"`
	Purpose        string                         `yaml:"purpose" json:"purpose"`
	Expected       string                         `yaml:"expected" json:"expected"`
	Location       string                         `yaml:"location,omitempty" json:"location,omitempty"`
	Exclusions     string                         `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
	Command        string                         `yaml:"command" json:"command"`
	Enabled        bool                           `yaml:"enabled" json:"enabled"`
	DisabledReason string                         `yaml:"disabled_reason,omitempty" json:"disabled_reason,omitempty"`
	Prerequisites  []TestPrerequisite             `yaml:"prerequisites,omitempty" json:"prerequisites,omitempty"`
	SpecKey        string                         `yaml:"spec_key,omitempty" json:"spec_key,omitempty"`
	SpecVersion    string                         `yaml:"spec_version,omitempty" json:"spec_version,omitempty"`
	ReportFormat   string                         `yaml:"report_format,omitempty" json:"report_format,omitempty"`
	ExpectedTests  []string                       `yaml:"expected_tests,omitempty" json:"expected_tests,omitempty"`
	Stages         []checkreport.StageRequirement `yaml:"stages,omitempty" json:"stages,omitempty"`
	Required       bool                           `yaml:"-" json:"required,omitempty"` // host-synthesized configured check only
}

var testSuiteID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (s TestSuite) ReportContract() checkreport.Contract {
	return (checkreport.Contract{Format: s.ReportFormat, Expected: s.ExpectedTests, Stages: s.Stages}).Clone()
}

func (c TestingConfig) Validate() error {
	if len(c.Suites) > 32 {
		return fmt.Errorf("testing.suites supports at most 32 suites")
	}
	seen := map[string]bool{}
	for _, s := range c.Suites {
		if !testSuiteID.MatchString(s.ID) || seen[s.ID] {
			return fmt.Errorf("testing suite id %q must be unique, 1–64 letters/digits/_/-, starting with a letter or digit", s.ID)
		}
		seen[s.ID] = true
		if s.Required {
			return fmt.Errorf("testing suite %s cannot own required verification; use the existing coding/task check contract", s.ID)
		}
		for _, f := range []struct{ name, value string }{{"name", s.Name}, {"purpose", s.Purpose}, {"expected", s.Expected}, {"command", s.Command}} {
			if strings.TrimSpace(f.value) == "" || len(f.value) > 8192 || strings.ContainsRune(f.value, '\x00') {
				return fmt.Errorf("testing suite %s %s must be nonblank, NUL-free and at most 8192 bytes", s.ID, f.name)
			}
		}
		if !s.Enabled && strings.TrimSpace(s.DisabledReason) == "" {
			return fmt.Errorf("disabled testing suite %s needs disabled_reason; previous results remain archived", s.ID)
		}
		for _, value := range []string{s.Location, s.Exclusions, s.DisabledReason, s.SpecKey, s.SpecVersion} {
			if len(value) > 8192 || strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("testing suite %s metadata must be NUL-free and at most 8192 bytes per field", s.ID)
			}
		}
		if (s.SpecKey == "") != (s.SpecVersion == "") {
			return fmt.Errorf("testing suite %s requires both spec_key and spec_version, or neither", s.ID)
		}
		if err := s.ReportContract().Validate(); err != nil {
			return fmt.Errorf("testing suite %s: %w", s.ID, err)
		}
		if len(s.Prerequisites) > 16 {
			return fmt.Errorf("testing suite %s allows at most 16 prerequisites", s.ID)
		}
		for _, p := range s.Prerequisites {
			if err := p.Validate(); err != nil {
				return fmt.Errorf("testing suite %s: %w", s.ID, err)
			}
		}
	}
	return nil
}

// SaveTesting changes only this project's testing section. It never persists
// inherited personal choices or writes connections/settings files.
func SaveTesting(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	var doc yaml.Node
	if err == nil {
		if _, _, err = LoadProject(path); err != nil {
			return err
		}
		if err = yaml.Unmarshal(raw, &doc); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("project configuration must be a mapping")
	}
	var value yaml.Node
	if err = value.Encode(c.Testing); err != nil {
		return err
	}
	found := false
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "testing" {
			root.Content[i+1] = &value
			found = true
		}
	}
	if !found {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "testing"}, &value)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err = CreateWorkspaceConfigDir(path); err != nil {
		return err
	}
	return writeConfig(path, out)
}
