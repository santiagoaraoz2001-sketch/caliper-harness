package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"specific-harness/internal/tui/slash"
	"specific-harness/internal/watch"
)

func historyFixture(width, height int) Model {
	m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: width, Height: height})
	m.Diff.Max = 100
	for i := 1; i <= 16; i++ {
		path := "space name.go"
		if i%2 == 0 {
			path = "other.go"
		}
		m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: i, Path: path, Op: watch.OpModify, BeforeKnown: true, After: []string{fmt.Sprintf("snapshot %d", i)}, Added: []string{fmt.Sprintf("snapshot %d", i)}, AddedN: 1}})
	}
	return m
}

func TestObservedHistoryScopePagingAndSnapshotIsolation(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		m := historyFixture(sz[0], sz[1])
		before := m.Chat.Context.Clone()
		m = submit(m, "/open-diff 15")
		if !m.filesActive() || m.Files.Capture == nil || m.Files.Viewer.Logical[0] != "snapshot 15" {
			t.Fatal("did not open retained file")
		}
		ticket := m.Files.Ticket
		// The background ticker may load lists, but it must not refresh historical source.
		m = viewportUpdate(m, statusTickMsg{})
		if m.Files.Ticket != ticket {
			t.Fatal("historical snapshot scheduled live refresh")
		}
		m.onFilesOpened(filesOpenedMsg{root: m.Files.Root, title: m.Files.Path, ticket: ticket - 1, lines: []string{"live replacement"}})
		if m.Files.Viewer.Logical[0] != "snapshot 15" {
			t.Fatal("stale file response overwrote snapshot")
		}
		m = submit(m, "/changes")
		if !m.Diff.History || m.Diff.Filter != "space name.go" || len(m.diffIndices()) != 8 || m.diffCur().Seq != 15 {
			t.Fatal("history scope wrong")
		}
		viewportFrame(t, m)
		m = viewportKey(m, tea.KeyPgUp)
		if m.diffCur().Seq >= 15 || m.diffCur().Path != "space name.go" {
			t.Fatal("history page did not move to older matching capture")
		}
		seen := map[int]bool{}
		for i := 0; i < 8; i++ {
			m.diffMove(-1)
		}
		for i := 0; i < 8; i++ {
			seen[m.diffCur().Seq] = true
			m = viewportKey(m, tea.KeyDown)
			viewportFrame(t, m)
		}
		if len(seen) != 8 {
			t.Fatal("retained matching history unreachable")
		}
		m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 17, Path: "other.go"}})
		if m.diffCur().Path != "space name.go" {
			t.Fatal("unrelated event hijacked selection")
		}
		m = submit(m, "/diff")
		if m.Diff.Filter != "" || m.Diff.History {
			t.Fatal("original /diff no longer shows all events")
		}
		if !reflect.DeepEqual(before, m.Chat.Context) {
			t.Fatal("read-only inspection changed model context")
		}
	}
}

func TestObservedHistoryExpiryDeletionPartialAndEmpty(t *testing.T) {
	m := historyFixture(80, 24)
	m.Diff.Max = 2
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 17, Path: "space name.go", Op: watch.OpDelete, Removed: []string{"removed excerpt"}}})
	m = submit(m, "/open-diff 1")
	if len(m.Alerts) == 0 || m.Files.Capture != nil {
		t.Fatal("expired capture was silently reread")
	}
	m = submit(m, "/open-diff 17")
	if !strings.Contains(m.captureNotice(), "removed-line excerpt") {
		t.Fatal("deleted excerpt presented as whole file")
	}
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 18, Path: "space name.go", Truncated: true, After: []string{"partial"}}})
	m = submit(m, "/open-diff 18")
	if !strings.Contains(m.captureNotice(), "partial captured file") {
		t.Fatal("partial snapshot presented as complete")
	}
	m = submit(m, "/changes never-seen.go")
	if m.Diff.Sel != -1 || len(m.diffIndices()) != 0 || !strings.Contains(ansi.Strip(m.renderDiff(80, 14)), "No retained observed changes") {
		t.Fatal("empty history misleading")
	}
	m = submit(m, "/open-diff")
	if m.Files.Capture == nil || m.Files.Capture.Seq != 18 {
		t.Fatal("failed selection changed existing snapshot")
	}
}

