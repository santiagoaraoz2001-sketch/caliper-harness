package bash

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ShellPath rejects the Windows WSL launcher: its filesystem, quoting and
// executable namespace differ from the captured Windows workspace.
func ShellPath() (string, error) {
	path, err := exec.LookPath("bash")
	if runtime.GOOS != "windows" {
		return path, err
	}
	if err == nil && !windowsLauncher(path) {
		return path, nil
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")} {
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, "Git", "bin", "bash.exe")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("native Bash unavailable: use Caliper's bundled Bash or Git for Windows; the System32 WSL launcher cannot run Windows project checks")
}

func windowsLauncher(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(filepath.Clean(path), "\\", "/"))
	return strings.Contains(p, "/system32/") || strings.Contains(p, "/sysnative/") || strings.Contains(p, "/windowsapps/")
}

// Windows' native command-line adapter and Git Bash do not share -c quoting.
// Pass script bytes in an owner-created file, leaving stdin and child lifetime
// unchanged. Never rewrite escapes in authored source.
func scriptArgs(script string) (args []string, cleanup func(), err error) {
	base := []string{"--noprofile", "--norc", "-o", "pipefail"}
	if runtime.GOOS != "windows" {
		return append(base, "-c", script), func() {}, nil
	}
	f, err := os.CreateTemp("", "caliper-script-*.sh")
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { _ = os.Remove(f.Name()) }
	_, writeErr := f.WriteString(script)
	closeErr := f.Close()
	if writeErr != nil {
		cleanup()
		return nil, nil, writeErr
	}
	if closeErr != nil {
		cleanup()
		return nil, nil, closeErr
	}
	return append(base, filepath.ToSlash(f.Name())), cleanup, nil
}
