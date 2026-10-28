package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/config"
	"specific-harness/internal/run"
	"specific-harness/internal/session"
)

func testsModel(t *testing.T) Model {
	t.Helper()
	m := settingsModel(t)
	m.Cfg.Testing = config.TestingConfig{Guidance: true, Suites: []config.TestSuite{{ID: "smoke", Name: "Smoke", Purpose: "Detect broken startup", Expected: "Startup reports ready", Command: "echo ready", Enabled: true}}}
	m.Cfg.Coding.CheckCommand = "echo required"
	m.navigate(ViewTests, "")
	return m
}

func TestTestsComposerEditorPreservesDraftAndValidates(t *testing.T) {
	m := testsModel(t)
	m.Input.SetValue("/tools full")
	m = enter(m)
	if m.Chat.Context.ToolView != "full" || m.Tests.Detail {
		t.Fatal("Tests intercepted slash command")
	}
	m.Input.SetValue("ordinary draft ")
	m = key(m, "r")
	if m.Input.Value() != "ordinary draft r" || m.Tests.Busy != "" {
		t.Fatal("Tests consumed ordinary text")
	}
	m.Tests.Sel = 1
	m.editTest(false)
	editor := m.Input.Value()
	m.navigate(ViewStatus, "")
	if m.Input.Value() != "ordinary draft r" {
		t.Fatal("JSON escaped into ordinary composer")
	}
	m.navigate(ViewTests, "")
	if m.Input.Value() != editor {
		t.Fatal("editor lost on navigation")
	}
	m.Input.SetValue("{ invalid")
	m = enter(m)
	if !m.Tests.Editing || m.Input.Value() != "{ invalid" {
		t.Fatal("invalid JSON erased draft or disarmed editor")
	}
	s := m.Cfg.Testing.Suites[0]
	s.ID = "renamed"
	b, _ := json.Marshal(s)
	m.Input.SetValue(string(b))
	m = enter(m)
	if !m.Tests.Editing || m.Cfg.Testing.Suites[0].ID != "smoke" {
		t.Fatal("stable ID changed")
	}
	m = key(m, "esc")
	if m.Tests.Editing || m.Input.Value() != "ordinary draft r" {
		t.Fatal("cancel failed to restore draft")
	}
	m.Input.Reset()
	m.editTest(false)
	s = m.Cfg.Testing.Suites[0]
	s.Expected = "Ready once, no duplicate startup"
	b, _ = json.Marshal(s)
	m.Input.SetValue(string(b))
	m = enter(m)
	if m.Tests.Editing || m.Cfg.Testing.Suites[0].Expected != s.Expected {
		t.Fatalf("valid edit did not commit: %+v", m.Alerts)
	}
}

