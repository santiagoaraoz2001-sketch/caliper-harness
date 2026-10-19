package bundle

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Reporter receives one human-readable progress line per repair step. It
// may be nil.
type Reporter func(string)

type pinnedSource struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

type runtimeSources struct {
	Platform string       `json:"platform"`
	Python   pinnedSource `json:"python"`
	Ripgrep  pinnedSource `json:"ripgrep"`
}

// loadSources reads the pinned dependency sources the builder copied into
// runtime/sources.json. Callers must have verified that file against the
// install inventory before trusting what it names.
func loadSources(root string) (runtimeSources, error) {
	var sources runtimeSources
	raw, err := os.ReadFile(filepath.Join(root, "runtime", "sources.json"))
	if err != nil {
		return sources, fmt.Errorf("pinned dependency sources missing; replace the complete Caliper folder: %w", err)
	}
	if err := json.Unmarshal(raw, &sources); err != nil {
		return sources, fmt.Errorf("pinned dependency sources unreadable; replace the complete Caliper folder: %w", err)
	}
	if platform() == "" || sources.Platform != platform() {
		return sources, fmt.Errorf("pinned dependency sources are for %q, not this platform; replace the complete Caliper folder", sources.Platform)
	}
	for _, source := range []pinnedSource{sources.Python, sources.Ripgrep} {
		if !strings.HasPrefix(source.URL, "https://") {
			return sources, fmt.Errorf("pinned dependency source is not HTTPS: %q", source.URL)
		}
		if decoded, err := hex.DecodeString(source.SHA256); err != nil || len(decoded) != sha256.Size {
			return sources, fmt.Errorf("pinned dependency source has an invalid SHA-256: %q", source.URL)
		}
	}
	return sources, nil
}

// downloadVerified streams url to dst while hashing it, and refuses the
// file unless it matches the pinned digest. dst is removed on failure.
func downloadVerified(client *http.Client, url, sha256Hex, dst string) error {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "caliper-harness-repair")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s returned %s", url, response.Status)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.CopyBuffer(io.MultiWriter(out, hash), response.Body, make([]byte, 1024*1024))
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(dst)
		return fmt.Errorf("download failed: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(dst)
		return closeErr
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, sha256Hex) {
		os.Remove(dst)
		return fmt.Errorf("SHA-256 mismatch for %s: got %s, want %s", url, got, strings.ToLower(sha256Hex))
	}
	return nil
}

// installRipgrep extracts the rg binary from a downloaded release archive
// (.zip on Windows, .tar.gz elsewhere) and installs it as bin/<rg>.
func installRipgrep(root, archive, archiveURL string) error {
	staged := filepath.Join(filepath.Dir(archive), "rg-staged")
	parsed, err := url.Parse(archiveURL)
	if err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(parsed.Path, ".zip"):
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return fmt.Errorf("cannot open ripgrep archive: %w", err)
		}
		defer reader.Close()
		found := false
		for _, entry := range reader.File {
			if path.Base(entry.Name) != rgFileName() || !entry.FileInfo().Mode().IsRegular() {
				continue
			}
			source, err := entry.Open()
			if err != nil {
				return err
			}
			err = writeStream(staged, source, 0755)
			source.Close()
			if err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("%s missing from ripgrep archive", rgFileName())
		}
	case strings.HasSuffix(parsed.Path, ".tar.gz"):
		found, err := extractTarFile(archive, rgFileName(), staged)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%s missing from ripgrep archive", rgFileName())
		}
	default:
		return fmt.Errorf("unsupported ripgrep archive: %s", archiveURL)
	}
	target := filepath.Join(root, "bin", rgFileName())
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return replaceFile(staged, target)
}

func writeStream(dst string, source io.Reader, perm os.FileMode) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(out, source, make([]byte, 1024*1024)); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// extractPython unpacks a python-build-standalone tar.gz (top-level
// "python/" directory) into dst, so dst itself becomes the interpreter
// root. Entries that would escape dst are refused.
func extractPython(archive, dst string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("cannot open Python archive: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("cannot read Python archive: %w", err)
		}
		if strings.ContainsAny(header.Name, "\\:\x00") || (header.Name != "python" && !strings.HasPrefix(header.Name, "python/")) {
			return fmt.Errorf("invalid Python archive path: %q", header.Name)
		}
		rel := path.Clean(strings.TrimPrefix(header.Name, "python/"))
		if rel == "." || rel == "python" {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
			return fmt.Errorf("Python archive entry escapes its folder: %q", header.Name)
		}
		target, err := containedPath(dst, rel)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			perm := os.FileMode(header.Mode).Perm()
			if perm == 0 {
				perm = 0644
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := writeStream(target, reader, perm); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Unix standalone builds link python -> python3.x. Validate link
			// destinations on every OS; never silently omit an archive entry.
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			link := header.Linkname
			if strings.ContainsAny(link, "\\:\x00") || path.IsAbs(link) {
				return fmt.Errorf("Python archive link escapes its folder: %q", link)
			}
			resolved := path.Clean(path.Join(path.Dir(rel), link))
			if header.Typeflag == tar.TypeLink {
				if !strings.HasPrefix(link, "python/") {
					return fmt.Errorf("invalid Python archive hard link: %q", link)
				}
				resolved = path.Clean(strings.TrimPrefix(link, "python/"))
			}
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return fmt.Errorf("Python archive link escapes its folder: %q", link)
			}
			linked, err := containedPath(dst, resolved)
			if err != nil {
				return err
			}
			if header.Typeflag == tar.TypeLink {
				err = os.Link(linked, target)
			} else {
				err = os.Symlink(link, target)
			}
			if err != nil {
				return err
			}
		}
	}
}

