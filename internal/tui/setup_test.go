package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
	"specific-harness/internal/tools/ask"
	"strings"
	"testing"
)

func TestSetupSavedTabCanReopenAndSaveWithoutLosingUnreviewedSettings(t *testing.T) {
	m := setupFixture(t, 80, 24)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m.Setup.Check.SetValue("go test ./...")
	m = viewportKey(m, tea.KeyEnter)
	m.Cfg.Model.ToolProtocol = "text"
	m.Cfg.Model.PlainRequestTimeoutSeconds = 777
	m = viewportKey(m, tea.KeyEnter)
	if m.Cfg.Model.ToolProtocol != "text" || m.Cfg.Model.PlainRequestTimeoutSeconds != 777 {
		t.Fatal("Setup reverted unreviewed Settings values")
	}
	m = viewportKey(m, tea.KeyCtrlN)
	if !m.setupActive() || !m.Setup.Started || m.Setup.Path == "" || m.Setup.Check.Value() != "go test ./..." {
		t.Fatal("retained saved tab lost initialized setup state")
	}
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	if m.Setup.Err != "" || m.setupActive() {
		t.Fatalf("second save failed: %s", m.Setup.Err)
	}
}

func TestSetupQuestionOwnsInput(t *testing.T) {
	m := setupFixture(t, 80, 24)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	before := m.Setup.Check.Value()
	ch := make(chan []ask.Answer, 1)
	um, _ := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "ask", prompt: "Your answer?", questions: []ask.Question{{Text: "Your answer?"}}, askDone: ch})
	m = um.(Model)
	m = key(m, "y")
	if m.Input.Value() != "y" || m.Setup.Check.Value() != before {
		t.Fatal("Setup intercepted question typing")
	}
	m = viewportKey(m, tea.KeyEnter)
	select {
	case answer := <-ch:
		if len(answer) != 1 || answer[0].Picked != "y" {
			t.Fatal(answer)
		}
	default:
		t.Fatal("Setup intercepted question Enter")
	}
	if m.Setup.Stage != 2 {
		t.Fatal("question changed setup stage")
	}
}

func TestSetupCompactShowsActiveValueAndError(t *testing.T) {
	m := setupFixture(t, 40, 12)
	if !strings.Contains(m.View(), "existing-local-model") {
		t.Fatal("compact setup hides selected model")
	}
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m.Setup.Check.SetValue("go test ./...")
	if !strings.Contains(m.View(), "go test ./...") {
		t.Fatal("compact setup hides check editor")
	}
	if !strings.Contains(m.View(), "Enter review") {
		t.Fatal("compact check hides action guidance")
	}
	m = viewportKey(m, tea.KeyEnter)
	if !strings.Contains(m.View(), "Enter save · PgUp/PgDn") {
		t.Fatal("compact review hides paging/save guidance")
	}
	m.Setup.Err = "Cannot save configuration"
	if !strings.Contains(m.View(), m.Setup.Err) {
		t.Fatal("compact setup hides save error")
	}
}

