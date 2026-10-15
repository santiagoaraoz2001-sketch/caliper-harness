package tui

import (
	"fmt"
	"strconv"
)

// Root prompts start visible turns. Interjections and model/tool events keep
// their existing transcript styling without creating navigation boundaries.
func (c *Chat) appendTurn(prompt string) {
	c.TurnStarts = append(c.TurnStarts, len(c.Lines))
	c.appendLine(lineUser, prompt)
}

// Offsets always start at the latest submitted prompt, not the reading cursor.
// This is local view navigation; it never reads or changes model context.
func (m *Model) turnsChat(args []string) {
	offset := 0
	if len(args) > 1 {
		m.alert("err", "usage: /turns [-N|0|current]")
		return
	}
	if len(args) == 1 && args[0] != "current" {
		var err error
		offset, err = strconv.Atoi(args[0])
		if err != nil || offset > 0 {
			m.alert("err", "use /turns -N for earlier turns; /turns, 0 or current returns to latest")
			return
		}
	}
	if offset == 0 {
		if m.Tabs[m.Active].at.view != ViewChat {
			m.navigate(ViewChat, "")
		}
		m.Chat.unpin()
		return
	}
	latest := len(m.Chat.TurnStarts) - 1
	if latest < 0 {
		m.alert("info", "No turns retained in this chat view · /turns returns to latest")
		return
	}
	if offset < -latest {
		m.alert("info", fmt.Sprintf("Only %d earlier turns retained in this chat view · oldest is /turns -%d", latest, latest))
		return
	}
	line := m.Chat.TurnStarts[latest+offset]
	rows := chatRows(m, chatTextWidth(m.Width))
	for i, row := range rows {
		if row.origin.Line != line || row.origin.Part != lineUser || row.origin.Offset != 0 {
			continue
		}
		if m.Tabs[m.Active].at.view != ViewChat {
			m.navigate(ViewChat, "")
		}
		limit := max(0, len(rows)-chatBodyHeight(viewHeight(*m)))
		m.Chat.pinTop = min(i, limit)
		m.Chat.pinned = len(rows) > viewHeight(*m)
		m.captureChatAnchor()
		return
	}
	m.alert("info", "Selected turn is unavailable in this chat view · /turns returns to latest")
}

func (c Chat) turnLabel(line int) string {
	for i := len(c.TurnStarts) - 1; i >= 0; i-- {
		if c.TurnStarts[i] <= line {
			return fmt.Sprintf("Turn %d · ", i-len(c.TurnStarts)+1)
		}
	}
	return ""
}
