package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/decision"
	"specific-harness/internal/serve"
	"specific-harness/internal/tools/ask"
)

func viewportUpdate(m Model, msg tea.Msg) Model  { next, _ := m.Update(msg); return next.(Model) }
func viewportKey(m Model, typ tea.KeyType) Model { return viewportUpdate(m, tea.KeyMsg{Type: typ}) }
func viewportFrame(t *testing.T, m Model) string {
	t.Helper()
	v := m.View()
	if lipgloss.Height(v) > m.Height {
		t.Errorf("frame height %d exceeds %d", lipgloss.Height(v), m.Height)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > m.Width {
			t.Errorf("frame line width %d exceeds %d: %q", lipgloss.Width(l), m.Width, ansi.Strip(l))
		}
	}
	return ansi.Strip(v)
}

func TestViewportHelpNavigation(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = submit(m, "/help")
			m.Input.SetValue("untouched")
			all := ""
			for i := 0; i < 20; i++ {
				v := viewportFrame(t, m)
				all += v
				if !strings.Contains(v, "Commands & keys") || !strings.Contains(v, "esc") {
					t.Error("missing pinned title/dismiss hint")
				}
				m = viewportKey(m, tea.KeyPgDown)
				if !m.HelpOpen {
					t.Fatal("page key dismissed Help")
				}
			}
			for _, s := range []string{"VIEWS", "KEYS", "shift+←→ drafts", "tab comment", "pgup/pgdn scroll", "enter dive", "/auth"} {
				if !strings.Contains(strings.Join(strings.Fields(all), " "), s) {
					t.Errorf("navigation omitted %q", s)
				}
			}
			m = viewportKey(m, tea.KeyHome)
			first := m.View()
			m = viewportKey(m, tea.KeyEnd)
			if first == m.View() {
				t.Error("Home/End did not navigate")
			}
			m = viewportUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
			viewportFrame(t, m)
			m = key(m, "x")
			if m.HelpOpen || m.Input.Value() != "untouched" {
				t.Fatal("dismiss key leaked to input")
			}
		})
	}
}

func TestViewportHelpOwnsAsk(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/help")
	m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "Which?", Options: []string{"A", "B"}}}}
	m = viewportKey(m, tea.KeyDown)
	if !m.HelpOpen || m.Chat.Dialog.PickIdx != 0 {
		t.Fatal("Help navigation reached Ask")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.HelpOpen || m.Chat.Dialog == nil {
		t.Fatal("Help escape reached Ask")
	}
}

func viewportServeFixture(w, h int) Model {
	m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: w, Height: h})
	m = submit(m, "/status")
	for i := 0; i < 32; i++ {
		m.Serve.Rows = append(m.Serve.Rows, ServeRow{Ep: serve.Endpoint{Label: "local", URL: "http://fixture.invalid", Model: fmt.Sprintf("ROW_%02d", i)}, IsModel: true, Have: true})
	}
	long := strings.Repeat("界α", 70)
	identity := &decision.Identity{Model: "SIDECAR_FULL_" + long + "_SIDECAR_TAIL", Path: "/SELECTED_PATH_" + long + "_PATH_TAIL", Architecture: "architecture", Runtime: "d1", NativeDevice: "mps", Source: "local"}
	err := "ACTIVATION_END_" + long + "_ACTIVATION_TAIL"
	m.Serve.Rows = append(m.Serve.Rows, ServeRow{Ep: serve.Endpoint{Label: "sidecar", URL: "http://fixture.invalid/decision", Model: identity.Model}, Sidecar: true, Have: true, Health: serve.Health{Decision: &decision.Health{Active: identity, Error: &err}}})
	m.Serve.Warnings = []string{"INVENTORY_FULL_" + long + "_INVENTORY_TAIL"}
	m.Serve.MemErr = "MEMORY_FULL_" + long + "_MEMORY_TAIL"
	m.Tabs[m.Active].Title = "ACTIVE_TAB_" + long + "_TAB_TAIL"
	m.Alerts = []Alert{{Kind: "err", Text: "ALERT_FULL_" + long + "_ALERT_TAIL"}}
	m.St.Model = "FOOTER_MODEL_" + long + "_FOOTER_TAIL"
	return m
}

