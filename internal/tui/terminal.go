package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// terminalOpen launches argv in a new terminal tab. A package var so
// tests capture the spawn without opening windows.
var terminalOpen = openTerminalTab

// openTerminalTab launches argv[0] with argv[1:] in a new tab of the
// user's terminal: macOS Terminal/iTerm2 via osascript, Windows Terminal
// via wt.exe. Anything else refuses loudly instead of guessing.
func openTerminalTab(argv []string) error {
	bin, args, err := terminalCommand(argv)
	if err != nil {
		return err
	}
	if err := exec.Command(bin, args...).Start(); err != nil {
		return fmt.Errorf("cannot open terminal: %v", err)
	}
	return nil
}

// terminalCommand builds the opener invocation: pure, so tests pin
// the program routing and the shell quoting.
func terminalCommand(argv []string) (string, []string, error) {
	if runtime.GOOS == "windows" {
		return windowsTerminalCommand(argv)
	}
	if runtime.GOOS != "darwin" {
		return "", nil, fmt.Errorf("new terminal tabs need macOS (Terminal/iTerm2); on %s resume with: %s", runtime.GOOS, strings.Join(argv, " "))
	}
	cmd := "cd " + shellQuote(cwd()) + " && exec " + shellJoin(argv)
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return "osascript", []string{"-e",
			`tell application "iTerm2" to tell current window to create tab with default profile command "` + cmd + `"`}, nil
	case "Apple_Terminal", "":
		return "osascript", []string{"-e",
			`tell application "Terminal" to do script "` + cmd + `"`}, nil
	default:
		return "", nil, fmt.Errorf("terminal %q cannot open tabs; resume with: %s", os.Getenv("TERM_PROGRAM"), strings.Join(argv, " "))
	}
}

// windowsTerminalCommand opens argv in a new Windows Terminal tab in the
// current window and directory. Only a session already inside Windows
// Terminal (WT_SESSION set) is accepted; anything else refuses with the
// resume command instead of guessing. WT_SESSION is set by Windows
// Terminal itself for every tab it hosts.
func windowsTerminalCommand(argv []string) (string, []string, error) {
	if os.Getenv("WT_SESSION") == "" {
		return "", nil, fmt.Errorf("new terminal tabs need Windows Terminal; resume with: %s", strings.Join(argv, " "))
	}
	if len(argv) == 0 || argv[0] == "" {
		return "", nil, fmt.Errorf("no executable supplied for terminal tab")
	}
	// wt has its own command separator syntax. Refuse semicolons rather than
	// allowing a resume argument or directory to introduce another tab command.
	dir := cwd()
	for _, a := range append([]string{dir}, argv...) {
		if strings.ContainsAny(a, ";\x00\r\n") {
			return "", nil, fmt.Errorf("terminal tab arguments contain unsupported separators; resume directly")
		}
	}
	// Keep argv raw: os/exec performs Windows command-line marshaling.
	// Prequoting here would turn quoting characters into literal argument data.
	args := append([]string{"-w", "0", "nt", "-d", dir, "--"}, argv...)
	return "wt.exe", args, nil
}

func cwd() string {
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "."
}

// shellQuote wraps one argv in single quotes against the tab's shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}