func TestTestsRequiredOptionalProjectPersistence(t *testing.T) {
	m := testsModel(t)
	original := "model:\n  name: keep-exact\n# untouched comment\n"
	if err := os.WriteFile(m.CfgPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	m.toggleTest()
	if !m.selectedTest().Required || !m.selectedTest().Enabled {
		t.Fatal("required check disabled")
	}
	if raw, _ := os.ReadFile(m.CfgPath); string(raw) != original {
		t.Fatal("required toggle wrote config")
	}
	m.Tests.Sel = 1
	m.toggleTest()
	if m.Cfg.Testing.Suites[0].Enabled || m.Cfg.Testing.Suites[0].DisabledReason == "" {
		t.Fatal("optional toggle lacks reason")
	}
	if m.startTest() != nil || m.Tests.Busy != "" {
		t.Fatal("disabled suite started")
	}
	raw, err := os.ReadFile(m.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "keep-exact") || !strings.Contains(string(raw), "untouched comment") || !strings.Contains(string(raw), "disabled_reason:") {
		t.Fatalf("project-only persistence lost fields: %s", raw)
	}
	m.toggleTest()
	if !m.Cfg.Testing.Suites[0].Enabled || m.Cfg.Testing.Suites[0].DisabledReason != "" {
		t.Fatal("enable failed")
	}
}

func TestTestsDesignDraftUsesGuidanceWithoutSending(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[enabled], func(t *testing.T) {
			m := testsModel(t)
			m.Cfg.Testing.Guidance = enabled
			m.Input.SetValue("user draft")
			m.Tests.Sel = 1
			id := m.Chat.ID
			m.designTest()
			if m.Chat.Running || m.Chat.ID != id || m.Chat.LastPrompt != "" || m.Tabs[m.Active].at.view != ViewChat {
				t.Fatal("draft auto-submitted or owner changed")
			}
			if !strings.Contains(m.Input.Value(), "Smoke") || !strings.Contains(m.Input.Value(), "observable") {
				t.Fatal("design draft lacks suite context")
			}
			if strings.Contains(m.Input.Value(), "caliper/standards/tests") != enabled {
				t.Fatal("guidance toggle ignored")
			}
			if m.Drafts[0] != "user draft" {
				t.Fatal("existing draft lost")
			}
		})
	}
	m := testsModel(t)
	m.Input.SetValue("draft0")
	m.Drafts = [3]string{"draft0", "draft1", "draft2"}
	m.designTest()
	if m.Input.Value() != "draft0" || m.Drafts[1] != "draft1" || m.Drafts[2] != "draft2" {
		t.Fatal("full drafts overwritten")
	}
}

func TestTestsOwnerAdmissionDialogCancelAndStaleCompletion(t *testing.T) {
	m := testsModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Tests.Busy, m.Tests.ctx, m.Tests.cancel = "test-owner", ctx, cancel
	id, seq := m.Chat.ID, m.Chat.Seq
	m.startRun("must not execute")
	m.newChat()
	if m.Chat.Running || m.Chat.ID != id || m.Chat.Seq != seq || m.Chat.LastPrompt != "" {
		t.Fatal("Chat adopted test-owned workspace")
	}
	if m.wfLaunch(WfRow{Approach: "forbidden"}, false) != nil || m.compactChat() != nil {
		t.Fatal("other engine admitted")
	}
	if m.applyField(settingsField("coding", "check_command", "text"), "changed") {
		t.Fatal("configuration changed in flight")
	}
	stale := make(chan string, 1)
	next, _ := m.Update(showDialogMsg{tests: "old-owner", ctx: ctx, kind: "auth", answer: stale, prompt: "stale"})
	m = next.(Model)
	if m.Chat.Dialog != nil || <-stale != "n" {
		t.Fatal("stale test approval accepted")
	}
	answer := make(chan string, 1)
	next, _ = m.Update(showDialogMsg{tests: "test-owner", ctx: ctx, kind: "auth", answer: answer, prompt: "current test"})
	m = next.(Model)
	if m.Chat.Dialog == nil || m.Chat.Dialog.Tests != "test-owner" {
		t.Fatal("current owner dialog rejected")
	}
	if cmd := m.requestQuit(); cmd != nil || ctx.Err() != context.Canceled || !m.Tests.Stopping || m.Tests.Busy == "" {
		t.Fatal("quit failed to retain ownership until settlement")
	}
	if <-answer != "n" {
		t.Fatal("cancel did not decline owned approval")
	}
	if m.onTestDone(testsDoneMsg{owner: "old-owner", ctx: ctx}) != nil || m.Tests.Busy == "" {
		t.Fatal("stale completion cleared owner")
	}
	cmd := m.onTestDone(testsDoneMsg{owner: "test-owner", ctx: ctx, result: run.TestRun{Status: "cancelled"}})
	if cmd == nil || m.Tests.Busy != "" {
		t.Fatal("settled quit did not release owner")
	}
}