func TestViewportStatusReachability(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := viewportServeFixture(size[0], size[1])
			all := ""
			for i := range m.Serve.Rows {
				v := viewportFrame(t, m)
				all += v
				if !strings.Contains(v, "selected") || !strings.Contains(v, fmt.Sprintf("%d/%d", i+1, len(m.Serve.Rows))) {
					t.Errorf("selection %d not visibly identified", i)
				}
				if i < 32 && !strings.Contains(v, fmt.Sprintf("ROW_%02d", i)) {
					t.Errorf("row %d not visible", i)
				}
				if !strings.Contains(v, "refresh") || !strings.Contains(v, "Enter /command") {
					t.Error("hints/input clipped")
				}
				if i < len(m.Serve.Rows)-1 {
					m = viewportKey(m, tea.KeyDown)
				}
			}
			recovered := ""
			for i := 0; i < 40; i++ {
				v := viewportFrame(t, m)
				all += v
				collecting := false
				for _, line := range strings.Split(v, "\n") {
					if strings.HasPrefix(line, "│ selected") {
						collecting = true
						continue
					}
					if collecting && strings.HasPrefix(line, "│ ↑↓ select") {
						break
					}
					if collecting {
						recovered += strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │"))
					}
				}
				m = viewportKey(m, tea.KeyPgDown)
				if m.Serve.Sel != 32 {
					t.Fatal("paging changed selected row")
				}
			}
			compact := strings.ReplaceAll(all, "\n", "")
			for _, s := range []string{"SIDECAR_FULL_", "SELECTED_PATH_", "ACTIVATION_END_", "INVENTORY_FULL_", "MEMORY_FULL_", "FOOTER_MODEL_", "ACTIVE_TAB_", "ALERT_FULL_"} {
				if !strings.Contains(compact, s) {
					t.Errorf("unreachable full detail %q", s)
				}
			}
			long := strings.Repeat("界α", 70)
			for _, value := range []string{"SIDECAR_FULL_" + long + "_SIDECAR_TAIL", "/SELECTED_PATH_" + long + "_PATH_TAIL", "ACTIVATION_END_" + long + "_ACTIVATION_TAIL", "INVENTORY_FULL_" + long + "_INVENTORY_TAIL", "MEMORY_FULL_" + long + "_MEMORY_TAIL", "ACTIVE_TAB_" + long + "_TAB_TAIL", "ALERT_FULL_" + long + "_ALERT_TAIL", "FOOTER_MODEL_" + long + "_FOOTER_TAIL"} {
				if !strings.Contains(recovered, value) {
					t.Errorf("full value not reconstructed via pages: %q", value)
				}
			}

			beforeProbe := m.View()
			m = viewportUpdate(m, serveProbedMsg{rows: m.Serve.Rows, mem: m.Serve.Mem, memErr: m.Serve.MemErr, warnings: m.Serve.Warnings})
			if m.View() != beforeProbe {
				t.Fatal("unchanged inventory probe reset paged details")
			}

			m = viewportKey(m, tea.KeyHome)
			if m.Serve.Sel != 0 {
				t.Fatal("Home did not select first")
			}
			m = viewportKey(m, tea.KeyEnd)
			if m.Serve.Sel != 32 {
				t.Fatal("End did not select last")
			}
			kept := serveKeyOf(m.Serve.Rows[32])
			rows := append([]ServeRow{m.Serve.Rows[32]}, m.Serve.Rows[:32]...)
			m = viewportUpdate(m, serveProbedMsg{rows: rows})
			if serveKeyOf(m.Serve.Rows[m.Serve.Sel]) != kept {
				t.Fatal("probe lost selected identity")
			}
			viewportFrame(t, m)
			m = viewportUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
			viewportFrame(t, m)
		})
	}
}