func TestChatDiffPreviewBoundAndCapturedFileCommand(t *testing.T) {
	m := New(testCfg(), "test")
	ev := watch.Event{Seq: 42, Path: "large.go", Op: watch.OpModify, BeforeKnown: true}
	for i := 0; i < 20; i++ {
		ev.Removed = append(ev.Removed, "old "+strings.Repeat("long ", 25))
		ev.Added = append(ev.Added, fmt.Sprint("new ", i))
	}
	ev.RemovedN, ev.AddedN = 20, 20
	ev.After = []string{"exact snapshot"}
	m.onDiffEvent(diffEventMsg{ev: ev})
	n := 0
	for _, row := range chatRows(&m, 74) {
		if row.kind == "added" || row.kind == "removed" {
			n++
		}
	}
	if n != chatDiffPreviewRows {
		t.Fatalf("preview uses %d source rows", n)
	}
	v := ansi.Strip(m.renderChat(80, 30))
	if !strings.Contains(v, "preview rows hidden") || !strings.Contains(v, "/open-diff 42") {
		t.Fatal("collapsed diff missing disclosure/action")
	}
	m = submit(m, "/open-diff 42")
	if m.Files.Viewer.Logical[0] != "exact snapshot" || len(m.Diff.Events[0].Removed) != 20 {
		t.Fatal("preview cap destroyed captured source")
	}
}

func TestObservedHistoryRestoresScopeAndRetainsDisplayedCapture(t *testing.T) {
	m := historyFixture(80, 24)
	m = submit(m, "/changes space name.go")
	m = submit(m, "/changes other.go")
	m = key(m, "ctrl+o")
	if m.Diff.Filter != "space name.go" {
		t.Fatal("back lost history scope")
	}
	m = key(m, "ctrl+g")
	if m.Diff.Filter != "other.go" {
		t.Fatal("forward lost history scope")
	}
	m = key(m, "ctrl+t")
	m = submit(m, "/changes space name.go")
	m = key(m, "ctrl+p")
	if m.Diff.Filter != "space name.go" {
		t.Fatal("workspace Changes tab lost its explicitly selected scope")
	}
	m = key(m, "ctrl+n")
	if m.Diff.Filter != "space name.go" {
		t.Fatal("next tab lost history scope")
	}
	m = viewportKey(m, tea.KeyEnter)
	seq := m.Diff.Viewer.Ev.Seq
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 19, Path: "space name.go", After: []string{"NEW CONTENT"}}})
	m = submit(m, "/open-diff")
	if m.Files.Capture.Seq != seq {
		t.Fatal("incoming event substituted displayed capture")
	}
	m.onFilesLoaded(filesLoadedMsg{err: errors.New("stale list error")})
	if m.Files.Err != "" || !strings.Contains(ansi.Strip(m.renderFiles(80, 14)), fmt.Sprintf("snapshot %d", seq)) {
		t.Fatal("delayed explorer error hid snapshot")
	}
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 20, Path: "space name.go", ReadError: "permission denied", After: []string{"last known source"}}})
	m = submit(m, "/open-diff 20")
	if !strings.Contains(m.captureNotice(), "read failed; last known capture") || !strings.Contains(m.captureNotice(), "permission denied") {
		t.Fatal("failed read concealed stale source")
	}
}

func TestMarkGutterPreservesEveryRenderedContentSegment(t *testing.T) {
	for _, width := range []int{80, 120} {
		for _, kind := range []string{lineUser, lineAssistant, lineResult, lineTool} {
			m := New(testCfg(), "test")
			payload := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 12) + "EXACT_TAIL"
			if kind == lineTool {
				payload = "bash cmd=" + payload
			}
			m.Chat.appendLine(kind, payload)
			rows := chatRows(&m, chatTextWidth(width))
			rendered := strings.Split(ansi.Strip(m.renderChat(width, len(rows))), "\n")
			marks := 0
			for i, row := range rows {
				if strings.HasPrefix(row.kind, "dot|") {
					marks++
				}
				if row.text != "" && !strings.Contains(rendered[i], ansi.Strip(row.text)) {
					t.Fatalf("%s at%d clipped segment %q", kind, width, row.text)
				}
			}
			if marks > 2 || (kind != lineResult && marks != 1) {
				t.Fatalf("%s repeated logo %d times", kind, marks)
			}
		}
		m := New(testCfg(), "test")
		source := strings.Repeat("0123456789", 25) + "SOURCE_TAIL"
		m.Chat.appendLine(lineAssistant, "```text\n"+source+"\n```")
		rows := chatRows(&m, chatTextWidth(width))
		rendered := strings.Split(ansi.Strip(m.renderChat(width, len(rows))), "\n")
		for i, row := range rows {
			if row.text != "" && !strings.Contains(rendered[i], row.text) {
				t.Fatal("source segment clipped")
			}
		}
	}
}

