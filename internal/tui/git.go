package tui

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// gitInfo reports the current branch plus the worktree root's base
// name for dir ("" means cwd). Outside a repo it errors.
func gitInfo(dir string) (branch, base string, err error) {
	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = dir
	out, err := branchCmd.Output()
	if err != nil {
		return "", "", err
	}
	topCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	topCmd.Dir = dir
	top, err := topCmd.Output()
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(out)), filepath.Base(strings.TrimSpace(string(top))), nil
}
