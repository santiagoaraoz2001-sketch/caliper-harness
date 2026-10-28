//go:build !windows

package tui

import tea "github.com/charmbracelet/bubbletea"

func ConsoleInput() ([]tea.ProgramOption, func(), error) { return nil, func() {}, nil }
func ObserveConsoleSize(*tea.Program) func()             { return func() {} }