// interpreterIn returns the Python executable inside an interpreter root
// (a runtime profile folder, staged or installed).
func interpreterIn(profileRoot string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(profileRoot, "python.exe")
	}
	return filepath.Join(profileRoot, "bin", "python3")
}

// runCaptured runs a command and folds the tail of its output into any error.
func runCaptured(command *exec.Cmd) error {
	output, err := command.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(output))
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return fmt.Errorf("%s failed: %w\n%s", filepath.Base(command.Path), err, tail)
	}
	return nil
}

// syncProfilePackages installs the hash-locked requirements into a staged
// interpreter, exactly as the builder does: uv pip sync when uv is
// available, otherwise the interpreter's own pip in hash-checking mode.
func syncProfilePackages(interpreter, lock, stage string) error {
	if uv, err := exec.LookPath("uv"); err == nil {
		args := []string{"--no-config", "pip", "sync", "--python", interpreter}
		if runtime.GOOS == "darwin" {
			args = append(args, "--python-platform", "aarch64-apple-darwin")
		}
		args = append(args, "--system", "--break-system-packages", "--require-hashes", "--only-binary", ":all:", "--link-mode", "copy", lock)
		command := exec.Command(uv, args...)
		command.Env = append(PythonEnv(os.Environ(), nil), "UV_CACHE_DIR="+filepath.Join(stage, "uv-cache"))
		if runtime.GOOS == "darwin" {
			command.Env = append(command.Env, "MACOSX_DEPLOYMENT_TARGET=14.0")
		}
		return runCaptured(command)
	}
	ensure := exec.Command(interpreter, "-m", "ensurepip", "--default-pip")
	ensure.Env = PythonEnv(os.Environ(), nil)
	_ = ensure.Run() // pip is already present in supported standalone builds.
	command := exec.Command(interpreter, "-m", "pip", "install", "--no-input", "--break-system-packages", "--require-hashes", "--only-binary", ":all:", "-r", lock)
	command.Env = append(PythonEnv(os.Environ(), nil), "PIP_DISABLE_PIP_VERSION_CHECK=1")
	if err := runCaptured(command); err != nil {
		return fmt.Errorf("%w (install uv for the builder-identical repair path, or replace the complete Caliper folder)", err)
	}
	return nil
}