func TestTestsNativeCommandReceiptAndFreshness(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	m := testsModel(t)
	m.StorePath = filepath.Join(root, "graph")
	m.Cfg.Store.Path = m.StorePath
	m.SessionsPath = filepath.Join(root, "sessions")
	m.Cfg.Sessions.Path = m.SessionsPath
	m.Cfg.Decision.Disabled = true
	m.Cfg.Auth.Mode = config.AuthYOLO
	m.Tests.Sel = 1
	before := m.Chat
	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = nextModel.(Model)
	if cmd == nil {
		t.Fatalf("test not admitted: %+v", m.Alerts)
	}
	if m.Tests.Busy == "" {
		t.Fatal("native shortcut lost execution owner before command dispatch")
	}
	msg, ok := cmd().(testsDoneMsg)
	if !ok || msg.err != nil {
		t.Fatalf("run failed: %+v", msg)
	}
	if msg.result.Status != "passed" || len(msg.result.Results) != 1 || msg.result.Results[0].Coverage.Status != "unknown" {
		t.Fatalf("opaque result overclaimed: %+v", msg.result)
	}
	if !reflect.DeepEqual(m.Chat, before) {
		t.Fatal("standalone test mutated Chat")
	}
	refresh := m.onTestDone(msg)
	if refresh == nil {
		t.Fatal("no result refresh")
	}
	h := refresh().(testsHistoryMsg)
	if h.err != nil || len(h.runs) != 1 || h.fresh[msg.result.Selection.ID] != "current" {
		t.Fatalf("receipt not retrievable/current: %+v", h)
	}
	next, _ := m.Update(h)
	m = next.(Model)
	m.Tests.Detail = true
	view := ansi.Strip(m.renderTests(120, 40))
	if !strings.Contains(view, "coverage unknown") || !strings.Contains(view, "ready") || !strings.Contains(view, msg.result.Selection.ID) {
		t.Fatalf("missing independent result: %s", view)
	}
	events, err := session.Read(m.SessionsPath, msg.result.Selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == "coding" || e.Kind == "coding_state" {
			t.Fatal("test archive exposes coding qualification event")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("new code"), 0600); err != nil {
		t.Fatal(err)
	}
	h = m.refreshTests()().(testsHistoryMsg)
	if h.fresh[msg.result.Selection.ID] != "stale" {
		t.Fatalf("changed workspace still current: %+v", h)
	}
}

func TestTestsEnabledSelectionAndHistoricalFailureInspection(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	m := testsModel(t)
	m.StorePath = filepath.Join(root, "graph")
	m.Cfg.Store.Path = m.StorePath
	m.SessionsPath = filepath.Join(root, "sessions")
	m.Cfg.Sessions.Path = m.SessionsPath
	m.Cfg.Decision.Disabled = true
	m.Cfg.Auth.Mode = config.AuthYOLO
	m.Cfg.Coding.CheckCommand = "exit 99"
	m.Cfg.Testing.Suites = append(m.Cfg.Testing.Suites, config.TestSuite{ID: "disabled", Name: "Disabled", Purpose: "Must not run", Expected: "No run", Command: "exit 99", DisabledReason: "User excluded"})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = next.(Model)
	if cmd == nil || m.Tests.Busy == "" {
		t.Fatal("enabled selection not dispatched")
	}
	msg := cmd().(testsDoneMsg)
	if msg.err != nil || len(msg.result.Results) != 1 || msg.result.Results[0].SuiteID != "smoke" || msg.result.Status != "passed" {
		t.Fatalf("enabled run included required/disabled: %+v %v", msg.result, msg.err)
	}
	m.onTestDone(msg)
	m.Tests.Sel = 1
	newest := msg.result
	older := msg.result
	older.Selection.ID = "tests-old"
	older.Results = []run.TestResult{{SuiteID: "smoke", Status: "failed", Reason: "historical assertion failed", Coverage: run.TestCoverage{Status: "incomplete"}}}
	m.Tests.History = []run.TestRun{newest, older}
	if strings.Contains(m.renderTests(120, 80), "historical assertion failed") {
		t.Fatal("compact view should show latest only")
	}
	m.Tests.Detail = true
	if !strings.Contains(m.renderTests(120, 80), "historical assertion failed") {
		t.Fatal("new pass concealed older failure")
	}
	m.Tests.Detail = false
	incomplete := newest
	incomplete.Results = nil
	incomplete.Status = "incomplete"
	incomplete.Ended = ""
	m.Tests.History = append([]run.TestRun{incomplete}, m.Tests.History...)
	if text := m.renderTests(120, 40); !strings.Contains(text, "no final command receipt") || strings.Contains(text, "Result: not run") {
		t.Fatalf("unfinished archive misreported: %s", text)
	}
}

func TestTestsReadinessAndLayouts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	m := testsModel(t)
	m.StorePath = filepath.Join(root, "graph")
	m.Cfg.Store.Path = m.StorePath
	m.SessionsPath = filepath.Join(root, "sessions")
	m.Cfg.Sessions.Path = m.SessionsPath
	m.Cfg.Testing.Suites[0].Prerequisites = []config.TestPrerequisite{{Kind: "manual", Value: "Browser access not verified"}}
	h := m.refreshTests()().(testsHistoryMsg)
	if !strings.Contains(h.ready["smoke"], "unknown prerequisite") || !strings.Contains(h.ready["smoke"], "manual prerequisite is unverified") {
		t.Fatalf("enabled readiness false positive: %+v", h)
	}
	next, _ := m.Update(h)
	m = next.(Model)
	m.Tests.Sel = 1
	view := m.renderTests(120, 40)
	if !strings.Contains(view, "[enabled]") || !strings.Contains(view, "Runnable: unknown prerequisite") || !strings.Contains(view, "manual prerequisite is unverified") {
		t.Fatalf("enabled and runnable collapsed: %s", view)
	}
	m.Cfg.Testing.Suites[0].Enabled = false
	if text := m.renderTests(120, 40); !strings.Contains(text, "[disabled]") || !strings.Contains(text, "Runnable: unknown prerequisite") || !strings.Contains(text, "manual prerequisite is unverified") {
		t.Fatalf("disabled hides readiness: %s", text)
	}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {40, 12}} {
		lines := strings.Split(m.renderTests(size[0], size[1]), "\n")
		if len(lines) != size[1] {
			t.Fatalf("height at %v: %d", size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("width overflow: %q", line)
			}
		}
	}
}

