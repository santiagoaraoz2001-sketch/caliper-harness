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
	"specific-harness/internal/pathutil"
	"strings"
)

// projectFingerprint observes project contents, not command spelling. Git workspaces
// include tracked and nonignored untracked files; other workspaces include all
// files. Exclusions are exact harness-owned state paths, never arbitrary data
// directories. This detects persistent changes, not temporary writes or effects
// outside the project, and is not a filesystem sandbox.
// projectInventory resolves the root and lists project file names under
// the same scope rules as projectFingerprint (git tracked/nonignored, or
// a full walk), returning the exclusion predicate for per-file checks.
func projectInventory(ctx context.Context, root string, excludes []string) (string, []string, func(string) bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", nil, nil, err
	}
	root, err = pathutil.EvalSymlinks(root)
	if err != nil {
		return "", nil, nil, err
	}
	if _, err := os.Stat(root); err != nil {
		return "", nil, nil, err
	}
	excluded := func(path string) bool {
		for _, ex := range excludes {
			if ex == "" {
				continue
			}
			abs, err := filepath.Abs(ex)
			if resolved, e := pathutil.EvalSymlinks(abs); e == nil {
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
			return "", nil, nil, e
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
			return "", nil, nil, err
		}
	}
	sort.Strings(names)
	return root, names, excluded, nil
}

// projectFileHashes observes per-file content digests under the same scope
// as projectFingerprint, so a shell mutation can name the files it touched.
// Evidence naming only; invalidation still uses the whole-project fingerprint.
func projectFileHashes(ctx context.Context, root string, excludes []string) (map[string]string, error) {
	root, names, excluded, err := projectInventory(ctx, root, excludes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(root, name)
		if excluded(path) {
			continue
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if pathutil.IsLinkMode(info.Mode()) {
			target, e := os.Readlink(path)
			if e != nil {
				continue
			}
			out[name] = "link:" + target
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		content := sha256.New()
		_, copyErr := io.Copy(content, f)
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out[name] = hex.EncodeToString(content.Sum(nil))
	}
	return out, ctx.Err()
}

func projectFingerprint(ctx context.Context, root string, excludes []string) (string, error) {
	root, names, excluded, err := projectInventory(ctx, root, excludes)
	if err != nil {
		return "", err
	}
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
		if pathutil.IsLinkMode(info.Mode()) {
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
