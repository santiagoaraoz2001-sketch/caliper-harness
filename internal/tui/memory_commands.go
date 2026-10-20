package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"specific-harness/internal/memory"
	"specific-harness/internal/store"
	"specific-harness/internal/tools/files"
)

// recallChat searches memories into the transcript.
func (m *Model) recallChat(args []string) {
	q := strings.Join(args, " ")
	if q == "" {
		m.alert("err", "usage: /recall <query>")
		return
	}
	workspace, err := files.Capture(m.Cfg.Tools.Bash, m.Cfg.PromptCatalog)
	if err != nil {
		m.alert("err", fmt.Sprintf("recall failed: %v", err))
		return
	}
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", fmt.Sprintf("cannot open store: %v", err))
		return
	}
	block, err := memory.QueryProject(context.Background(), s, workspace.Path, "", q, m.Cfg.Recall.TopN, m.Cfg.Recall.MaxChars, m.Cfg.Recall.MinWord, m.Cfg.PromptCatalog)
	s.Close()
	if err != nil {
		m.alert("err", fmt.Sprintf("recall failed: %v", err))
		return
	}
	if block == "" {
		m.alert("info", fmt.Sprintf("no memories match %q", q))
		return
	}
	m.Chat.appendLine(lineSys, block)
	m.Chat.unpin()
	m.showChat()
}

func (m *Model) memorySessionRef() string {
	if n := len(m.Chat.SessionIDs); n > 0 {
		return m.Chat.SessionIDs[n-1]
	}
	return ""
}

// rememberChat uses the same project notebook the model can recall. Earlier
// global memories remain readable; new global notes require explicit intent.
func (m *Model) rememberChat(args []string) {
	global := len(args) > 0 && args[0] == "--global"
	if global {
		args = args[1:]
	}
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		m.alert("err", "usage: /remember [--global] <text>")
		return
	}
	scope := "global"
	if !global {
		w, err := files.Capture(m.Cfg.Tools.Bash, m.Cfg.PromptCatalog)
		if err != nil {
			m.alert("err", "remember failed: "+err.Error())
			return
		}
		scope = w.Path
	}
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", "cannot open store: "+err.Error())
		return
	}
	var entry *memory.Entry
	if global {
		entry, err = memory.Add(s, "note", text, m.memorySessionRef())
	} else {
		entry, err = memory.PutKnowledge(context.Background(), s, scope, m.memorySessionRef(), "user/note/"+uuid.NewString(), text, "", "", "")
	}
	s.Close()
	if err != nil {
		m.alert("err", "remember failed: "+err.Error())
		return
	}
	m.Chat.appendLine(lineSys, fmt.Sprintf("remembered in %s · %s", scope, entry.ID))
	m.Chat.unpin()
	m.showChat()
}

// styleChat edits only an explicitly authored project document. Running roles
// retain their already captured snapshot; subsequent runs read the new head.
func (m *Model) styleChat(args []string) {
	action, text := "show", ""
	if len(args) > 0 {
		action, text = args[0], strings.TrimSpace(strings.Join(args[1:], " "))
	}
	if (action != "show" && action != "add" && action != "set" && action != "clear") ||
		((action == "add" || action == "set") && text == "") ||
		((action == "show" || action == "clear") && text != "") {
		m.alert("err", "usage: /style [show|add <rule>|set <text>|clear]")
		return
	}
	w, err := files.Capture(m.Cfg.Tools.Bash, m.Cfg.PromptCatalog)
	if err != nil {
		m.alert("err", "style failed: "+err.Error())
		return
	}
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", "cannot open store: "+err.Error())
		return
	}
	defer s.Close()
	ctx := context.Background()
	heads, err := memory.Knowledge(ctx, s, w.Path, memory.StyleguideKey, "", 1, 0, true)
	if err != nil {
		m.alert("err", "style failed: "+err.Error())
		return
	}
	previous, body := "", ""
	if len(heads) > 0 {
		previous, body = heads[0].ID, heads[0].Body
	}
	if action == "show" {
		if body == "" {
			m.alert("info", "no project style guidance; /style add <rule>")
			return
		}
		m.Chat.appendLine(lineSys, fmt.Sprintf("project style · %s · version %s\n%s", w.Path, previous, body))
	} else {
		var entry *memory.Entry
		if action == "add" {
			entry, err = memory.AppendStyleguideRule(ctx, s, w.Path, m.memorySessionRef(), text)
		} else {
			entry, err = memory.PutKnowledge(ctx, s, w.Path, m.memorySessionRef(), memory.StyleguideKey, text, previous, "", "")
		}
		if err != nil {
			m.alert("err", "style failed: "+err.Error())
			return
		}
		m.Chat.appendLine(lineSys, fmt.Sprintf("project style saved · %s · version %s · used from next run", w.Path, entry.ID))
	}
	m.Chat.unpin()
	m.showChat()
}
