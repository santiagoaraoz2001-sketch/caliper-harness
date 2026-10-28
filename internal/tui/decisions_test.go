package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"specific-harness/internal/decision"
	"specific-harness/internal/session"
)

// decisionsModel builds a model whose chat ran twice: an older run
// with one verdict plus a malformed line, and a newer run with two.
func decisionsModel(t *testing.T) Model {
	t.Helper()
	m := New(testCfg(), "test")
	dir := t.TempDir()
	m.SessionsPath = dir
	m.StorePath = t.TempDir()
	mk := func(id, source, pick string, conf float64, res string) {
		text := fmt.Sprintf("source=%s | pick=%s | conf=%.3f | resolution=%s | options=a; b | context=c",
			source, pick, conf, res)
		if err := session.Append(dir, id, session.Event{Kind: decision.KindJudgment, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	mk("20260101-000000-aa", decision.SourceRiskyAuth, "allow", 0.528, "asked-allowed")
	if err := session.Append(dir, "20260101-000000-aa",
		session.Event{Kind: decision.KindJudgment, Text: "garbage line"}); err != nil {
		t.Fatal(err)
	}
	mk("20260202-000000-bb", decision.SourceJudgeTool, "approve refund", 0.757, "returned")
	mk("20260202-000000-bb", decision.SourceCoherence, "coherent", 0.92, "returned")
	m.noteSession("20260101-000000-aa")
	m.noteSession("20260202-000000-bb")
	m.SyncTabChat()
	return m
}

func loadDecisions(m Model) Model {
	um, _ := m.Update(m.refreshDecisions()())
	return um.(Model)
}

func TestDecisionsFeedNewestFirst(t *testing.T) {
	m := loadDecisions(decisionsModel(t))
	if len(m.Decisions.Rows) != 3 {
		t.Fatalf("rows=%+v skipped=%d", m.Decisions.Rows, m.Decisions.Skipped)
	}
	if m.Decisions.Rows[0].Session != "20260202-000000-bb" ||
		m.Decisions.Rows[2].Session != "20260101-000000-aa" {
		t.Errorf("newest run first: %+v", m.Decisions.Rows)
	}
	r := m.Decisions.Rows[0]
	if r.Source != decision.SourceCoherence || r.Pick != "coherent" || r.Resolution != "returned" {
		t.Errorf("row=%+v", r)
	}
}

func TestDecisionsSkipsMalformed(t *testing.T) {
	m := decisionsModel(t)
	m.noteSession("missing-id")
	m = loadDecisions(m)
	if len(m.Decisions.Rows) != 3 {
		t.Fatalf("rows=%+v", m.Decisions.Rows)
	}
	// one malformed line + one unreadable session.
	if m.Decisions.Skipped != 2 {
		t.Errorf("skipped=%d", m.Decisions.Skipped)
	}
}

func TestDecisionsBucketsAndSources(t *testing.T) {
	m := loadDecisions(decisionsModel(t))
	if m.Decisions.BySource[decision.SourceJudgeTool] != 1 ||
		m.Decisions.BySource[decision.SourceRiskyAuth] != 1 ||
		m.Decisions.BySource[decision.SourceCoherence] != 1 {
		t.Errorf("bySource=%v", m.Decisions.BySource)
	}
	// 0.528 → bucket 1, 0.757 → 2, 0.92 → 3.
	if m.Decisions.Buckets != [4]int{0, 1, 1, 1} {
		t.Errorf("buckets=%v", m.Decisions.Buckets)
	}
}

func TestDecisionsEmpty(t *testing.T) {
	m := New(testCfg(), "test")
	m.SessionsPath = t.TempDir()
	m = loadDecisions(m)
	if len(m.Decisions.Rows) != 0 {
		t.Fatalf("rows=%+v", m.Decisions.Rows)
	}
	out := m.renderDecisions(80, 20)
	if !strings.Contains(out, "no judgments yet") {
		t.Errorf("empty state must say so:\n%s", out)
	}
}

func TestDecisionsRenderRows(t *testing.T) {
	m := loadDecisions(decisionsModel(t))
	out := m.renderDecisions(100, 30)
	for _, want := range []string{"risky-auth", "approve refund", "0.76", "asked-allowed", "auto-refreshes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestNoteSessionCap(t *testing.T) {
	m := New(testCfg(), "test")
	for i := 0; i < 20+5; i++ {
		m.noteSession(fmt.Sprintf("id-%d", i))
	}
	if len(m.Chat.SessionIDs) != 20 {
		t.Fatalf("ids=%d", len(m.Chat.SessionIDs))
	}
	if m.Chat.SessionIDs[0] != "id-5" {
		t.Errorf("oldest must drop first: %v", m.Chat.SessionIDs[:2])
	}
}

func TestStartRunTracksSession(t *testing.T) {
	m := New(testCfg(), "test")
	m.StorePath = filepath.Join(t.TempDir(), "store")
	m.SessionsPath = t.TempDir()
	m.Server = "http://127.0.0.1:1"
	m.ModelName = "test-model"
	// The fixture starts a real worker. Receive its settlement after store.Close
	// before TempDir cleanup; otherwise that cleanup races the worker's writes.
	settled := make(chan struct{}, 1)
	prog := tea.NewProgram(fidelityProgram{m}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(runDoneMsg); ok {
			settled <- struct{}{}
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			return msg
		}
		return nil
	}))
	m.Shared.Prog = prog
	finished := make(chan error, 1)
	go func() { _, err := prog.Run(); finished <- err }()
	t.Cleanup(func() {
		prog.Quit()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	})
	m.startRun("hi")
	if len(m.Chat.SessionIDs) != 1 {
		t.Fatalf("startRun must record its session: %+v", m.Chat.SessionIDs)
	}
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		m.stopChat()
		t.Fatal("session fixture worker did not settle")
	}
}

func TestDecisionsEscBack(t *testing.T) {
	m := decisionsModel(t)
	m = submit(m, "/decisions")
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Fatal("esc must walk decisions back to chat")
	}
}