func TestTestsNativeCancelWaitsForCommandSettlement(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	m := testsModel(t)
	m.Tests.Sel = 1
	m.StorePath = filepath.Join(root, "graph")
	m.Cfg.Store.Path = m.StorePath
	m.SessionsPath = filepath.Join(root, "sessions")
	m.Cfg.Sessions.Path = m.SessionsPath
	m.Cfg.Decision.Disabled = true
	m.Cfg.Auth.Mode = config.AuthYOLO
	command := "echo ready > started.txt; sleep 20"
	m.Cfg.Testing.Suites[0].Command = command
	cmd := m.startTest()
	if cmd == nil {
		t.Fatal("not admitted")
	}
	ch := make(chan testsDoneMsg, 1)
	go func() { ch <- cmd().(testsDoneMsg) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "started.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			m.stopTest()
			t.Fatal("command never reached sentinel")
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.stopTest()
	if m.Tests.Busy == "" || !m.Tests.Stopping {
		t.Fatal("cancel released owner before process settlement")
	}
	select {
	case msg := <-ch:
		if msg.result.Status != "cancelled" {
			t.Fatalf("cancel outcome: %+v err=%v", msg.result, msg.err)
		}
		m.onTestDone(msg)
		if m.Tests.Busy != "" {
			t.Fatal("owner retained after settlement")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled native command failed to settle")
	}
}
