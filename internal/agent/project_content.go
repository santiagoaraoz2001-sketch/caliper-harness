package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// projectFingerprint observes project contents, not command spelling. Git workspaces
// include tracked and nonignored untracked files; other workspaces include all
// files. Exclusions are exact harness-owned state paths, never arbitrary data
// directories. This detects persistent changes, not temporary writes or effects
// outside the project, and is not a filesystem sandbox.
func projectFingerprint(ctx context.Context, root string, excludes []string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(root); err != nil {
		return "", err
	}
	excluded := func(path string) bool {
		for _, ex := range excludes {
			if ex == "" {
				continue
			}
			abs, err := filepath.Abs(ex)
			if resolved, e := filepath.EvalSymlinks(abs); e == nil {
				abs = resolved
			}
			if err == nil && (path == abs || strings.HasPrefix(path, abs+string(filepath.Separator))) {
				return true
			}
		}
		return false
	}
	var names []string
	probe := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel")
	if raw, e := probe.Output(); e == nil && filepath.Clean(strings.TrimSpace(string(raw))) == root {
		raw, e := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", ".").Output()
		if e != nil {
			return "", e
		}
		for _, name := range strings.Split(string(raw), "\x00") {
			if name != "" {
				names = append(names, name)
			}
		}
	} else {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if excluded(path) || d.Name() == ".git" {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				name, e := filepath.Rel(root, path)
				if e != nil {
					return e
				}
				names = append(names, name)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(names)
	h := sha256.New()
	previous := ""
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if name == previous {
			continue
		}
		previous = name
		path := filepath.Join(root, name)
		if excluded(path) {
			continue
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		} // Tracked deletion changes the inventory.
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%q\x00%d\x00", name, info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := os.Readlink(path)
			if e != nil {
				return "", e
			}
			fmt.Fprintf(h, "%q\x00", target)
			info, err = os.Stat(path)
			if os.IsNotExist(err) {
				continue
			} // The dangling link itself is observed.
			if err != nil {
				return "", err
			}
			if info.IsDir() {
				continue
			} // External directory contents are outside this inventory.
		}
		if info.IsDir() { // Git submodule: include its own project inventory.
			digest, e := projectFingerprint(ctx, path, excludes)
			if e != nil {
				return "", e
			}
			fmt.Fprint(h, digest)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		} // Device/pipe identity, never block reading a stream.

		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		content := sha256.New()
		_, err = io.Copy(content, f)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		h.Write(content.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), ctx.Err()
}
