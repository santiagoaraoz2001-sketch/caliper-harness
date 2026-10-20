package config

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
)

// Preferences are personal defaults. Model connections have their existing
// owner; project checks, graph/session paths and authored documents stay local.
type Preferences struct {
	Context   ContextConfig   `yaml:"context"`
	Params    ParamsConfig    `yaml:"params"`
	Auth      AuthConfig      `yaml:"auth"`
	Agent     AgentConfig     `yaml:"agent"`
	Subagents SubagentsConfig `yaml:"subagents"`
	Workflows WorkflowsConfig `yaml:"workflows"`
	UI        UIConfig        `yaml:"ui"`
	Recall    RecallConfig    `yaml:"recall"`
	Schemas   SchemaConfig    `yaml:"schemas"`
	Retention RetentionConfig `yaml:"retention"`
	Tools     ToolsConfig     `yaml:"tools"`
	Tabs      TabsConfig      `yaml:"tabs"`
}

var preferenceSections = []string{"context", "params", "auth", "agent", "subagents", "workflows", "ui", "recall", "schemas", "retention", "tools", "tabs"}

func preferences(c Config) Preferences {
	return Preferences{
		Context: c.Context, Params: c.Params, Auth: c.Auth, Agent: c.Agent,
		Subagents: c.Subagents, Workflows: c.Workflows, UI: c.UI, Recall: c.Recall,
		Schemas: c.Schemas, Retention: c.Retention, Tools: c.Tools, Tabs: c.Tabs,
	}
}
func (p Preferences) apply(c *Config) {
	c.Context = p.Context
	c.Params = p.Params
	c.Auth = p.Auth
	c.Agent = p.Agent
	c.Subagents = p.Subagents
	c.Workflows = p.Workflows
	c.UI = p.UI
	c.Recall = p.Recall
	c.Schemas = p.Schemas
	c.Retention = p.Retention
	c.Tools = p.Tools
	c.Tabs = p.Tabs
}
func PreferencesPath() (string, error) {
	p, e := ConnectionsPath()
	return filepath.Join(filepath.Dir(p), "settings.yaml"), e
}
func loadPreferences(c *Config) (bool, error) {
	path, e := PreferencesPath()
	if e != nil {
		return false, e
	}
	raw, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	p := preferences(*c)
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if e = d.Decode(&p); e != nil {
		return false, fmt.Errorf("personal settings %s: %w", path, e)
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return false, fmt.Errorf("personal settings %s contains trailing YAML documents", path)
	}
	p.apply(c)
	c.Context.CompactionAbsoluteTokens = 0
	return true, c.Validate()
}

// ManagedWorkspaceConfig distinguishes Caliper's automatic workspace storage
// from a project-authored file or an explicit alternate --config path.
func ManagedWorkspaceConfig(path string) bool {
	dir, e := WorkspaceDir()
	if e != nil {
		return false
	}
	absolute, e := filepath.Abs(path)
	return e == nil && filepath.Clean(absolute) == filepath.Join(dir, DefaultPath)
}

// SaveSettings saves personal defaults unless the user selected a project file.
// Known old automatic snapshots are backed up before their personal sections
// are removed. Unknown project keys refuse; no authored override is migrated.
func SaveSettings(path string, c Config) error {
	if c.ProjectOverride || !ManagedWorkspaceConfig(path) {
		return Save(path, c)
	}
	if e := c.Validate(); e != nil {
		return e
	}
	original, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil {
		if _, _, e = Load(path); e != nil {
			return e
		}
	}
	out, e := projectBytes(c, true)
	if e != nil {
		return e
	}
	pref, e := yaml.Marshal(preferences(c))
	if e != nil {
		return e
	}
	target, e := PreferencesPath()
	if e != nil {
		return e
	}
	if e = CreateWorkspaceConfigDir(path); e != nil {
		return e
	}
	if len(original) > 0 {
		backup := path + ".before-global-settings"
		if _, e = os.Stat(backup); os.IsNotExist(e) {
			if e = os.WriteFile(backup, original, 0600); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
	}
	if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return e
	}
	if e = writeConfig(target, pref); e != nil {
		return e
	}
	if e = SaveConnections(c); e != nil {
		return fmt.Errorf("personal settings saved; connections were not saved: %w", e)
	}
	if e = writeConfig(path, out); e != nil {
		return fmt.Errorf("personal settings saved; workspace settings were not updated: %w", e)
	}
	return nil
}
