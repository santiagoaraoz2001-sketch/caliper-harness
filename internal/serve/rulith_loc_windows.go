//go:build windows

package serve

import (
	"os"
	"path/filepath"
)

// LocateRulith finds the per-user Rulith Inference install. The
// installer's recorded location (%LOCALAPPDATA%\Rulith Inference) is
// checked first; both the embedded Python and the manager must exist.
func LocateRulith() (RulithInstall, bool) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return RulithInstall{}, false
	}
	root := filepath.Join(local, "Rulith Inference")
	install := RulithInstall{
		Root:    root,
		Python:  filepath.Join(root, "runtime", "python", "python.exe"),
		Manager: filepath.Join(root, "runtime", "tools", "manager.py"),
	}
	for _, p := range []string{install.Python, install.Manager} {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			return RulithInstall{}, false
		}
	}
	return install, true
}
