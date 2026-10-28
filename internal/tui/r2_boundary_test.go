package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/serve"
	"specific-harness/internal/watch"
)

func TestR2SetupCanonicalIdentityAndCurrentSelection(t *testing.T) {
	m := New(testCfg(), "test")
	m.CfgPath = filepath.Join(t.TempDir(), "config.yaml")
	m.Cfg.Model = config.ModelConfig{Name: "current", Runtime: "openai", Server: "http://localhost:9876/v1"}
	m.Serve.Rows = []ServeRow{{IsModel: true, Ep: serve.Endpoint{Model: "first-other", URL: m.Cfg.Model.Server}}, {IsModel: true, Ep: serve.Endpoint{Model: "current", URL: m.Cfg.Model.Server}}}
	m.OpenSetup()
	if i := m.setupIndex(m.setupChoices()); i != 1 {
		t.Fatalf("current role not selected: %d", i)
	}
	m.Setup.Added = []setupChoice{{model: m.Cfg.Model}, {model: m.Cfg.Model}}
	for i := 0; i < 3; i++ {
		choices := m.setupChoices()
		if len(choices) != 2 || choices[m.setupIndex(choices)].model.Name != "current" {
			t.Fatalf("duplicate or changed selection: %+v", choices)
		}
	}
	other := m.Cfg.Model
	other.Runtime = "ollama"
	m.Setup.Added = append(m.Setup.Added, setupChoice{model: other})
	if len(m.setupChoices()) != 3 {
		t.Fatal("distinct runtime collapsed")
	}
	m.Setup.Pick = setupModelKey(config.ModelConfig{Name: "missing", Server: m.Cfg.Model.Server, Runtime: "openai"})
	if m.setupIndex(m.setupChoices()) != -1 {
		t.Fatal("missing saved choice silently replaced")
	}
}

func TestR2PlansRefreshKeepsLogicalReadingPosition(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width, m.Height = 90, 25
	m.navigate(ViewPlans, "")
	p := LoadedPlan{P: &plan.Plan{ID: "same", Title: "Project", Goal: strings.Repeat("goal detail\n", 70), State: plan.PlanActive}}
	m.Plans = PlansState{Plans: []LoadedPlan{p}, Loaded: true}
	m.Plans.rebuild("same")
	m.plansDetail(m.plansCur())
	layout := layoutLines(detailLogical(m.Plans.Detail), BoxInner(m.Width))
	m.Plans.Scroll = layout.gotoLine(30, 5)
	line := layout.topLine(m.Plans.Scroll)
	m.onPlansLoaded(plansLoadedMsg{plans: []LoadedPlan{p}, request: m.Plans.Request, owner: m.Chat.ID, path: m.StorePath})
	after := layoutLines(detailLogical(m.Plans.Detail), BoxInner(m.Width))
	if after.topLine(m.Plans.Scroll) != line {
		t.Fatalf("refresh moved reading anchor: %d -> %d", line, after.topLine(m.Plans.Scroll))
	}
	p.P.Goal = "short"
	m.onPlansLoaded(plansLoadedMsg{plans: []LoadedPlan{p}, request: m.Plans.Request, owner: m.Chat.ID, path: m.StorePath})
	after = layoutLines(detailLogical(m.Plans.Detail), BoxInner(m.Width))
	if m.Plans.Scroll > after.clamp(m.Plans.Scroll, detailAvail(m.plansHeight(viewHeight(m), m.Width))) {
		t.Fatal("shortened content retained invalid scroll")
	}
}

func TestR2ConversationDraftsAndFailedSettingsEdit(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("draft A")
	m.newTab()
	if m.Input.Value() != "" {
		t.Fatal("new conversation inherited draft")
	}
	m.Input.SetValue("draft B")
	m.switchTab(0)
	if m.Input.Value() != "draft A" {
		t.Fatal("A draft lost")
	}
	m.navigate(ViewSettings, "")
	field := SettingField{Section: "params", Key: "temperature", Label: "Temperature"}
	m.armEdit(field)
	m.Input.SetValue("NaN")
	m.applyEdit("NaN")
	if m.Settings.Editing == nil || m.Input.Value() != "NaN" {
		t.Fatal("invalid edit discarded")
	}
	m.Input.SetValue("0.4")
	m.applyEdit("0.4")
	if m.Settings.Editing != nil || m.Input.Value() != "draft A" || m.Cfg.Params.Temperature != 0.4 {
		t.Fatal("valid apply did not restore owning draft")
	}
	m.focusChat()
	m.switchTab(1)
	if m.Input.Value() != "draft B" {
		t.Fatal("B draft lost")
	}
}

