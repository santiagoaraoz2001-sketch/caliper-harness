package tui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

func TestAssistedSetupPromptOpensWithoutChangingConnections(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	before := m.Cfg
	if cmd := m.submit("/manage assisted"); cmd != nil {
		t.Fatal("opening the prompt scheduled external work")
	}
	path, err := config.ConnectionsPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{path, m.CfgPath, "caliper config --config", "Do not pass connections.yaml as --config", "system_one", "Only {model} is substituted"} {
		if !strings.Contains(m.HelpText, needle) {
			t.Errorf("prompt omitted %q", needle)
		}
	}
	if !m.HelpOpen || strings.Contains(m.HelpText, "{{") || !reflect.DeepEqual(before, m.Cfg) || m.Setup.Add != nil {
		t.Fatal("opening did not retain presentation-only behavior")
	}
	for _, name := range []string{path, m.CfgPath} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("opening wrote %s: %v", name, err)
		}
	}
	start := strings.Index(m.HelpText, "```yaml\n") + len("```yaml\n")
	end := strings.Index(m.HelpText[start:], "```") + start
	c := config.Defaults()
	v := config.Connections{Model: c.Model, Decision: c.Decision, Serve: c.Serve}
	decoder := yaml.NewDecoder(bytes.NewBufferString(m.HelpText[start:end]))
	decoder.KnownFields(true)
	if err := decoder.Decode(&v); err != nil {
		t.Fatalf("published connection example has unknown or invalid keys: %v", err)
	}
	c.Model, c.Decision, c.Serve = v.Model, v.Decision, v.Serve
	if err := c.Validate(); err != nil {
		t.Fatalf("published connection example is invalid: %v", err)
	}
}

func TestAssistedSetupManageKeyAndComposerOwnership(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	m.navigate(ViewManage, "")
	m.Input.SetValue("draft")
	m = key(m, "g")
	if m.HelpOpen || m.Input.Value() != "draftg" {
		t.Fatal("assisted key intercepted composer text")
	}
	m.Input.Reset()
	m = key(m, "g")
	if !m.HelpOpen || !strings.Contains(m.View(), "LLM-assisted setup") {
		t.Fatal("Manage shortcut did not open assisted setup")
	}
	m = viewportKey(m, tea.KeyEsc)
	m = key(m, "n")
	if m.Setup.Add == nil {
		t.Fatal("assisted setup displaced the existing manual form")
	}
	m.Setup.Add.Field = 0
	m = key(m, "g")
	if m.HelpOpen || !strings.Contains(m.Setup.Add.Values[0].Value(), "g") {
		t.Fatal("assisted key intercepted the connection form")
	}
}

func TestAssistedSetupCopyKeepsFullPromptAndReportsFailure(t *testing.T) {
	m := setupFixture(t, 80, 24)
	m.openAssistedSetup()
	original := m.HelpText
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd == nil {
		t.Fatal("copy shortcut did not schedule a clipboard write")
	}
	var copied string
	msg := copyAssistedPrompt(original, func(text string) error { copied = text; return nil })()
	m = viewportUpdate(m, msg)
	if copied != original || !m.HelpOpen || m.HelpNotice != "Full prompt copied" {
		t.Fatal("copy truncated the prompt or displaced the view")
	}
	msg = copyAssistedPrompt(original, func(string) error { return errors.New("no clipboard utility") })()
	m = viewportUpdate(m, msg)
	if !strings.Contains(m.HelpNotice, "select prompt text") || m.HelpText != original || !m.HelpOpen {
		t.Fatal("clipboard failure hid the prompt or claimed success")
	}
	m = viewportKey(m, tea.KeyEnd)
	if !strings.Contains(viewportFrame(t, m), "Report what was configured") {
		t.Fatal("last prompt paragraph cannot be reached")
	}
	m = viewportUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	viewportFrame(t, m)
	m = viewportKey(m, tea.KeyEsc)
	m = submit(m, "/help")
	if m.HelpText != "" || m.HelpNotice != "" || !strings.Contains(m.View(), "Commands & keys") {
		t.Fatal("ordinary help retained the assisted prompt")
	}
}

func TestAssistedSetupUsesAbsoluteCurrentProjectPath(t *testing.T) {
	m := setupFixture(t, 80, 24)
	m.CfgPath = "relative-settings.yaml"
	m.openAssistedSetup()
	abs, err := filepath.Abs(m.CfgPath)
	if err != nil || !strings.Contains(m.HelpText, abs) {
		t.Fatal("relative project path left ambiguous in copied prompt")
	}
}
