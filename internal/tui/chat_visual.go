package tui

import (
	"encoding/json"
	"fmt"
	"specific-harness/internal/tool"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type recordedThought struct{ Label, Text string }

const chatDiffPreviewRows = 6

func chatTextWidth(w int) int { return max(1, w-5) } // Action gutter, gap and right scroll track.

// Rows preserve literal transcript/source text; formatting never feeds the model.
func chatRows(m *Model, w int) []chatRow {
	var rows []chatRow
	origin := chatOrigin{Valid: true}
	label, raw, cursor := "", "", 0
	partPrefix := ""
	set := func(part, name string) {
		origin.Part, origin.Offset = partPrefix+part, 0
		label, raw, cursor = name, "", 0
	}
	wrap := func(text string, width int) []string { raw, cursor = ansi.Strip(text), 0; return Wrap(text, width) }
	mk := func(kind, text string) chatRow {
		o := origin
		if raw != "" {
			if at := strings.Index(raw[cursor:], ansi.Strip(text)); at >= 0 {
				o.Offset = cursor + at
				cursor = o.Offset + len(ansi.Strip(text))
			}
		}
		return chatRow{kind: kind, text: text, origin: o, label: label}
	}
	push := func(kind, text string) {
		width := w
		switch kind {
		case lineResult, "resulthead", "failurehead":
			width -= 4
		case "source", "fence", "assistantfence", "thought", lineSys:
			width -= 2
		}
		for i, row := range wrap(text, max(1, width)) {
			rowKind := kind
			if kind == lineSys && i == 0 {
				rowKind = "systemhead"
			}
			if i > 0 {
				switch kind {
				case "assistanthead":
					rowKind = lineAssistant
				case "assistantfence":
					rowKind = "fence"
				case "toolhead", "diffhead", "failurehead", "resulthead":
					rowKind = kind + "cont"
				}
			}
			rows = append(rows, mk(rowKind, row))
		}
	}
	thought := func(at int) {
		for ti, t := range m.Chat.Thoughts[at] {
			set(fmt.Sprintf("thought-head:%d", ti), t.Label)
			rows = append(rows, mk("thoughthead", t.Label))
			set(fmt.Sprintf("thought:%d", ti), t.Label)
			push("thought", t.Text)
		}
	}
	requests := func(at int) {
		limit := len(m.Chat.Lines)
		for next := range m.Chat.NativeRequests {
			if next > at && next < limit {
				limit = next
			}
		}
		for _, call := range m.Chat.NativeRequests[at] {
			shown := false
			for idx, id := range m.Chat.ToolCallIDs {
				if idx >= at && idx < limit && id == call.ID {
					shown = true
				}
			}
			if !shown {
				set("native-head:"+call.ID, call.Name+" request · "+call.ID)
				push("toolhead", Title.Render("Requested "+call.Name)+Muted.Render(" · "+call.ID))
				set("native:"+call.ID, call.Name+" request · "+call.ID)
				for i, row := range wrap(call.Name+" "+call.Arguments, w) {
					kind := lineTool
					if i > 0 {
						kind = "toolcont"
					}
					rows = append(rows, mk(kind, row))
				}
			}
		}
	}
	prose := func(text string) {
		fence := ""
		part, name := origin.Part, label
		for i, line := range strings.Split(text, "\n") {
			set(fmt.Sprintf("%s:line:%d", part, i), name)
			kind := lineAssistant
			if i == 0 {
				kind = "assistanthead"
			}
			token, info := tool.MarkdownFence(line)
			if fence == "" && token != "" {
				fence = token
				kind = "fence"
			} else if fence != "" {
				kind = "source"
				if len(token) >= len(fence) && token[0] == fence[0] && info == "" {
					fence = ""
					kind = "fence"
				}
			}
			if i == 0 && kind == "fence" {
				kind = "assistantfence"
			}
			push(kind, line)
		}
	}
	for idx, line := range m.Chat.Lines {
		partPrefix = ""
		origin = chatOrigin{Line: idx, Valid: true}
		if idx > 0 && (line.Kind == lineUser || line.Kind == lineTool || line.Kind == lineDiff) {
			set("separator", "")
			rows = append(rows, mk("space", ""))
		}
		thought(idx)
		requests(idx)
		set(line.Kind, chatKindLabel(line.Kind))
		switch line.Kind {
		case lineUser:
			for i, row := range wrap(line.Text, max(1, w-2)) {
				kind := lineUser
				if i > 0 {
					kind = lineUserCont
				}
				rows = append(rows, mk(kind, row))
			}
		case lineAssistant:
			prose(line.Text)
		case lineTool:
			name, _, _ := strings.Cut(line.Text, " ")
			name, _, _ = strings.Cut(name, "\n")
			action := name + " requested"
			switch name {
			case "read":
				action = "Read requested"
			case "bash":
				action = "Run requested"
			case "edit":
				action = "Edit requested"
			case "write":
				action = "Write requested"
			case "search":
				action = "Search requested"
			}
			head := Title.Render(action) + Muted.Render(" · ") + PathSt.Render(name)
			_, args, _ := strings.Cut(line.Text, " ")
			var fields map[string]any
			if json.Unmarshal([]byte(args), &fields) == nil {
				if path, ok := fields["path"].(string); ok {
					head += " " + PathSt.Render(path)
				}
			}
			if id := m.Chat.ToolCallIDs[idx]; id != "" {
				head += Muted.Render(" · " + id)
			}
			set("toolhead", action)
			push("toolhead", head)
			set(lineTool, name+" request"+chatIdentity(m.Chat.ToolCallIDs[idx]))
			for i, row := range wrap(line.Text, w) {
				kind := lineTool
				if i > 0 {
					kind = "toolcont"
				}
				rows = append(rows, mk(kind, row))
			}
		case lineResult:
			if entry, ok := m.Chat.Recorded[idx]; ok {
				label := entry.Message.Name
				if label == "" {
					label = "Tool"
				}
				if entry.Message.ToolCallID != "" {
					label += " · " + entry.Message.ToolCallID
				}
				label += " · " + entry.Kind
				kind := "resulthead"
				if entry.Kind == "failure" {
					kind = "failurehead"
				}
				set("resulthead", label)
				push(kind, label)
				set(lineResult, label)
			} else {
				set("resulthead", "Tool result")
				push("resulthead", "Tool result")
				set(lineResult, "Tool result")
			}
			push(lineResult, line.Text)
		case lineDiff:
			seq, _ := strconv.Atoi(line.Text)
			if group, ok := m.Chat.DiffGroups[idx]; ok && group.Count > 1 {
				set("diff:group", "Observed filesystem changes")
				push("diffmeta", fmt.Sprintf("%d observed changes · #%d–#%d · /changes: recent captures", group.Count, group.First, group.Last))
				if !group.Preview {
					continue // Ambient bursts are a quiet summary, not agent actions.
				}
				push("diffmeta", "Latest capture below · /diff retains recent individual captures")
			}
			partPrefix = fmt.Sprintf("capture:%d:", seq)
			found := false
			for _, ev := range m.Diff.Events {
				if ev.Seq != seq {
					continue
				}
				found = true
				set("diffhead", fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
				counts := fmt.Sprintf(" (+%d −%d) · #%d", ev.AddedN, ev.RemovedN, ev.Seq)
				if ev.BeforePartial || ev.Truncated {
					counts = fmt.Sprintf(" (retained prefix +%d −%d; total unknown) · #%d", ev.AddedN, ev.RemovedN, ev.Seq)
				}
				push("diffhead", Title.Render("Observed "+ev.Op)+" "+PathSt.Render(ev.Path)+Muted.Render(counts))
				set("diffmeta", fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
				push("diffmeta", diffMeta(ev))
				var removed, added []chatRow
				for li, line := range ev.Removed {
					set(fmt.Sprintf("removed:%d", li), fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
					for _, row := range wrap(line, max(1, w-2)) {
						removed = append(removed, mk("removed", row))
					}
				}
				for li, line := range ev.Added {
					set(fmt.Sprintf("added:%d", li), fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
					for _, row := range wrap(line, max(1, w-2)) {
						added = append(added, mk("added", row))
					}
				}
				rn := min(len(removed), chatDiffPreviewRows/2)
				an := min(len(added), chatDiffPreviewRows-rn)
				rn = min(len(removed), chatDiffPreviewRows-an)
				rows = append(rows, removed[:rn]...)
				rows = append(rows, added[:an]...)
				if hidden := len(removed) + len(added) - rn - an; hidden > 0 {
					set("diff:hidden", fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
					push("diffmeta", fmt.Sprintf("… %d preview rows hidden", hidden))
				}
				if rest := ev.AddedN + ev.RemovedN - len(ev.Added) - len(ev.Removed); ev.BeforeKnown && rest > 0 {
					set("diff:unretained", fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
					push("diffmeta", fmt.Sprintf("… %d changed source lines not retained in this snippet", rest))
				}
				set("diff:open", fmt.Sprintf("Observed #%d · %s", ev.Seq, ev.Path))
				push("diffmeta", fmt.Sprintf("/open-diff %d opens captured file · /diff lists events", ev.Seq))
			}
			if !found {
				set("diff:expired", "Expired observed capture")
				push("diffmeta", "Observed change capture expired · /diff current captures")
			}
		default:
			push(line.Kind, line.Text)
		}
	}
	partPrefix = ""
	origin = chatOrigin{Line: len(m.Chat.Lines), Valid: true}
	thought(len(m.Chat.Lines))
	requests(len(m.Chat.Lines))
	if m.Chat.Running && m.Chat.Streaming {
		if buf := m.Chat.Stream.String(); buf != "" {
			origin.Line, origin.Stream = -1, true
			set(lineAssistant, "Assistant reply · streaming")
			prose(stripToolBlocks(buf) + "▍")
		} else {
			origin.Line, origin.Stream = -1, true
			label := "Waiting for model…"
			switch m.Chat.StreamActivity {
			case "executing tool":
				label = "Executing tool…"
			case "active":
				label = "Server is responding…"
			case "thinking":
				label = "Thinking…"
			case "tool":
				label = "Receiving tool call…"
			}
			set("waiting", label)
			rows = append(rows, mk("waiting", label))
		}
	}
	if len(rows) == 0 {
		rows = append(rows, mk(lineSys, "Ask anything to begin."), mk(lineSys, "/help lists every command."))
	}
	return markChatRows(rows)
}

// Simple category-colored dots identify headers without adding transcript rows.
func markChatRows(rows []chatRow) []chatRow {
	var out []chatRow
	category := func(kind string) string {
		switch kind {
		case lineUser:
			return "user"
		case "assistanthead", "assistantfence":
			return "assistant"
		case "toolhead":
			return "tool"
		case "thoughthead", "waiting":
			return "thinking"
		case "diffhead":
			return "diff"
		case "failurehead":
			return "failure"
		case "resulthead":
			return "result"
		case "systemhead":
			return "system"
		}
		return ""
	}
	for i := 0; i < len(rows); i++ {
		row := rows[i]
		cat := category(row.kind)
		if cat == "" {
			out = append(out, row)
			continue
		}
		if cat == "system" {
			if resultStyle(row.text).GetForeground() == ErrSt.GetForeground() {
				cat = "system-error"
			} else if resultStyle(row.text).GetForeground() == WarnSt.GetForeground() {
				cat = "system-warning"
			}
		}
		// Separate action blocks, keeping a tool's result attached to its request.
		// Source/diff/continuation rows retain their original local grouping.
		if len(out) > 0 && out[len(out)-1].kind != "space" && cat != "result" && cat != "failure" {
			separator := row
			separator.kind, separator.text, separator.label = "space", "", ""
			separator.origin.Part, separator.origin.Offset = "separator:"+row.origin.Part, 0
			out = append(out, separator)
		}
		row.kind = "dot|" + cat + "|" + row.kind
		out = append(out, row)
	}
	return out
}
func (m *Model) renderChat(w, h int) string {
	rows := chatRows(m, chatTextWidth(w))
	fullHeight := h
	scrollable := len(rows) > h
	if scrollable {
		h = chatBodyHeight(h)
	}
	start := max(0, len(rows)-h)
	if m.Chat.pinned {
		start = min(m.Chat.pinTop, max(0, len(rows)-h))
	}
	end := min(len(rows), start+h)
	var out []string
	if scrollable {
		if fullHeight > 1 {
			identity := "Live"
			if m.Chat.pinned {
				identity = rows[start].label
				if identity == "" {
					identity = "Earlier output"
				}
				identity = m.Chat.turnLabel(rows[start].origin.Line) + identity
			}
			hint := "wheel/PgUp/PgDn · drag bar"
			if m.Chat.pinned {
				hint = "Scrollback · " + hint
			}
			bannerWidth := max(1, w-2)
			out = append(out, Guidance.Render(PadRight(truncate(identity, max(1, bannerWidth-lipgloss.Width(hint)-2)), hint, bannerWidth)))
		}
	}
	for _, row := range rows[start:end] {
		marker, cat := "", ""
		if fields := strings.SplitN(row.kind, "|", 3); len(fields) == 3 {
			cat = fields[1]
			row.kind = fields[2]
			marker = fields[0]
		}
		var text string
		switch row.kind {
		case lineUser:
			text = UserSt.Render(row.text)
		case lineUserCont:
			text = UserSt.Render("  " + row.text)
		case lineAssistant, "assistanthead":
			text = Body.Render(proseText(row.text))
		case "source":
			text = "  " + sourceText(row.text)
		case "fence", "assistantfence":
			text = Muted.Render("  " + row.text)
		case "toolhead", "toolheadcont":
			text = row.text
		case lineTool:
			text = Guidance.Render(row.text)
		case "toolcont":
			text = Guidance.Render(row.text)
		case "thoughthead":
			text = InfoSt.Italic(true).Render(row.text)
		case "thought":
			text = ThinkingSt.Render("  " + row.text)
		case "waiting":
			text = InfoSt.Italic(true).Render(row.text)
		case lineResult:
			text = resultStyle(row.text).Render("  │ " + sourceText(row.text))
		case "resulthead", "resultheadcont":
			text = Muted.Render("  ↳ " + row.text)
		case "failurehead", "failureheadcont":
			text = ErrSt.Render("  ↳ " + row.text)
		case "diffhead", "diffheadcont":
			text = Title.Render(row.text)
		case "diffmeta":
			text = Guidance.Render(row.text)
		case "added":
			text = diffSource("+ ", row.text, chatTextWidth(w), true)
		case "removed":
			text = diffSource("− ", row.text, chatTextWidth(w), false)
		case "space":
			text = ""
		case "systemhead", lineSys:
			text = resultStyle(row.text).Render(row.text)
		default:
			style := resultStyle(row.text)
			text = style.Render("· " + row.text)
		}
		if marker != "" {
			color := Ink
			switch cat {
			case "user":
				color = Accent
			case "tool":
				color = Accent
			case "thinking":
				color = Cyan
			case "result":
				color = Cyan
			case "system":
				color = Cyan
			case "system-error":
				color = ErrHue
			case "system-warning":
				color = WarnHue
			case "diff":
				color = WarnHue
			case "failure":
				color = ErrHue
			}
			text = " " + Body.Foreground(lipgloss.Color(color)).Render("•") + " " + text
		} else if text != "" {
			text = "   " + text
		}
		if row.kind == lineUser || row.kind == lineUserCont {
			surfaceWidth := w
			if scrollable && w > 4 {
				surfaceWidth -= 2 // Reserve the gap/track before padding, avoiding false ellipses.
			}
			text = shadedRow(text, surfaceWidth, UserSt, Shade)
		}
		out = append(out, truncate(text, w))
	}
	out = fitRows(out, fullHeight)
	if scrollable && w > 4 {
		out = withChatScrollTrack(out, w, len(rows), h, start)
	}
	return strings.Join(out, "\n")
}
