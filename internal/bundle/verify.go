package bundle

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// ManifestFile is the bundle's SHA-256 inventory, written at build time by
// python/<platform>/write_manifest.py as "<sha256>  ./<posix path>" lines.
const ManifestFile = "MANIFEST.sha256"

// Failure kinds reported by VerifyManifest.
const (
	FailureMissing = "missing"
	FailureChanged = "changed"
)

// Failure is one inventoried file that is absent or no longer matches the
// hash recorded when the bundle was built. Path is bundle-relative and
// slash-separated, exactly as written in the manifest.
type Failure struct {
	Path string
	Kind string
}

func (f Failure) String() string { return f.Path + " (" + f.Kind + ")" }

// VerifyManifest hashes every file listed in the bundle inventory and
// reports the ones that are missing or changed. A missing or malformed
// inventory is an error: without it there is no chain to verify against.
func VerifyManifest(root string) (checked int, failures []Failure, err error) {
	return VerifyManifestProgress(root, nil)
}

// VerifyManifestProgress is VerifyManifest with progress reporting. Files
// are hashed by a small worker pool: a complete bundle inventories tens of
// thousands of mostly small files, and hashing them strictly one at a time
// makes every launch wait on single-file latency. Failures keep manifest
// order, and progress (when set) is called once per finished file with a
// strictly increasing done count, never concurrently.
func VerifyManifestProgress(root string, progress func(done, total int)) (checked int, failures []Failure, err error) {
	manifest := filepath.Join(root, ManifestFile)
	file, err := os.Open(manifest)
	if err != nil {
		return 0, nil, fmt.Errorf("install inventory missing: %s; replace the complete Caliper folder", manifest)
	}
	defer file.Close()
	var entries []manifestEntry
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			entry, err := parseManifestLine(line)
			if err != nil {
				return 0, nil, err
			}
			entries = append(entries, entry)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, nil, fmt.Errorf("cannot read install inventory %s: %w", manifest, readErr)
		}
	}
	if len(entries) == 0 {
		return 0, nil, fmt.Errorf("install inventory is empty: %s; replace the complete Caliper folder", manifest)
	}
	base, err := resolvedBase(root)
	if err != nil {
		return 0, nil, err
	}
	outcomes := make([]manifestOutcome, len(entries))
	jobs := make(chan int)
	var wait sync.WaitGroup
	var progressMu sync.Mutex
	done := 0
	for range min(runtime.NumCPU(), 8) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				failure, failed, err := entries[index].check(root, base)
				outcomes[index] = manifestOutcome{failure, failed, err}
				if progress != nil {
					progressMu.Lock()
					done++
					progress(done, len(entries))
					progressMu.Unlock()
				}
			}
		}()
	}
	for index := range entries {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	for _, outcome := range outcomes {
		if outcome.err != nil {
			return 0, nil, outcome.err
		}
		if outcome.failed {
			failures = append(failures, outcome.failure)
		}
	}
	return len(entries), failures, nil
}

type manifestEntry struct {
	want, rel string
}

type manifestOutcome struct {
	failure Failure
	failed  bool
	err     error
}

func parseManifestLine(line string) (manifestEntry, error) {
	// Paths may contain spaces, so split on the fixed hash width, not fields.
	if len(line) < 67 || line[64] != ' ' || line[65] != ' ' {
		return manifestEntry{}, fmt.Errorf("malformed install inventory line: %q", line)
	}
	want, listed := line[:64], line[66:]
	if _, err := hex.DecodeString(want); err != nil {
		return manifestEntry{}, fmt.Errorf("malformed SHA-256 in install inventory line: %q", line)
	}
	rel, err := manifestRel(listed)
	if err != nil {
		return manifestEntry{}, err
	}
	return manifestEntry{want: want, rel: rel}, nil
}

func (entry manifestEntry) check(root, base string) (Failure, bool, error) {
	name, err := containedIn(root, base, entry.rel)
	if err != nil {
		return Failure{}, false, err
	}
	sum, err := hashFile(name)
	if os.IsNotExist(err) {
		return Failure{Path: entry.rel, Kind: FailureMissing}, true, nil
	}
	if err != nil {
		return Failure{}, false, err
	}
	if !strings.EqualFold(sum, entry.want) {
		return Failure{Path: entry.rel, Kind: FailureChanged}, true, nil
	}
	return Failure{}, false, nil
}

func verifyManifestLine(root, line string, failures *[]Failure) error {
	entry, err := parseManifestLine(line)
	if err != nil {
		return err
	}
	base, err := resolvedBase(root)
	if err != nil {
		return err
	}
	failure, failed, err := entry.check(root, base)
	if err != nil {
		return err
	}
	if failed {
		*failures = append(*failures, failure)
	}
	return nil
}

// manifestRel converts a "./runtime/decision/python.exe" inventory path to a
// clean bundle-relative slash path, refusing anything that escapes the root.
func manifestRel(listed string) (string, error) {
	if !strings.HasPrefix(listed, "./") {
		return "", fmt.Errorf("install inventory path must be bundle-relative: %q", listed)
	}
	if strings.ContainsAny(listed, "\\:\x00") {
		return "", fmt.Errorf("invalid install inventory path: %q", listed)
	}
	rel := path.Clean(strings.TrimPrefix(listed, "./"))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return "", fmt.Errorf("install inventory path escapes the bundle: %q", listed)
	}
	return rel, nil
}

