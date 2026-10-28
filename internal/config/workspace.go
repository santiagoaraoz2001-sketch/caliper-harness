package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"specific-harness/internal/pathutil"
)

// UserDataDir is independent of both the installed version and the working
// directory. Updates can replace program files without touching project state.
func UserDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support")
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
		if base == "" {
			base, err = os.UserConfigDir()
		}
	default:
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
	}
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("user data directory must be absolute: %s", base)
	}
	return filepath.Join(base, "caliper"), nil
}

// WorkspaceDir uses the full canonical path, including symlink resolution, so
// two unrelated repositories never share a graph merely because names match.
// It computes a location only; inspecting config never creates directories.
func WorkspaceDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cwd, err = pathutil.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	root, err := UserDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "workspaces", fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.Clean(cwd))))), nil
}

func workspaceDefaults(c *Config) error {
	dir, err := WorkspaceDir()
	if err != nil {
		return fmt.Errorf("workspace storage: %w", err)
	}
	// Graph and session defaults form one workspace history. If either exists,
	// keep both legacy locations so future writes cannot split that history.
	legacy := false
	for _, path := range []string{c.Store.Path, c.Sessions.Path} {
		if _, err := os.Stat(path); err == nil {
			legacy = true
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if !legacy {
		c.Store.Path = filepath.Join(dir, "kuzu")
		c.Sessions.Path = filepath.Join(dir, "sessions")
	}
	if _, err := os.Stat(c.Behavior.Path); os.IsNotExist(err) {
		c.Behavior.Path = filepath.Join(dir, "behavior.md")
	} else if err != nil {
		return err
	}
	return nil
}

// CreateWorkspaceConfigDir creates only the managed default's parent. Explicit
// paths retain their existing missing-parent errors rather than creating an
// arbitrary directory tree on the user's behalf.
func CreateWorkspaceConfigDir(path string) error {
	dir, err := WorkspaceDir()
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if absolute == filepath.Join(dir, DefaultPath) {
		return os.MkdirAll(dir, 0700)
	}
	return nil
}
