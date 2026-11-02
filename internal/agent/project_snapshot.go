package agent

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"specific-harness/internal/pathutil"
)

// ProjectSnapshot records project contents under the same scope rules as
// projectFingerprint so a frozen check episode's own writes (screenshots,
// coverage, generated reports) can be undone exactly afterward. It is not
// a sandbox: effects outside the project inventory are not captured.
type ProjectSnapshot struct {
	root     string
	excludes []string
	files    map[string]snapshotFile
	dirs     map[string]bool
}

type snapshotFile struct {
	mode os.FileMode
	data []byte
	link string
}

func captureProjectSnapshot(ctx context.Context, root string, excludes []string) (*ProjectSnapshot, error) {
	resolved, names, excluded, err := projectInventory(ctx, root, excludes)
	if err != nil {
		return nil, err
	}
	snap := &ProjectSnapshot{root: resolved, excludes: excludes, files: map[string]snapshotFile{}, dirs: map[string]bool{}}
	// Record explicit directories (including empty ones) so restore can
	// prune only directories the check created.
	_ = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, e error) error {
		if e != nil || !d.IsDir() {
			return nil
		}
		if excluded(path) || d.Name() == ".git" {
			return filepath.SkipDir
		}
		if rel, e := filepath.Rel(resolved, path); e == nil && rel != "." {
			snap.dirs[rel] = true
		}
		return nil
	})
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(resolved, name)
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
				return nil, e
			}
			snap.files[name] = snapshotFile{mode: info.Mode(), link: target}
			continue
		}
		if info.IsDir() {
			// Git submodule inventory entry: capture its files directly.
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
				if e != nil || d.IsDir() || excluded(p) {
					return e
				}
				data, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				rel, e := filepath.Rel(resolved, p)
				if e != nil {
					return e
				}
				fi, e := d.Info()
				if e != nil {
					return e
				}
				snap.files[rel] = snapshotFile{mode: fi.Mode(), data: data}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		snap.files[name] = snapshotFile{mode: info.Mode(), data: data}
	}
	return snap, nil
}

// Restore returns the project to the captured contents and reports the
// sorted relative paths that had been created, modified, or deleted.
func (s *ProjectSnapshot) Restore(ctx context.Context) ([]string, error) {
	_, current, excluded, err := projectInventory(ctx, s.root, s.excludes)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for _, name := range current {
		path := filepath.Join(s.root, name)
		if excluded(path) {
			continue
		}
		if _, ok := s.files[name]; !ok {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return changed, err
			}
			changed = append(changed, name)
		}
	}
	for name, f := range s.files {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		path := filepath.Join(s.root, name)
		if f.link != "" {
			target, e := os.Readlink(path)
			if e == nil && target == f.link {
				continue
			}
			_ = os.Remove(path)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return changed, err
			}
			if err := os.Symlink(f.link, path); err != nil {
				return changed, err
			}
			changed = append(changed, name)
			continue
		}
		data, e := os.ReadFile(path)
		if e == nil && bytes.Equal(data, f.data) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return changed, err
		}
		if err := os.WriteFile(path, f.data, f.mode.Perm()); err != nil {
			return changed, err
		}
		_ = os.Chmod(path, f.mode.Perm())
		changed = append(changed, name)
	}
	// Prune directories the episode created (deepest first; only empty
	// ones can be removed, which is exactly the artifact-dir case).
	var dirs []string
	_ = filepath.WalkDir(s.root, func(path string, d fs.DirEntry, e error) error {
		if e == nil && d.IsDir() {
			if excluded(path) || d.Name() == ".git" {
				return filepath.SkipDir
			}
			if rel, e := filepath.Rel(s.root, path); e == nil && rel != "." && !s.dirs[rel] {
				dirs = append(dirs, path)
			}
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		_ = os.Remove(dir)
	}
	sort.Strings(changed)
	return changed, nil
}