// extractTarFile writes the first regular tar entry whose base name matches
// to dst.
func extractTarFile(archive, base, dst string) (bool, error) {
	file, err := os.Open(archive)
	if err != nil {
		return false, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return false, fmt.Errorf("cannot open gzip archive %s: %w", archive, err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("cannot read tar archive %s: %w", archive, err)
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == base {
			return true, writeStream(dst, reader, 0755)
		}
	}
}

// Repair re-downloads the pinned dependencies behind every repairable
// failure and restores them: bundled ripgrep and each damaged private
// Python profile (fresh standalone interpreter plus its hash-locked
// packages). Every archive is hash-checked against runtime/sources.json,
// which the caller must have verified against the install inventory first.
// On success the runtime manifest is regenerated, the structural check
// passes, and a fresh inventory describes the verified tree.
func Repair(root string, failures []Failure, note Reporter) (result error) {
	if note == nil {
		note = func(string) {}
	}
	if _, unrepairable := ClassifyFailures(failures); len(unrepairable) > 0 {
		return fmt.Errorf("%s cannot be downloaded; replace the complete Caliper folder", unrepairable[0].Path)
	}
	// Recheck at the mutation boundary, including every trust anchor and script.
	// The caller's earlier inventory result may no longer describe these bytes.
	_, current, err := VerifyManifest(root)
	if err != nil {
		return err
	}
	if _, broken := ClassifyFailures(current); len(broken) > 0 {
		return fmt.Errorf("repair source changed: %s; replace the complete Caliper folder", broken[0])
	}
	failures = current
	if len(failures) == 0 {
		return nil
	}
	originalInventory, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if err != nil {
		return err
	}
	if err := repairAnchorsInventoried(originalInventory); err != nil {
		return err
	}
	sources, err := loadSources(root)
	if err != nil {
		return err
	}
	needRipgrep := false
	profiles := map[string]bool{}
	for _, failure := range failures {
		if failure.Path == "bin/"+rgFileName() {
			needRipgrep = true
			continue
		}
		for _, profile := range requiredProfiles() {
			if strings.HasPrefix(failure.Path, "runtime/"+profile+"/") {
				profiles[profile] = true
			}
		}
	}
	stage, err := os.MkdirTemp(root, ".runtime-repair-")
	if err != nil {
		return err
	}
	tx := &repairTransaction{stage: stage}
	defer func() {
		if result != nil {
			if err := tx.rollback(); err != nil {
				result = errors.Join(result, err)
				return
			}
		}
		os.RemoveAll(stage)
	}()
	client := &http.Client{Timeout: 30 * time.Minute}

	if needRipgrep {
		note(fmt.Sprintf("Downloading ripgrep %s", sources.Ripgrep.Version))
		archive := filepath.Join(stage, "ripgrep-archive")
		if err := downloadVerified(client, sources.Ripgrep.URL, sources.Ripgrep.SHA256, archive); err != nil {
			return err
		}
		freshRoot := filepath.Join(stage, "ripgrep")
		if err := installRipgrep(freshRoot, archive, sources.Ripgrep.URL); err != nil {
			return err
		}
		note("ripgrep download verified")
	}

	if len(profiles) > 0 {
		note(fmt.Sprintf("Downloading Python %s", sources.Python.Version))
		archive := filepath.Join(stage, "python.tar.gz")
		if err := downloadVerified(client, sources.Python.URL, sources.Python.SHA256, archive); err != nil {
			return err
		}
		// Canonical profile order keeps the report (and error surface)
		// stable across runs.
		for _, profile := range requiredProfiles() {
			if !profiles[profile] {
				continue
			}
			if err := repairProfile(root, stage, archive, profile, note); err != nil {
				return err
			}
		}
	}
	// Prepare every requested dependency before changing the installed tree.
	if needRipgrep {
		if err := tx.activate(filepath.Join(stage, "ripgrep", "bin", rgFileName()), filepath.Join(root, "bin", rgFileName())); err != nil {
			return err
		}
	}
	for _, profile := range requiredProfiles() {
		if profiles[profile] {
			if err := tx.activate(filepath.Join(stage, "new-"+profile), filepath.Join(root, "runtime", profile)); err != nil {
				return err
			}
		}
	}
	if len(profiles) > 0 {
		if err := tx.backup(filepath.Join(root, Marker)); err != nil {
			return err
		}
		if err := regenerateManifest(root); err != nil {
			return err
		}
	}
	if err := Check(root); err != nil {
		return fmt.Errorf("repair did not restore a complete bundle: %w", err)
	}
	// Everything restored came from hash-checked pinned sources and passed
	// the runtime import verification above, so re-inventory the tree as
	// the new verified state for later startups.
	if err := tx.backup(filepath.Join(root, ManifestFile)); err != nil {
		return err
	}
	if err := writeRepairedInventory(root, originalInventory, profiles, needRipgrep); err != nil {
		return fmt.Errorf("cannot record the repaired installation: %w", err)
	}
	if _, remaining, err := VerifyManifest(root); err != nil || len(remaining) > 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("repair left %d files unverified, first: %s", len(remaining), remaining[0])
	}
	note("Installation repaired and verified")
	return nil
}

// repairProfile rebuilds one runtime/<profile> interpreter in staging and
// leaves activation to the transaction after every profile is prepared.
func repairProfile(root, stage, pythonArchive, profile string, note Reporter) error {
	installed := filepath.Join(root, "runtime", profile)
	lock := filepath.Join(installed, "requirements.lock")
	lockBytes, err := os.ReadFile(lock)
	if err != nil {
		return fmt.Errorf("%s requirements lock is unavailable; replace the complete Caliper folder: %w", profile, err)
	}
	fresh := filepath.Join(stage, "new-"+profile)
	if err := os.MkdirAll(fresh, 0755); err != nil {
		return err
	}
	note(fmt.Sprintf("Rebuilding the %s Python runtime", profile))
	if err := extractPython(pythonArchive, fresh); err != nil {
		return err
	}
	interpreter := interpreterIn(fresh)
	if _, err := os.Stat(interpreter); err != nil {
		return fmt.Errorf("standalone Python missing after download: %s", interpreter)
	}
	note(fmt.Sprintf("Downloading the %s Python packages", profile))
	if err := syncProfilePackages(interpreter, lock, stage); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := normalizeWindowsRuntime(root, fresh, pythonArchive); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(fresh, "requirements.lock"), lockBytes, 0644); err != nil {
		return err
	}
	note(fmt.Sprintf("%s Python runtime prepared", profile))
	return nil
}

