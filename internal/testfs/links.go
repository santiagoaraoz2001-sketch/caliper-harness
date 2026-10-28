// Package testfs provides native filesystem fixtures for integration tests.
package testfs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// Symlink uses an actual symbolic link where available. On Windows a directory
// junction exercises the same reparse-point boundary without elevated privilege.
// File links have no equivalent fallback and return their actual creation error.
func Symlink(target, link string) error {
	err := os.Symlink(target, link)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(link), resolved)
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil || !info.IsDir() {
		return err
	}
	resolved, statErr = filepath.Abs(resolved)
	if statErr != nil {
		return statErr
	}
	out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", link, resolved).CombinedOutput()
	if junctionErr != nil {
		return fmt.Errorf("directory junction: %w: %s (symlink: %v)", junctionErr, out, err)
	}
	return nil
}

// FileSymlink keeps non-link assertions running when Windows cannot create a
// file symlink. The named subtest makes this precise coverage gap visible.
func FileSymlink(t *testing.T, target, link string) bool {
	t.Helper()
	created := false
	t.Run("native_file_symlink_fixture", func(t *testing.T) {
		if err := os.Symlink(target, link); err != nil {
			if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
				t.Skipf("file symlink requires Developer Mode or privilege: %v", err)
			}
			t.Fatal(err)
		}
		created = true
	})
	return created
}
