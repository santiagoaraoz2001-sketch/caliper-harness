package process

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// WindowsPythonAlias is a hint, not proof of a functioning interpreter. It is
// cheap enough for tool help and never runs or installs anything.
func WindowsPythonAlias(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.Contains(p, "/windowsapps/") && strings.HasPrefix(filepath.Base(p), "python") && strings.HasSuffix(p, ".exe")
}

// Executable verifies a selected Windows Python alias rather than presenting a
// Store redirector as an installed runtime. Normal executables keep LookPath's
// behavior; only selection/discovery invokes this bounded version check.
func Executable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" || !WindowsPythonAlias(path) {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := Command(ctx, path, "--version")
	out := &versionOutput{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := Run(cmd); err != nil || !strings.HasPrefix(strings.TrimSpace(out.text), "Python 3.") {
		return "", fmt.Errorf("Python Windows Store alias is not a verified interpreter: %s; select an installed Python executable", path)
	}
	return path, nil
}

type versionOutput struct{ text string }

func (w *versionOutput) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(len(p), max(0, 128-len(w.text)))
	w.text += string(p[:keep])
	return n, nil
}
