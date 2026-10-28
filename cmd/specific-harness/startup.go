package main

import (
	"bufio"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"os"
	"path/filepath"
	"specific-harness/internal/bundle"
	"specific-harness/internal/tools/bash"
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

// parseYes reports whether a typed answer accepts a yes/no question.
// Anything that is not an explicit yes — including just pressing Enter —
// is a no, so downloads never start by accident.
func parseYes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

func (s *startupDisplay) askYesNo(question string) bool {
	fmt.Fprintf(os.Stderr, "  ? %s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return parseYes(answer)
}

// summarizeFailures names the first few broken files so the warning says
// what is actually wrong without flooding the startup screen.
func summarizeFailures(failures []bundle.Failure) string {
	names := make([]string, 0, 3)
	for i, failure := range failures {
		if i == 3 {
			break
		}
		names = append(names, failure.String())
	}
	summary := fmt.Sprintf("%d installed file(s) missing or changed: %s", len(failures), strings.Join(names, ", "))
	if len(failures) > len(names) {
		summary += fmt.Sprintf(" · and %d more", len(failures)-len(names))
	}
	return summary
}

// verifyInstall runs the install verification chain: every file recorded
// in MANIFEST.sha256 is hashed, and when only downloadable dependencies
// are broken the user is asked before anything is fetched.
func (s *startupDisplay) verifyInstall(root string) bool {
	s.begin("Install", "Verifying every installed file (SHA-256)")
	_, failures, err := bundle.VerifyManifestProgress(root, func(done, total int) {
		if s.animated && (done == total || done%100 == 0) {
			s.detail(fmt.Sprintf("Verifying every installed file (SHA-256) · %d of %d", done, total))
		}
	})
	if err != nil {
		s.finish("error", err.Error())
		return false
	}
	if len(failures) == 0 {
		s.finish("ok", "Installation verified against MANIFEST.sha256")
		return true
	}
	s.finish("warn", summarizeFailures(failures))
	repairable, unrepairable := bundle.ClassifyFailures(failures)
	if len(unrepairable) > 0 {
		s.line("error", "Install", summarizeFailures(unrepairable)+" · these cannot be downloaded; replace the complete Caliper folder")
		return false
	}
	if !s.askYesNo(fmt.Sprintf("Download the pinned dependencies and repair %d file(s) now?", len(repairable))) {
		s.line("error", "Install", "Repair declined · the broken dependencies are required to launch")
		return false
	}
	s.begin("Dependencies", "Downloading the pinned dependencies")
	if err := bundle.Repair(root, failures, func(text string) { s.detail(text) }); err != nil {
		s.finish("error", err.Error())
		return false
	}
	s.finish("ok", "Dependencies downloaded · installation repaired and verified")
	return true
}

func (s *startupDisplay) dependencies() bool {
	root := bundle.Root()
	if root != "" {
		if !s.verifyInstall(root) {
			return false
		}
		s.begin("Bundled runtimes", "Checking the private runtime")
		if err := bundle.Check(root); err != nil {
			s.finish("error", err.Error())
			return false
		}
		s.finish("ok", "Private Python runtimes and ripgrep found · readiness checked in Models & Servers (/manage)")
	}
	s.begin("Tools", "Checking command availability")
	var missing []string
	if _, err := bash.ShellPath(); err != nil {
		missing = append(missing, "native Bash")
	}
	for _, name := range []string{"rg"} {
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