// Reuse the builder's verified normalizer after downloading the private runtime.
// It repairs mangled PE imports before removing vendored CRT, in staging only.
func normalizeWindowsRuntime(root, fresh, pythonArchive string) error {
	// The target interpreter loads its own CRT DLLs, preventing their deletion on
	// Windows. Run from a separate copy of the same already-verified archive; the
	// installed interpreter may be broken and is not a safe repair dependency.
	runner, err := os.MkdirTemp(filepath.Dir(fresh), ".normalize-python-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(runner)
	if err := extractPython(pythonArchive, runner); err != nil {
		return fmt.Errorf("Windows system-runtime normalization failed; cannot prepare isolated interpreter: %w", err)
	}
	cmd := exec.Command(interpreterIn(runner), "-I", filepath.Join(root, "python", "windows", "use_system_runtime.py"), fresh)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Windows system-runtime normalization failed; installed runtime unchanged: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// regenerateManifest reruns the bundled runtime verifier with the repaired
// interpreters and republishes runtime/manifest.json from its report.
func regenerateManifest(root string) error {
	directory := runtime.GOOS
	if directory == "darwin" {
		directory = "macos"
	}
	script := filepath.Join(root, "python", directory, "verify_runtime.py")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("bundled runtime verifier missing: %s; replace the complete Caliper folder", script)
	}
	profile := "decision"
	if runtime.GOOS == "darwin" {
		profile = "worker"
	}
	command := exec.Command(Python(root, profile), "-I", script, root)
	command.Env = PythonEnv(os.Environ(), nil)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		detail := ""
		if errors.As(err, &exit) {
			detail = strings.TrimSpace(string(exit.Stderr))
		}
		return fmt.Errorf("repaired runtime failed verification: %s failed: %w\n%s", filepath.Base(command.Path), err, detail)
	}
	pending, err := os.CreateTemp(filepath.Dir(filepath.Join(root, Marker)), ".manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(pending.Name())
	if _, err := pending.Write(output); err != nil {
		pending.Close()
		return err
	}
	if err := pending.Sync(); err != nil {
		pending.Close()
		return err
	}
	if err := pending.Close(); err != nil {
		return err
	}
	return replaceFile(pending.Name(), filepath.Join(root, Marker))
}

// Keep the previous dependency until the complete repaired tree is verified.
// A failed import, inventory write, or later profile swap restores all earlier
// swaps, instead of deleting the only usable copy during a partial repair.
type repairTransaction struct {
	stage    string
	previous []struct{ target, backup string }
}

func (tx *repairTransaction) backup(target string) error {
	backup := filepath.Join(tx.stage, fmt.Sprintf("previous-%d", len(tx.previous)))
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		backup = ""
	} else if err != nil {
		return err
	} else if err := os.Rename(target, backup); err != nil {
		return err
	}
	tx.previous = append(tx.previous, struct{ target, backup string }{target, backup})
	return nil
}
func (tx *repairTransaction) activate(fresh, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if err := tx.backup(target); err != nil {
		return err
	}
	return os.Rename(fresh, target)
}
func (tx *repairTransaction) rollback() error {
	var failures []error
	for i := len(tx.previous) - 1; i >= 0; i-- {
		item := tx.previous[i]
		if err := os.RemoveAll(item.target); err != nil {
			failures = append(failures, err)
			continue
		}
		if item.backup != "" {
			if err := os.Rename(item.backup, item.target); err != nil {
				failures = append(failures, fmt.Errorf("restore %s from preserved %s: %w", item.target, item.backup, err))
			}
		}
	}
	return errors.Join(failures...)
}

func repairAnchorsInventoried(inventory []byte) error {
	entries := map[string]bool{}
	for _, line := range strings.Split(string(inventory), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if len(line) >= 67 {
			rel, err := manifestRel(line[66:])
			if err != nil {
				return err
			}
			entries[rel] = true
		}
	}
	directory := runtime.GOOS
	if directory == "darwin" {
		directory = "macos"
	}
	anchors := []string{"runtime/sources.json", Marker, "python/" + directory + "/verify_runtime.py"}
	if runtime.GOOS == "windows" {
		anchors = append(anchors, "ensure-windows-runtime.ps1", "python/windows/use_system_runtime.py")
	}
	for _, profile := range requiredProfiles() {
		anchors = append(anchors, "runtime/"+profile+"/requirements.lock")
	}
	for _, rel := range anchors {
		if !entries[rel] {
			return fmt.Errorf("repair trust anchor %s is not inventoried; replace the complete Caliper folder", rel)
		}
	}
	return nil
}
