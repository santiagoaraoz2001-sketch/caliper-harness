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
// user's terminal. macOS only (Terminal/iTerm2 via osascript); anything
// else refuses loudly instead of guessing.
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