// Aggregate actual navigation instead of requiring every detail on one screen.
func viewportServePages(m Model, w, vh int) string {
	outer := m.Height - viewHeight(m)
	m = viewportUpdate(m, tea.WindowSizeMsg{Width: w, Height: vh + outer})
	m = viewportKey(m, tea.KeyHome)
	for range 20 {
		m = viewportKey(m, tea.KeyPgUp)
	}
	var all strings.Builder
	for range max(1, len(m.Serve.Rows)) {
		for range 20 {
			all.WriteString(m.renderServe(w, vh) + "\n")
			m = viewportKey(m, tea.KeyPgDown)
		}
		m = viewportKey(m, tea.KeyDown)
	}
	return strings.Join(strings.Fields(all.String()), " ")
}

func TestViewportStatusActionAdmission(t *testing.T) {
	m := viewportServeFixture(80, 24)
	m.CfgPath = filepath.Join(t.TempDir(), "config.yaml")
	m = viewportKey(m, tea.KeyEnd)
	m = viewportKey(m, tea.KeyUp)
	m.Serve.Rows[31].Health.Up = true
	rows := append([]ServeRow{m.Serve.Rows[31]}, m.Serve.Rows[:31]...)
	m = viewportUpdate(m, serveProbedMsg{rows: rows})
	// u uses current selected identity, not the old numeric position; no command is run.
	m = key(m, "u")
	if m.Cfg.Model.Name != rows[0].Ep.Model {
		t.Fatal("use used stale row")
	}
	m = viewportUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 8})
	before := m.Cfg.Model
	m = key(m, "u")
	if m.Cfg.Model != before || !strings.Contains(lastAlert(m).Text, "enlarge") {
		t.Fatal("invisible selection admitted action")
	}
}

func TestViewportStatusLoadingDetails(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := viewportServeFixture(size[0], size[1])
			m.Serve.Rows = nil
			originalCfg := m.Cfg
			seen := map[int]string{}
			total := 0
			for range 30 {
				v := viewportFrame(t, m)
				if !strings.Contains(v, "selected 0/0") {
					t.Fatal("loading claims a selection")
				}
				start, end, currentTotal := 0, 0, 0
				collecting := false
				index := 0
				for _, line := range strings.Split(v, "\n") {
					if strings.HasPrefix(line, "┌─ Status · ") {
						fmt.Sscanf(strings.TrimPrefix(line, "┌─ Status · "), "%d-%d/%d", &start, &end, &currentTotal)
					}
					if strings.HasPrefix(line, "│ selected") {
						collecting = true
						index = start
						continue
					}
					if collecting && strings.HasPrefix(line, "│ ↑↓ select") {
						break
					}
					if collecting && index <= end {
						content := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │"))
						if old, ok := seen[index]; ok && old != content {
							t.Fatalf("overlapping visible row %d changed", index)
						}
						seen[index] = content
						index++
					}
				}
				if start < 1 || end < start || currentTotal < end || index != end+1 {
					t.Fatal("rendered page range did not match visible body rows")
				}
				if total != 0 && total != currentTotal {
					t.Fatal("fixture content changed during paging")
				}
				total = currentTotal
				m = viewportKey(m, tea.KeyPgDown)
			}
			recovered := ""
			for i := 1; i <= total; i++ {
				row, ok := seen[i]
				if !ok {
					t.Fatalf("row %d was never visible", i)
				}
				recovered += row
			}
			for _, value := range []string{m.St.Model, m.Tabs[m.Active].Title, m.Alerts[0].Text} {
				if !strings.Contains(recovered, value) {
					t.Errorf("loading hid full identity/detail %q", value)
				}
			}
			for _, msg := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("x")}, {Type: tea.KeyRunes, Runes: []rune("u")}} {
				next, cmd := m.Update(msg)
				m = next.(Model)
				if cmd != nil || !reflect.DeepEqual(m.Cfg, originalCfg) {
					t.Fatal("loading acquired action without a row")
				}
			}
		})
	}
}
