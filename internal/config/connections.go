package config

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
)

// Connections contains reusable machine choices, never project state or checks.
type Connections struct {
	Prompts  PromptsConfig  `yaml:"prompts,omitempty"` // machine-wide edited shipped catalog; project overrides remain local
	Model    ModelConfig    `yaml:"model"`
	Decision DecisionConfig `yaml:"decision"`
	Serve    ServeConfig    `yaml:"serve"`
}

func ConnectionsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "caliper", "connections.yaml"), nil
}
func loadConnections(c *Config) error {
	path, err := ConnectionsPath()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	v := Connections{Model: c.Model, Decision: c.Decision, Serve: c.Serve}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err = d.Decode(&v); err != nil {
		return fmt.Errorf("connections %s: %w", path, err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("connections %s contains trailing YAML documents", path)
	}
	c.Model, c.Decision, c.Serve = v.Model, v.Decision, v.Serve
	c.Prompts = v.Prompts
	return c.Validate()
}

// SaveConnections atomically saves the reusable selections. Project config is
// separate and continues to override these machine defaults.
func SaveConnections(c Config) error { return saveConnections(c, nil) }

func saveConnections(c Config, preservedPrompts *PromptsConfig) error {
	if preservedPrompts == nil {
		current := Defaults()
		if err := loadConnections(&current); err != nil {
			return err
		}
		preservedPrompts = &current.Prompts
	}
	if err := c.Validate(); err != nil {
		return err
	}
	path, err := ConnectionsPath()
	if err != nil {
		return err
	}
	raw, err := yaml.Marshal(Connections{Prompts: *preservedPrompts, Model: c.Model, Decision: c.Decision, Serve: c.Serve})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connections-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ProjectBytes keeps reusable choices out of a setup-managed project's file.
func ProjectBytes(c Config) ([]byte, error) { return projectBytes(c, false) }

// WorkspaceBytes retains legacy values until personal defaults have been saved.
func WorkspaceBytes(c Config) ([]byte, error) {
	p, err := PreferencesPath()
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return projectBytes(c, err == nil)
}
func projectBytes(c Config, omitPreferences bool) ([]byte, error) {
	raw, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var n map[string]any
	if err = yaml.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	delete(n, "model")
	delete(n, "decision")
	delete(n, "serve")
	if omitPreferences {
		for _, key := range preferenceSections {
			delete(n, key)
		}
	}
	return yaml.Marshal(n)
}

// SaveEffective persists connection selections without changing personal settings.
// Managed workspace files do not freeze shared selections; explicit project files
// retain their existing override behavior.
func SaveEffective(path string, c Config) error {
	if !c.ProjectOverride && ManagedWorkspaceConfig(path) {
		if err := c.Validate(); err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			if _, _, err := Load(path); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		out, err := WorkspaceBytes(c)
		if err != nil {
			return err
		}
		if err := CreateWorkspaceConfigDir(path); err != nil {
			return err
		}
		if err := writeConfig(path, out); err != nil {
			return err
		}
		if err := SaveConnections(c); err != nil {
			return fmt.Errorf("workspace saved but reusable connections failed: %w", err)
		}
		return nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Save(path, c)
	}
	if err != nil {
		return err
	}
	var n map[string]any
	if err = yaml.Unmarshal(raw, &n); err != nil {
		return err
	}
	for _, key := range []string{"model", "decision", "serve"} {
		if _, explicit := n[key]; explicit {
			return Save(path, c)
		}
	}
	if err = c.Validate(); err != nil {
		return err
	}
	out, err := ProjectBytes(c)
	if err != nil {
		return err
	}
	if err = writeConfig(path, out); err != nil {
		return err
	}
	if err = SaveConnections(c); err != nil {
		return fmt.Errorf("project saved but reusable connections failed: %w", err)
	}
	return nil
}

// A failed write leaves the previous settings file intact on all platforms.
func writeConfig(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".caliper-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