func setupFixture(t *testing.T, w, h int) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	m := viewportUpdate(New(testCfg(), "fixture"), tea.WindowSizeMsg{Width: w, Height: h})
	m.CfgPath = filepath.Join(t.TempDir(), "caliper-harness.yaml")
	m.Serve.Rows = []ServeRow{{IsModel: true, Have: true, Ep: serve.Endpoint{URL: "http://127.0.0.1:8080/v1", Model: "existing-local-model"}, Health: serve.Health{Up: true, APIBase: "http://127.0.0.1:8080/v1", Runtime: "openai"}}}
	m.OpenSetup()
	return m
}
func TestSetupStagesWithoutEffectsThenSavesExactConfig(t *testing.T) {
	m := setupFixture(t, 80, 24)
	original := m.Cfg
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m.Setup.Check.SetValue("printf NEVER_EXECUTE_DURING_SETUP > setup-effect.txt")
	m = viewportKey(m, tea.KeyEnter)
	if !reflect.DeepEqual(m.Cfg, original) {
		t.Fatal("selection/review changed live config")
	}
	if _, err := os.Stat(m.CfgPath); !os.IsNotExist(err) {
		t.Fatal("review wrote config")
	}
	m = viewportKey(m, tea.KeyEnter)
	cfg, used, err := config.Load(m.CfgPath)
	if err != nil || !used || cfg.Model.Name != "existing-local-model" || cfg.Coding.CheckCommand != "printf NEVER_EXECUTE_DURING_SETUP > setup-effect.txt" || m.setupActive() {
		t.Fatalf("saved result missing: %v", err)
	}
	if _, err := os.Stat("setup-effect.txt"); !os.IsNotExist(err) {
		t.Fatal("setup executed the project check")
	}
}
func TestSetupRefusalPreservesLiveStateAndExternalConfig(t *testing.T) {
	for _, kind := range []string{"external", "missing-parent", "busy", "disappeared"} {
		t.Run(kind, func(t *testing.T) {
			m := setupFixture(t, 80, 24)
			m = viewportKey(m, tea.KeyEnter)
			m = viewportKey(m, tea.KeyEnter)
			m.Setup.Check.SetValue("go test ./...")
			m = viewportKey(m, tea.KeyEnter)
			before := m.Cfg
			var external []byte
			switch kind {
			case "external":
				external = []byte("model:\n  name: externally-changed\n")
				if err := os.WriteFile(m.CfgPath, external, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-parent":
				m.Setup.Path = filepath.Join(t.TempDir(), "absent", "config.yaml")
			case "busy":
				m.Chat.Running = true
			case "disappeared":
				m.Serve.Rows = nil
			}
			m.setupSave()
			if m.Setup.Err == "" || !reflect.DeepEqual(before, m.Cfg) || !m.setupActive() {
				t.Fatal("refused save changed live config or lost form")
			}
			if external != nil {
				got, _ := os.ReadFile(m.CfgPath)
				if string(got) != string(external) {
					t.Fatal("external config overwritten")
				}
			}
		})
	}
}
func TestSetupStableSelectionDraftAndLayeredEscape(t *testing.T) {
	m := setupFixture(t, 80, 24)
	m.Input.SetValue("ordinary draft")
	m.Serve.Rows = append(m.Serve.Rows, m.Serve.Rows[0])
	m.Serve.Rows[1].Ep.Model = "second-model"
	m.Input.SetValue("")
	m = viewportKey(m, tea.KeyDown)
	pick := m.Setup.Pick
	m.Serve.Rows[0], m.Serve.Rows[1] = m.Serve.Rows[1], m.Serve.Rows[0]
	if m.setupChoices()[m.setupIndex(m.setupChoices())].key != pick {
		t.Fatal("probe reorder substituted selection")
	}
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m.Setup.Check.SetValue("retained exact check")
	m = viewportKey(m, tea.KeyTab)
	m = submit(m, "/chat")
	m = submit(m, "/setup")
	if m.Setup.Check.Value() != "retained exact check" || m.Setup.Stage != 2 {
		t.Fatal("setup tab lost field")
	}
	m.Setup.CheckFocused = true
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEsc)
	if m.Setup.Stage != 2 {
		t.Fatal("review Escape skipped check")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Setup.Stage != 1 {
		t.Fatal("check Escape skipped judge")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Setup.Stage != 0 {
		t.Fatal("check Escape skipped model")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.setupActive() {
		t.Fatal("model Escape failed to return chat")
	}
	if _, err := os.Stat(m.CfgPath); !os.IsNotExist(err) {
		t.Fatal("cancel wrote configuration")
	}
}
func TestSetupViewsFitAndDiscloseLongReview(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 12}} {
		m := setupFixture(t, size[0], size[1])
		for stage := 0; stage < 4; stage++ {
			m.Setup.Stage = stage
			m.Setup.Model = m.Cfg.Model
			m.Setup.Model.Name = strings.Repeat("long-模型-", 25)
			m.Setup.Check.SetValue("go test ./...")
			viewportFrame(t, m)
		}
	}
}
