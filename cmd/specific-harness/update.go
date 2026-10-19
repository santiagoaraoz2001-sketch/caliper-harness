package main

import (
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/bundle"
	"specific-harness/internal/config"
	"specific-harness/internal/update"
)

// The existing installed launcher can remain pointed at an older immutable
// folder. Dispatch happens only in a new process, never underneath a live run.
func launchSelectedVersion() (bool, int) {
	if bundle.Root() == "" {
		return false, 0
	}
	manager, err := update.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Update selection:", err)
		return false, 0
	}
	selected, err := manager.Selected()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Update selection:", err, "— using this installed version")
		return false, 0
	}
	if selected == nil || !update.Newer(selected.Version, version) {
		return false, 0
	}
	exe, _ := os.Executable()
	if filepath.Clean(exe) == filepath.Clean(selected.Executable()) {
		return false, 0
	}
	if err := selected.EnsurePrerequisites(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 1
	}
	code, err := runSelected(*selected)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot launch staged Caliper:", err)
		return true, code
	}
	return true, code
}

// npm delegates activation to this same owner used by /update. It deliberately
// runs before selected-version dispatch and never resolves the install cwd as
// a project or writes a project configuration.
func activateInstalledVersion() int {
	manager, err := update.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	selected, err := manager.Selected()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	oldRoot := ""
	if selected != nil {
		oldRoot = selected.Root
	}
	root := bundle.Root()
	if root == "" {
		fmt.Fprintln(os.Stderr, "Installation activation requires the complete Caliper product")
		return 1
	}
	expected := filepath.Join(manager.Home, "versions", version, manager.Platform, "Caliper")
	canonical, err := filepath.EvalSymlinks(expected)
	if err != nil || canonical != root {
		fmt.Fprintln(os.Stderr, "Installation activation requires its immutable version folder:", expected)
		return 1
	}
	if err = config.PreserveInstalledPrompts(oldRoot, filepath.Join(root, "prompts.yaml")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, err = manager.Activate(&update.Selection{Version: version, Platform: manager.Platform, Root: expected})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Caliper " + version + " selected for the next launch; previous versions and work are preserved.")
	return 0
}
