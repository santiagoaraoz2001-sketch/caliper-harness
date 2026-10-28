package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/bundle"
	"specific-harness/internal/config"
	appupdate "specific-harness/internal/update"
)

type updateState struct {
	busy     bool
	cancel   context.CancelFunc
	ctx      context.Context
	progress appupdate.Progress
}
type updateCheckedMsg struct {
	manager  appupdate.Manager
	release  *appupdate.Release
	err      error
	explicit bool
}
type updatePreparedMsg struct {
	manager   appupdate.Manager
	selection *appupdate.Selection
}
type updateProgressMsg struct {
	ctx      context.Context
	progress appupdate.Progress
}

type updateFinishedMsg struct {
	version  string
	err      error
	declined bool
}

func (m *Model) checkUpdate(explicit bool) tea.Cmd {
	if m.Updater == nil {
		m.Updater = &updateState{}
	}
	if m.Updater.busy {
		m.AddSysLine("An update is already in progress.")
		return nil
	}
	if bundle.Root() == "" {
		if explicit {
			m.AddSysLine("Updates are available in installed Caliper releases; this is a development binary.")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	*m.Updater = updateState{busy: true, cancel: cancel, ctx: ctx}
	current := m.Version
	timeout := time.Duration(m.Cfg.Serve.ProbeTimeoutSeconds) * time.Second
	return func() tea.Msg {
		defer cancel()
		// Reuse the user's existing readiness/network probe timeout. Checking never
		// holds TUI startup or changes the running model's request budget.
		checkCtx, stop := context.WithTimeout(ctx, timeout)
		defer stop()
		manager, e := appupdate.Default()
		if e != nil {
			return updateCheckedMsg{err: e, explicit: explicit}
		}
		release, e := manager.Check(checkCtx, current)
		return updateCheckedMsg{manager: manager, release: release, err: e, explicit: explicit}
	}
}
func (m *Model) updateChecked(msg updateCheckedMsg) tea.Cmd {
	if m.Updater == nil {
		m.Updater = &updateState{}
	}
	*m.Updater = updateState{}
	if m.quitPending {
		return m.requestQuit()
	}
	if msg.err != nil {
		if msg.explicit {
			m.AddSysLine("Update check failed: " + msg.err.Error())
		}
		return nil
	}
	if msg.release == nil {
		if msg.explicit {
			m.AddSysLine("Caliper is up to date.")
		}
		return nil
	}
	if !msg.explicit && (m.Chat.Dialog != nil || m.Chat.Running || m.Wf.Busy != "" || m.Tests.Busy != "") {
		m.AddSysLine(fmt.Sprintf("Caliper %s is available. Use /update when ready; current work continues.", msg.release.Version))
		return nil
	}
	answer := make(chan string, 1)
	if msg.explicit {
		answer <- "y"
		m.AddSysLine("Downloading Caliper " + msg.release.Version + " for next launch; your current run continues.")
	} else {
		m.Chat.Dialog = &Dialog{Kind: "auth", Prompt: fmt.Sprintf("Caliper %s is available. Download the complete update for your next launch? [y/N] Previous versions and your work are preserved.", msg.release.Version), Answer: answer}
	}
	ctx, cancel := context.WithCancel(context.Background())
	*m.Updater = updateState{busy: true, cancel: cancel, ctx: ctx}
	var prog *tea.Program
	if m.Shared != nil {
		prog = m.Shared.Prog
	}
	manager := msg.manager
	manager.Progress = func(p appupdate.Progress) {
		if prog != nil {
			prog.Send(updateProgressMsg{ctx: ctx, progress: p})
		}
	}
	return func() tea.Msg {
		defer cancel()
		select {
		case <-ctx.Done():
			return updateFinishedMsg{err: ctx.Err()}
		case response := <-answer:
			if strings.ToLower(strings.TrimSpace(response)) != "y" && strings.ToLower(strings.TrimSpace(response)) != "yes" {
				return updateFinishedMsg{declined: true}
			}
		}
		selected, e := manager.Prepare(ctx, *msg.release)
		if e != nil {
			return updateFinishedMsg{err: e}
		}
		return updatePreparedMsg{manager: msg.manager, selection: selected}
	}
}
func (m *Model) updateFinished(msg updateFinishedMsg) tea.Cmd {
	if m.Updater == nil {
		m.Updater = &updateState{}
	}
	*m.Updater = updateState{}
	if msg.err != nil {
		m.AddSysLine("Update was not activated: " + msg.err.Error())
	} else if msg.declined {
		m.AddSysLine("Update declined; this version is unchanged.")
	} else {
		m.AddSysLine(fmt.Sprintf("Caliper %s is ready for your next launch. Close and reopen Caliper when your work is settled; the current run is unchanged.", msg.version))
	}
	if m.quitPending {
		return m.requestQuit()
	}
	return nil
}

// Downloading does not capture mutable Settings. Preservation and activation
// happen at a UI event boundary, using the current on-disk configuration.
func (m *Model) updatePrepared(msg updatePreparedMsg) tea.Cmd {
	if m.quitPending {
		return m.updateFinished(updateFinishedMsg{err: context.Canceled})
	}
	selected, err := msg.manager.Selected()
	if err != nil {
		return m.updateFinished(updateFinishedMsg{err: err})
	}
	// Another process may finish a newer update during this download. A TUI
	// update must not undo it; explicit npm version selection remains separate.
	if selected != nil && appupdate.Newer(selected.Version, msg.selection.Version) {
		m.AddSysLine("A newer Caliper " + selected.Version + " was selected while downloading; keeping it selected.")
		return m.updateFinished(updateFinishedMsg{version: selected.Version})
	}
	cfgPath := m.CfgPath
	if cfgPath == "" {
		cfgPath = config.DefaultFile()
	}
	load := config.Load
	if m.Cfg.ProjectOverride {
		load = config.LoadProject
	}
	cfg, _, e := load(cfgPath)
	if e == nil {
		e = config.PreserveCustomPrompts(cfgPath, cfg, bundle.Root(), filepath.Join(msg.selection.Root, "prompts.yaml"))
	}
	if e == nil {
		_, e = msg.manager.Activate(msg.selection)
	}
	if e == nil {
		if saved, _, loadErr := load(cfgPath); loadErr == nil {
			m.Cfg.Prompts = saved.Prompts
		}
	}
	return m.updateFinished(updateFinishedMsg{version: msg.selection.Version, err: e})
}

// Progress is a single replaceable display row. Never append chunks to the
// transcript, and never let a late event resurrect a completed attempt.
func (m *Model) updateProgress(msg updateProgressMsg) {
	if m.Updater == nil || !m.Updater.busy || m.Updater.ctx != msg.ctx {
		return
	}
	m.Updater.progress = msg.progress
}
func (m Model) updateProgressLine() string {
	if m.Updater == nil || !m.Updater.busy || m.Updater.progress.Phase == "" {
		return ""
	}
	p := m.Updater.progress
	line := "Update: " + p.Phase
	if p.Phase == "Downloading update" {
		line += fmt.Sprintf(" %.1f MiB", float64(p.Bytes)/(1<<20))
		if p.TotalBytes > 0 {
			line += fmt.Sprintf(" / %.1f MiB", float64(p.TotalBytes)/(1<<20))
		}
	}
	if p.Phase == "Verifying installed files" && p.TotalFiles > 0 {
		line += fmt.Sprintf(" %d/%d files", p.Files, p.TotalFiles)
	}
	return line + fmt.Sprintf(" (%s elapsed)", p.Elapsed.Round(time.Second))
}