func TestDecisionsViewOpens(t *testing.T) {
	m := decisionsModel(t)
	m = submit(m, "/decisions")
	if !m.decisionsActive() {
		t.Fatal("not on the decisions view")
	}
	m = loadDecisions(m)
	if len(m.Decisions.Rows) != 3 {
		t.Fatalf("rows=%+v", m.Decisions.Rows)
	}
}

func decisionsViewportModel(t *testing.T, w, h, count int) (Model, string, []byte) {
	t.Helper()
	m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: w, Height: h})
	m.SessionsPath, m.StorePath = t.TempDir(), t.TempDir()
	m.noteSession("viewport")
	for i := range count {
		text := fmt.Sprintf("source=coherence | pick=VERDICT_%03d | conf=0.920 | resolution=returned | options=a; b | context=c", i)
		if err := session.Append(m.SessionsPath, "viewport", session.Event{Kind: decision.KindJudgment, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	// A real nonjudgment event makes even the empty feed readable.
	if err := session.Append(m.SessionsPath, "viewport", session.Event{Kind: "result", Text: "retained raw evidence"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.SessionsPath, "viewport.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := m.submit("/decisions")
	if cmd == nil {
		t.Fatal("public command did not refresh the feed")
	}
	m = viewportUpdate(m, cmd())
	m.Input.SetValue("retained draft κ")
	return m, path, raw
}

func decisionsViewportKey(t *testing.T, m Model, typ tea.KeyType) Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: typ})
	if cmd != nil {
		t.Fatal("view navigation launched an operation")
	}
	m = next.(Model)
	if m.Input.Value() != "retained draft κ" || m.Chat.Running {
		t.Fatal("view navigation altered the draft or started a model run")
	}
	return m
}

func TestDecisionsViewportRetainedFeed(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, count := range []int{0, 3, 32, 100} {
			t.Run(fmt.Sprintf("%dx%d/%d", size[0], size[1], count), func(t *testing.T) {
				m, path, raw := decisionsViewportModel(t, size[0], size[1], count)
				all := ""
				for range count + 1 {
					v := viewportFrame(t, m)
					all += v
					if !strings.Contains(v, "auto-refreshes") || !strings.Contains(v, "esc chat") || !strings.Contains(v, "retained draft") {
						t.Fatal("view hints or draft are clipped")
					}
					m = decisionsViewportKey(t, m, tea.KeyPgDown)
					if ansi.Strip(m.View()) == v {
						break
					}
				}
				for i := range count {
					if !strings.Contains(all, fmt.Sprintf("VERDICT_%03d", i)) {
						t.Errorf("retained verdict %d cannot be reached by paging", i)
					}
				}
				current, err := os.ReadFile(path)
				if err != nil || string(current) != string(raw) || len(m.Decisions.Rows) != count {
					t.Fatal("viewing changed raw events or retained rows")
				}
			})
		}
	}
}