// Reuse scratch space across files: allocating a MiB for each of tens of
// thousands of inventory entries makes startup spend time collecting garbage.
var hashBuffers = sync.Pool{New: func() any { b := make([]byte, 1024*1024); return &b }}

func hashFile(name string) (string, error) {
	info, err := os.Stat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("inventoried path is not a regular file: %s", name)
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := hashBuffers.Get().(*[]byte)
	defer hashBuffers.Put(buffer)
	if _, err := io.CopyBuffer(hash, file, *buffer); err != nil {
		return "", fmt.Errorf("cannot hash installed file %s: %w", name, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// anchorFailures are inventory entries whose bytes are the trust root for a
// download repair. If one of these changed, the pinned sources can no longer
// be believed and the folder must be replaced by hand.
func isAnchor(rel string) bool {
	switch rel {
	case "runtime/sources.json", "runtime/manifest.json":
		return true
	}
	return strings.HasSuffix(rel, "/requirements.lock")
}

// ClassifyFailures splits verification failures into the downloadable
// dependencies Repair can restore (the private Python runtimes and bundled
// ripgrep) and everything else, which only a fresh bundle can replace.
func ClassifyFailures(failures []Failure) (repairable, unrepairable []Failure) {
	for _, failure := range failures {
		switch {
		case isAnchor(failure.Path):
			unrepairable = append(unrepairable, failure)
		case repairableProfile(failure.Path) != "":
			repairable = append(repairable, failure)
		case failure.Path == "bin/"+rgFileName():
			repairable = append(repairable, failure)
		default:
			unrepairable = append(unrepairable, failure)
		}
	}
	return repairable, unrepairable
}

// replaceFile uses a unique sibling backup on platforms that cannot replace an
// existing destination. Never remove a preexisting .old file or lose rollback.
func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if _, err := os.Lstat(dst); err != nil {
		return err
	}
	holder, err := os.MkdirTemp(filepath.Dir(dst), ".replace-")
	if err != nil {
		return err
	}
	aside := filepath.Join(holder, "previous")
	if err := os.Rename(dst, aside); err != nil {
		os.Remove(holder)
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		if restore := os.Rename(aside, dst); restore != nil {
			return fmt.Errorf("replace failed: %v; previous file preserved at %s; restore failed: %w", err, aside, restore)
		}
		os.Remove(holder)
		return err
	}
	return os.RemoveAll(holder)
}

func repairableProfile(rel string) string {
	for _, profile := range requiredProfiles() {
		if strings.HasPrefix(rel, "runtime/"+profile+"/") {
			return profile
		}
	}
	return ""
}

func resolvedBase(root string) (string, error) {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(base)
}

func containedPath(root, rel string) (string, error) {
	base, err := resolvedBase(root)
	if err != nil {
		return "", err
	}
	return containedIn(root, base, rel)
}

// Resolve existing parents as well as the leaf: lexical checks alone do not
// prevent a link inside the application from pointing at user files outside it.
func containedIn(root, base, rel string) (string, error) {
	target := filepath.Join(base, filepath.FromSlash(rel))
	probe := target
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			within, err := filepath.Rel(base, resolved)
			if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("installed path escapes application folder: %s", rel)
			}
			return filepath.Join(root, filepath.FromSlash(rel)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}
		probe = parent
	}
}

// Repair may re-inventory only the dependency trees it rebuilt. Keep all other
// release digests unchanged; do not bless unrelated edits or inventory user data
// that happens to live beside the app.
func writeRepairedInventory(root string, original []byte, profiles map[string]bool, ripgrep bool) error {
	entries := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(original)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if len(line) < 67 {
			return fmt.Errorf("malformed original inventory")
		}
		rel, err := manifestRel(line[66:])
		if err != nil {
			return err
		}
		if profiles[repairableProfile(rel)] || (ripgrep && rel == "bin/"+rgFileName()) || (len(profiles) > 0 && rel == Marker) {
			continue
		}
		var changed []Failure
		if err := verifyManifestLine(root, line, &changed); err != nil {
			return err
		}
		if len(changed) > 0 {
			return fmt.Errorf("unrepaired file changed during repair: %s", rel)
		}
		entries[rel] = line[:64]
	}
	add := func(rel string) error {
		name, err := containedPath(root, rel)
		if err != nil {
			return err
		}
		sum, err := hashFile(name)
		if err == nil {
			entries[rel] = sum
		}
		return err
	}
	for profile := range profiles {
		dir := filepath.Join(root, "runtime", profile)
		if err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			return add(filepath.ToSlash(rel))
		}); err != nil {
			return err
		}
	}
	if len(profiles) > 0 {
		if err := add(Marker); err != nil {
			return err
		}
	}
	if ripgrep {
		if err := add("bin/" + rgFileName()); err != nil {
			return err
		}
	}
	var paths []string
	for rel := range entries {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	var data strings.Builder
	for _, rel := range paths {
		fmt.Fprintf(&data, "%s  ./%s\n", entries[rel], rel)
	}
	return os.WriteFile(filepath.Join(root, ManifestFile), []byte(data.String()), 0644)
}
