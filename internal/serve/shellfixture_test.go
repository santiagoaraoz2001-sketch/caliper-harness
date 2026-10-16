package serve

import (
	"os/exec"
	"runtime"
	"testing"
)

// napCmd is a long-lived no-op process for lifecycle tests. Windows has
// no coreutils sleep; PowerShell's Start-Sleep is always installed.
func napCmd(secs string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds " + secs}
	}
	return []string{"sleep", secs}
}

// scriptCmd runs a shell-script fixture on this platform: POSIX runs it
// through its shebang; Windows needs sh explicitly (Git for Windows
// provides it). Skips when sh is unavailable.
func scriptCmd(t *testing.T, script string, args ...string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("sh not on PATH — shell fixture unavailable (add Git for Windows' bin)")
		}
		return append([]string{"sh", script}, args...)
	}
	return append([]string{script}, args...)
}