func TestOriginalCommandsRemainUnchangedWithAdditions(t *testing.T) {
	original := []string{"chat", "diff", "plans", "sessions", "decisions", "workflow", "workflows", "files", "settings", "status", "models", "new", "stop", "retry", "btw", "tools", "auth", "recall", "remember", "compact", "context", "clear", "comment", "goto", "help", "quit"}
	commands := slash.Commands()
	names := map[string]bool{}
	for _, c := range commands {
		if names[c.Name] {
			t.Fatalf("duplicate command %s", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range original {
		if !names[name] {
			t.Fatalf("original command %s missing", name)
		}
	}
	additions := []string{"open-diff", "changes", "turns", "setup", "update", "manage", "style"}
	for _, name := range additions {
		if !names[name] {
			t.Fatalf("added command %s missing", name)
		}
	}
	if len(commands) != len(original)+len(additions) {
		t.Fatal("unexpected registry changes")
	}
}

func TestSelectionBackgroundSurvivesNestedStylesAndPlainProfile(t *testing.T) {
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(termenv.TrueColor)
	row := selectionRow("model "+PathSt.Render("path")+" "+InfoSt.Render("resident"), 40)
	bg := false
	seen := 0
	for len(row) > 0 {
		if loc := sgrSequence.FindStringIndex(row); loc != nil && loc[0] == 0 {
			seq := row[:loc[1]]
			row = row[loc[1]:]
			if seq == "\x1b[0m" || seq == "\x1b[49m" {
				bg = false
			}
			if strings.Contains(seq, "48;2;42;42;42") {
				bg = true
			}
			continue
		}
		if !bg {
			t.Fatal("unshaded selected text or padding")
		}
		_, n := utf8.DecodeRuneInString(row)
		row = row[n:]
		seen++
	}
	if seen != 40 {
		t.Fatal("selected row width changed")
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	if strings.Contains(selectionRow("plain", 8), "\x1b") {
		t.Fatal("plain profile emits color escapes")
	}
}

func TestSystemIndicatorKeepsDispositionWithoutExtraRows(t *testing.T) {
	for _, label := range []string{"error: ", "warning: "} {
		for _, body := range []string{"short", strings.Repeat("long message ", 20)} {
			m := New(testCfg(), "test")
			m.Chat.appendLine(lineSys, label+body)
			rows := chatRows(&m, 74)
			head := strings.Split(rows[0].kind, "|")
			if len(head) != 3 || head[0] != "dot" || head[1] == "system" {
				t.Fatal("system indicator lost its disposition")
			}
			if len(rows) != len(Wrap(label+body, 72)) {
				t.Fatal("decoration added rows to the message")
			}
		}
	}
}

func TestChatDotsPreserveCategoriesWithoutLogos(t *testing.T) {
	m := historyFixture(80, 24)
	m.Chat.Lines = nil
	m.Chat.appendLine(lineUser, "inspect the file")
	m.Chat.Thoughts = map[int][]recordedThought{1: {{Label: "Reasoning", Text: "preserve existing behavior"}}}
	m.Chat.appendLine(lineAssistant, "I will inspect its source.")
	m.Chat.appendLine(lineTool, "read path=source.go")
	m.Chat.appendLine(lineResult, "recorded output")
	m.Chat.appendLine(lineDiff, "15")
	rows := chatRows(&m, chatTextWidth(80))
	marks := 0
	colors := map[string]bool{}
	for _, row := range rows {
		parts := strings.Split(row.kind, "|")
		if len(parts) == 3 {
			if parts[0] == "dot" {
				marks++
			}
			colors[parts[1]] = true
			if parts[0] != "dot" {
				t.Fatal("chat logo remains")
			}
		}
	}
	if marks != 6 || !colors["user"] || !colors["thinking"] || !colors["assistant"] || !colors["tool"] || !colors["result"] || !colors["diff"] {
		t.Fatal("anchors repeat or categories lost")
	}
	viewportFrame(t, m)
}