func TestDecisionsViewportRefreshResizeAndEscape(t *testing.T) {
	m, _, _ := decisionsViewportModel(t, 80, 24, 100)
	m = decisionsViewportKey(t, m, tea.KeyEnd)
	last := viewportFrame(t, m)
	if !strings.Contains(last, "VERDICT_000") || strings.Contains(last, "VERDICT_099") {
		t.Fatal("End does not reveal the final verdict")
	}
	m = decisionsViewportKey(t, m, tea.KeyPgUp)
	if viewportFrame(t, m) == last {
		t.Fatal("first PgUp after End did not move")
	}
	m = decisionsViewportKey(t, m, tea.KeyEnd)
	for range 10 {
		m = decisionsViewportKey(t, m, tea.KeyPgDown)
	}
	if viewportFrame(t, m) != last {
		t.Fatal("paging beyond the bottom changed the last page")
	}
	m = decisionsViewportKey(t, m, tea.KeyPgUp)
	if viewportFrame(t, m) == last {
		t.Fatal("first PgUp after repeated bottom paging did not move")
	}
	m = decisionsViewportKey(t, m, tea.KeyEnd)
	m = viewportUpdate(m, m.refreshDecisions()())
	if viewportFrame(t, m) != last {
		t.Fatal("unchanged refresh reset the visible page")
	}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {120, 140}, {80, 24}} {
		m = viewportUpdate(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		viewportFrame(t, m)
		m = decisionsViewportKey(t, m, tea.KeyEnd)
		if !strings.Contains(m.View(), "VERDICT_000") {
			t.Fatal("resize hid the final verdict")
		}
	}
	m.Cfg.UI.MaxDecisionRows = 3
	m = viewportUpdate(m, m.refreshDecisions()())
	if len(m.Decisions.Rows) != 3 || !strings.Contains(viewportFrame(t, m), "VERDICT_099") {
		t.Fatal("smaller feed did not clamp the visible page")
	}
	m.Cfg.UI.MaxDecisionRows = 100
	m = viewportUpdate(m, m.refreshDecisions()())
	if !strings.Contains(viewportFrame(t, m), "VERDICT_099") {
		t.Fatal("smaller-feed refresh left a stale offset")
	}
	m = decisionsViewportKey(t, m, tea.KeyHome)
	if !strings.Contains(viewportFrame(t, m), "VERDICT_099") {
		t.Fatal("Home does not restore the first verdict")
	}
	m.alert("warn", "owned notice")
	m = decisionsViewportKey(t, m, tea.KeyEsc)
	if !m.decisionsActive() || !m.alertHidden {
		t.Fatal("first Escape did not only dismiss the alert")
	}
	m = decisionsViewportKey(t, m, tea.KeyEsc)
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Fatal("second Escape did not return to chat")
	}
}

func TestDecisionsViewportWrappedValuesAndRetention(t *testing.T) {
	m, path, _ := decisionsViewportModel(t, 80, 24, 0)
	longPick := "LONG_VERDICT_" + strings.Repeat("界α", 50) + "_TAIL"
	for i := range 32 {
		pick, source := fmt.Sprintf("VERDICT_%03d", i), decision.SourceCoherence
		if i == 30 {
			pick, source = longPick, decision.SourceJudgeTool
		}
		text := fmt.Sprintf("source=%s | pick=%s | conf=0.920 | resolution=returned | options=a; b | context=c", source, pick)
		if err := session.Append(m.SessionsPath, "viewport", session.Event{Kind: decision.KindJudgment, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Append(m.SessionsPath, "foreign", session.Event{Kind: decision.KindJudgment, Text: "source=coherence | pick=FOREIGN_CHAT | conf=0.920 | resolution=returned"}); err != nil {
		t.Fatal(err)
	}
	if err := session.Append(m.SessionsPath, "viewport", session.Event{Kind: decision.KindJudgment, Text: "malformed verdict"}); err != nil {
		t.Fatal(err)
	}
	m.Cfg.UI.MaxDecisionRows = 17
	m = viewportUpdate(m, m.refreshDecisions()())
	if len(m.Decisions.Rows) != 17 || m.Decisions.Skipped != 1 || m.Decisions.Buckets != [4]int{0, 0, 0, 17} {
		t.Fatal("viewport changed configured retention or feed accounting")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first := viewportFrame(t, m)
	for range 5 {
		m = viewportUpdate(m, m.refreshDecisions()())
		if viewportFrame(t, m) != first {
			t.Fatal("unchanged multi-source refresh changed the visible page")
		}
	}
	visible, reconstructed := "", ""
	for range 20 {
		v := viewportFrame(t, m)
		visible += v
		for _, row := range strings.Split(v, "\n") {
			if strings.HasPrefix(row, "│ ") && !strings.Contains(row, "pgup/pgdn") && !strings.Contains(row, "retained draft") {
				reconstructed += strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(row, "│ "), " │"))
			}
		}
		m = decisionsViewportKey(t, m, tea.KeyPgDown)
		if ansi.Strip(m.View()) == v {
			break
		}
	}
	if !strings.Contains(reconstructed, longPick) || !strings.Contains(visible, "unreadable or malformed") || strings.Contains(visible, "FOREIGN_CHAT") || strings.Contains(visible, "VERDICT_014") {
		t.Fatal("wrapped values, malformed disclosure or current-chat/retention scope was lost")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(raw) {
		t.Fatal("paging rewrote raw judgments")
	}
}

func TestDecisionsConfidenceProvenance(t *testing.T) {
	m := decisionsModel(t)
	decision.LogJudgment(func(kind, text string) {
		if err := session.Append(m.SessionsPath, "20260202-000000-bb", session.Event{Kind: kind, Text: text}); err != nil {
			t.Fatal(err)
		}
	}, decision.SourceJudgeTool, decision.Request{Context: "a | b", Options: []string{"allow"}}, decision.Verdict{Option: "allow", Confidence: 0.9, ConfidenceSource: "model-self-report"}, "returned", 0)
	m = loadDecisions(m)
	for _, width := range []int{80, 120} {
		out := ansi.Strip(m.renderDecisions(width, 40))
		for _, want := range []string{"model-self-report", "unspecified", "not comparable"} {
			if !strings.Contains(out, want) {
				t.Errorf("width %d missing %q in %s", width, want, out)
			}
		}
	}
}
