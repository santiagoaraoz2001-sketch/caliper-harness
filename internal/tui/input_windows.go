//go:build windows

package tui

import (
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

// ConsoleInput selects Bubble Tea's ANSI parser. Its native Windows event
// reader discards bracketed-paste boundaries and splits UTF-16 surrogate pairs.
// A distinct owned handle avoids that reader, while preserving terminal raw
// mode and Go's UTF-8 console decoding. No global stdin or codepage is changed.
func ConsoleInput() ([]tea.ProgramOption, func(), error) {
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) != nil {
		return nil, func() {}, nil
	}
	var handle windows.Handle
	p := windows.CurrentProcess()
	if err := windows.DuplicateHandle(p, windows.Handle(os.Stdin.Fd()), p, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, nil, err
	}
	in := os.NewFile(uintptr(handle), "Caliper console input")
	return []tea.ProgramOption{tea.WithInput(in)}, func() {
		// Close alone waits for a pending ReadConsoleW. Cancel our owned
		// handle's read first; unrelated console handles remain untouched.
		_ = windows.CancelIoEx(handle, nil)
		_ = in.Close()
	}, nil
}

// ANSI input does not carry Windows console resize records, and Windows has
// no SIGWINCH. Observe only size changes with one owned, lightweight ticker.
func ObserveConsoleSize(p *tea.Program) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	w, h, _ := term.GetSize(os.Stdout.Fd())
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				nw, nh, err := term.GetSize(os.Stdout.Fd())
				if err == nil && nw > 0 && nh > 0 && (nw != w || nh != h) {
					w, h = nw, nh
					p.Send(tea.WindowSizeMsg{Width: w, Height: h})
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }
}