func TestR2CRLFStyledDisplayPreservesSource(t *testing.T) {
	raw := "# Cargo examples\r\nconst n = 1\r\nplain\r"
	if strings.Contains(sourceText(raw), "\r") || strings.Contains(diffSource("+ ", "# Cargo examples\r", 80, true), "\r") {
		t.Fatal("CR survives ANSI styling")
	}
	if ansi.Strip(sourceText(raw)) != "# Cargo examples\nconst n = 1\nplain" {
		t.Fatal("display lost text")
	}
	quote, err := Quote(strings.Split(raw, "\n"), "[1]")
	if err != nil || !strings.Contains(quote, "# Cargo examples") {
		t.Fatalf("source quote lost: %q %v", quote, err)
	}
	if !strings.Contains(raw, "\r\n") {
		t.Fatal("source changed")
	}
}

func TestR2CommentShortcutKeepsCapturedOwner(t *testing.T) {
	m := historyFixture(120, 40)
	ev := watch.Event{Seq: 77, Path: "old.md", At: time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC), Op: watch.OpModify, After: []string{"captured content"}, Truncated: true}
	m.navigate(ViewDiff, "")
	m.Diff.Viewer = &DiffViewer{Ev: ev}
	m.commentPrefill("old.md")
	if !m.diffActive() {
		t.Fatal("shortcut lost viewer")
	}
	m.commentChat([]string{"old.md", "[1]", "review this"})
	if len(m.Comment) != 1 {
		t.Fatal("comment rejected")
	}
	prompt := buildPrompt("continue", m.Comment)
	for _, want := range []string{"captured content", "#77", "2026-10-25T01:02:03Z", "partial"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q: %s", want, prompt)
		}
	}
	if m.Alerts[len(m.Alerts)-1].Kind != "info" {
		t.Fatal("successful queue kept failure banner")
	}
}

func TestR2FileBoundsAreMetadataNotSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte("one\ntwo\nthree\nfour"), 0600); err != nil {
		t.Fatal(err)
	}
	lines, notice, err := readFileBounded(dir, "large.txt", 1024, 2)
	if err != nil || len(lines) != 2 || notice == "" {
		t.Fatalf("bounds missing: %#v %s %v", lines, notice, err)
	}
	if _, err := Quote(lines, "[3]"); err == nil {
		t.Fatal("metadata is quotable as source")
	}
	m := New(testCfg(), "test")
	m.Files.Root = dir
	m.Files.Path = "large.txt"
	m.Files.Ticket = 1
	m.onFilesOpened(filesOpenedMsg{root: dir, title: "large.txt", ticket: 1, lines: lines, notice: notice})
	if m.captureNotice() != notice {
		t.Fatal("bound omitted from visible header")
	}
}

func TestR2ModelEvidenceLabels(t *testing.T) {
	m := New(testCfg(), "test")
	m.Serve.Sel = -1
	r := ServeRow{IsModel: true, Ep: serve.Endpoint{URL: "http://localhost:1234", Model: "fixture"}, Health: serve.Health{Up: true, Kind: serve.KindOllama, Available: []string{"other"}}}
	m.Cfg.Serve.Models = []config.SavedModel{{Server: r.Ep.URL, Name: r.Ep.Model}}
	text := ansi.Strip(m.serveModelItem(r).Label + " " + m.serveModelItem(r).Value)
	if strings.Contains(text, "l load") || !strings.Contains(text, "unknown") {
		t.Fatal(text)
	}
	r.Health.Available = []string{"fixture"}
	m.Cfg.Serve.Models = nil
	text = ansi.Strip(m.serveModelItem(r).Label + " " + m.serveModelItem(r).Value)
	if strings.Contains(text, "saved") || !strings.Contains(text, "catalog") {
		t.Fatal(text)
	}
}

func TestR2ToolActivityAndClearScope(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Streaming = true
	m.Chat.Seq = 4
	m.onEvent(eventMsg{seq: 4, kind: "tool", text: "bash long check"})
	if m.Chat.StreamActivity != "executing tool" {
		t.Fatal("tool not active")
	}
	m.onEvent(eventMsg{seq: 4, kind: "result", text: "done"})
	if m.Chat.StreamActivity != "" {
		t.Fatal("stale tool activity")
	}
	m.clearChat()
	if !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "context retained") {
		t.Fatal("clear scope ambiguous")
	}
	m.Chat.Dialog = &Dialog{Kind: "ask"}
	_ = tea.KeyEnter
	if !strings.Contains(m.escapeHint(), "discards") {
		t.Fatal("question batch scope hidden")
	}
}
