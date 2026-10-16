package tui

import "runtime"

// napCmd is a long-lived no-op process for lifecycle tests. Windows has
// no coreutils sleep; PowerShell's Start-Sleep is always installed.
func napCmd(secs string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds " + secs}
	}
	return []string{"sleep", secs}
}
