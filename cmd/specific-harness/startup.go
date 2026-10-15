package main

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"os"
	"path/filepath"
	"specific-harness/internal/bundle"
	"specific-harness/internal/tui"
	"strings"
	"sync"
	"time"
)

type startupDisplay struct {
	mu             sync.Mutex
	width          int
	animated       bool
	label, message string
	stop, settled  chan struct{}
	frame          int
}

func interactiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func newStartup(version, path string) *startupDisplay {
	w, _, err := term.GetSize(os.Stderr.Fd())
	if err != nil {
		w = 80
	}
	s := &startupDisplay{width: max(20, w), animated: term.IsTerminal(os.Stderr.Fd()) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && os.Getenv("CALIPER_HARNESS_REDUCED_MOTION") != "1" && os.Getenv("SPECIFIC_HARNESS_REDUCED_MOTION") != "1"}
	cwd, _ := os.Getwd()
	fmt.Fprint(os.Stderr, tui.StartupHeader(version, filepath.Base(cwd), s.width))
	return s
}

func (s *startupDisplay) pending() {
	glyph := string([]rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")[s.frame%10])
	row := glyph + " " + s.label + " · " + s.message
	fmt.Fprint(os.Stderr, "\r\x1b[2K  "+tui.PathSt.Render(ansi.Truncate(row, s.width-4, "…")))
}

func (s *startupDisplay) begin(label, message string) {
	s.label, s.message, s.frame = label, message, 0
	if !s.animated {
		fmt.Fprintf(os.Stderr, "  · %s · %s\n", label, message)
		return
	}
	s.stop, s.settled = make(chan struct{}), make(chan struct{})
	s.pending()
	go func() {
		defer close(s.settled)
		tick := time.NewTicker(90 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
				s.mu.Lock()
				s.frame++
				s.pending()
				s.mu.Unlock()
			}
		}
	}()
}

func (s *startupDisplay) detail(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = text
	if s.animated {
		s.pending()
	} else {
		fmt.Fprintln(os.Stderr, "    "+text)
	}
}

func (s *startupDisplay) finish(state, message string) {
	if s.animated {
		close(s.stop)
		<-s.settled
		fmt.Fprint(os.Stderr, "\r\x1b[2K")
	}
	s.line(state, s.label, message)
}

func (s *startupDisplay) line(state, label, message string) {
	style, glyph := tui.Guidance, "·"
	switch state {
	case "ok":
		style, glyph = tui.InfoSt, "✓"
	case "warn":
		style, glyph = tui.WarnSt, "!"
	case "error":
		style, glyph = tui.ErrSt, "!"
	}
	fmt.Fprintln(os.Stderr, "  "+style.Render(glyph+" "+label))
	for _, row := range tui.Wrap(message, s.width-4) {
		fmt.Fprintln(os.Stderr, "    "+tui.Guidance.Render(row))
	}
}

func (s *startupDisplay) dependencies() bool {
	root := bundle.Root()
	if root != "" {
		s.begin("Bundled runtimes", "Checking the private Mac runtime")
		if err := bundle.Check(root); err != nil {
			s.finish("error", err.Error())
			return false
		}
		s.finish("ok", "Private Python runtimes and ripgrep found · readiness checked in Status")
	}
	s.begin("Tools", "Checking command availability")
	var missing []string
	for _, name := range []string{"bash", "rg"} {
		if _, err := bundle.Tool(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		s.finish("warn", "Missing "+strings.Join(missing, ", ")+" · install these before using coding tools")
		return root == ""
	}
	s.finish("ok", "Bash and ripgrep available")
	return true
}
