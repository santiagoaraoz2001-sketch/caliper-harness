// Package bundle resolves only application-owned Mac runtime assets.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const Marker = "runtime/manifest.json"

// Required is set only for the self-contained Mac build. Moving its executable
// without the runtime folder fails rather than silently using system Python.
var Required string

func Managed(root string) bool {
	if root != "" && Required == "1" {
		return true
	}
	s, err := os.Stat(filepath.Join(root, Marker))
	return root != "" && err == nil && s.Mode().IsRegular()
}

func Check(root string) error {
	raw, err := os.ReadFile(filepath.Join(root, Marker))
	if err != nil {
		return fmt.Errorf("bundled runtime manifest missing: %s; replace the complete Caliper folder", filepath.Join(root, Marker))
	}
	var manifest struct {
		Format   int    `json:"format"`
		Platform string `json:"platform"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Format != 1 || manifest.Platform != "darwin-arm64" {
		return fmt.Errorf("invalid bundled runtime manifest: %s; replace the complete Caliper folder", filepath.Join(root, Marker))
	}
	for _, profile := range []string{"worker", "decision"} {
		path := Python(root, profile)
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("bundled %s interpreter missing or not executable: %s; replace the complete Caliper folder", profile, path)
		}
	}
	_, err = ToolAt(root, "rg")
	return err
}

func Root() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return RootAt(executable)
}

func RootAt(executable string) string {
	root := filepath.Dir(executable)
	if Managed(root) {
		return root
	}
	return ""
}

// Python returns a bundled path even if missing: a managed bundle must not
// silently run a different system interpreter. Start names the missing path.
func Python(root, profile string) string {
	return filepath.Join(root, "runtime", profile, "bin", "python3")
}

func Tool(name string) (string, error) { return ToolAt(Root(), name) }
func ToolAt(root, name string) (string, error) {
	if name == "rg" && Managed(root) {
		p := filepath.Join(root, "bin", "rg")
		s, err := os.Stat(p)
		if err != nil || !s.Mode().IsRegular() || s.Mode().Perm()&0111 == 0 {
			return "", fmt.Errorf("bundled ripgrep missing or not executable: %s; replace the complete Caliper folder", p)
		}
		return p, nil
	}
	return exec.LookPath(name)
}

// PythonEnv leaves project/custom processes alone. Only built-in bundled
// Python gets a private import environment; model/cache locations are retained.
func PythonEnv(base, extra []string) []string {
	drop := map[string]bool{"PYTHONHOME": true, "PYTHONPATH": true, "PYTHONUSERBASE": true, "PYTHONSTARTUP": true, "PYTHONNOUSERSITE": true, "PYTHONDONTWRITEBYTECODE": true, "VIRTUAL_ENV": true, "CONDA_PREFIX": true, "__PYVENV_LAUNCHER__": true, "DYLD_LIBRARY_PATH": true, "DYLD_FRAMEWORK_PATH": true}
	var out []string
	for _, env := range append(append([]string(nil), base...), extra...) {
		key, _, _ := strings.Cut(env, "=")
		if !drop[key] && !strings.HasPrefix(key, "PYTHON") {
			out = append(out, env)
		}
	}
	return append(out, "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1")
}
