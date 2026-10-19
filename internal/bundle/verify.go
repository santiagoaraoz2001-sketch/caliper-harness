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
	"sort"
	"strings"
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
	manifest := filepath.Join(root, ManifestFile)
	file, err := os.Open(manifest)
	if err != nil {
		return 0, nil, fmt.Errorf("install inventory missing: %s; replace the complete Caliper folder", manifest)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			checked++
			if err := verifyManifestLine(root, line, &failures); err != nil {
				return 0, nil, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, nil, fmt.Errorf("cannot read install inventory %s: %w", manifest, readErr)
		}
	}
	if checked == 0 {
		return 0, nil, fmt.Errorf("install inventory is empty: %s; replace the complete Caliper folder", manifest)
	}
	return checked, failures, nil
}

func verifyManifestLine(root, line string, failures *[]Failure) error {
	// Paths may contain spaces, so split on the fixed hash width, not fields.
	if len(line) < 67 || line[64] != ' ' || line[65] != ' ' {
		return fmt.Errorf("malformed install inventory line: %q", line)
	}
	want, listed := line[:64], line[66:]
	if _, err := hex.DecodeString(want); err != nil {
		return fmt.Errorf("malformed SHA-256 in install inventory line: %q", line)
	}
	rel, err := manifestRel(listed)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	sum, err := hashFile(path)
	if os.IsNotExist(err) {
		*failures = append(*failures, Failure{Path: rel, Kind: FailureMissing})
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, want) {
		*failures = append(*failures, Failure{Path: rel, Kind: FailureChanged})
	}
	return nil
}

// manifestRel converts a "./runtime/decision/python.exe" inventory path to a
// clean bundle-relative slash path, refusing anything that escapes the root.
func manifestRel(listed string) (string, error) {
	if !strings.HasPrefix(listed, "./") {
		return "", fmt.Errorf("install inventory path must be bundle-relative: %q", listed)
	}
	rel := path.Clean(strings.TrimPrefix(listed, "./"))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return "", fmt.Errorf("install inventory path escapes the bundle: %q", listed)
	}
	return rel, nil
}

func hashFile(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, file, make([]byte, 1024*1024)); err != nil {
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
		case strings.HasPrefix(failure.Path, "runtime/"):
			repairable = append(repairable, failure)
		case failure.Path == "bin/"+rgFileName():
			repairable = append(repairable, failure)
		default:
			unrepairable = append(unrepairable, failure)
		}
	}
	return repairable, unrepairable
}

// WriteManifest regenerates the bundle inventory over the current tree, in
// the same format the builder writes. Repair calls it only after every
// restored file came from the pinned, hash-checked sources and the runtime
// passed its import verification, so the new inventory describes a verified
// installation. Staging directories from builds and repairs are excluded.
func WriteManifest(root string) error {
	var rels []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if entry.IsDir() {
			if slash == ".runtime-build" || slash == ".runtime-repair" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || slash == ManifestFile || slash == ".MANIFEST.sha256.tmp" {
			return nil
		}
		rels = append(rels, slash)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(rels)
	pending := filepath.Join(root, ".MANIFEST.sha256.tmp")
	out, err := os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(out)
	for _, rel := range rels {
		sum, err := hashFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			out.Close()
			os.Remove(pending)
			return err
		}
		fmt.Fprintf(writer, "%s  ./%s\n", sum, rel)
	}
	if err := writer.Flush(); err != nil {
		out.Close()
		os.Remove(pending)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(pending)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(pending)
		return err
	}
	return replaceFile(pending, filepath.Join(root, ManifestFile))
}

// replaceFile renames src over dst on platforms where a bare rename refuses
// to overwrite an existing file.
func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	aside := dst + ".old"
	os.Remove(aside)
	if err := os.Rename(dst, aside); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		os.Rename(aside, dst)
		return err
	}
	os.Remove(aside)
	return nil
}
