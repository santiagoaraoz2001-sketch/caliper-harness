// Package update installs immutable application folders. It never changes user
// configuration, project files, models, sessions or the running executable.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"specific-harness/internal/bundle"
	"specific-harness/internal/config"
)

const Repository = "santiagoaraoz2001-sketch/caliper-harness"
const Version = "0.1.6"

type Release struct{ Version, ArchiveURL, ChecksumURL, PartsURL string }
type Selection struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Root     string `json:"root"`
}
type Manager struct {
	Home, Platform, API string
	Client              *http.Client
}

func Default() (Manager, error) {
	home, err := config.UserDataDir()
	return Manager{Home: home, Platform: runtime.GOOS + "-" + runtime.GOARCH, API: "https://api.github.com/repos/" + Repository + "/releases/latest", Client: http.DefaultClient}, err
}
func validVersion(s string) bool { _, ok := versionParts(s); return ok }
func versionParts(s string) ([3]int, bool) {
	var out [3]int
	p := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(p) != 3 {
		return out, false
	}
	for i, v := range p {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 || strconv.Itoa(n) != v {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
func Newer(a, b string) bool {
	x, ok := versionParts(a)
	y, ok2 := versionParts(b)
	if !ok || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}
func (m Manager) get(ctx context.Context, url string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "caliper/"+Version)
	c := m.Client
	if c == nil {
		c = http.DefaultClient
	}
	r, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", url, r.StatusCode)
	}
	return r, nil
}
func (m Manager) Check(ctx context.Context, current string) (*Release, error) {
	r, e := m.get(ctx, m.API)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	var data struct {
		Tag               string `json:"tag_name"`
		Draft, Prerelease bool
		Assets            []struct {
			Name string
			URL  string `json:"browser_download_url"`
		}
	}
	if e = json.NewDecoder(r.Body).Decode(&data); e != nil {
		return nil, e
	}
	v := strings.TrimPrefix(data.Tag, "v")
	if !validVersion(v) {
		return nil, fmt.Errorf("release has invalid version %q", data.Tag)
	}
	if data.Draft || data.Prerelease || !Newer(v, current) {
		return nil, nil
	}
	name := "caliper-" + v + "-" + m.Platform + ".tar.gz"
	out := &Release{Version: v}
	for _, a := range data.Assets {
		if a.Name == name {
			out.ArchiveURL = a.URL
		}
		if a.Name == name+".parts.json" {
			out.PartsURL = a.URL
		}
		if a.Name == name+".sha256" {
			out.ChecksumURL = a.URL
		}
	}
	if (out.ArchiveURL == "" && out.PartsURL == "") || out.ChecksumURL == "" {
		return nil, fmt.Errorf("Caliper %s has no complete %s release; current installation retained", v, m.Platform)
	}
	return out, nil
}
func executable(root, platform string) string {
	if strings.HasPrefix(platform, "windows-") {
		return filepath.Join(root, "caliper.exe")
	}
	return filepath.Join(root, "caliper-harness")
}
func (m Manager) Selected() (*Selection, error) {
	raw, e := os.ReadFile(filepath.Join(m.Home, "current.json"))
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var s Selection
	if e = json.Unmarshal(raw, &s); e != nil {
		return nil, e
	}
	expected := filepath.Join(m.Home, "versions", s.Version, m.Platform, "Caliper")
	if !validVersion(s.Version) || s.Platform != m.Platform || filepath.Clean(s.Root) != expected {
		return nil, fmt.Errorf("invalid Caliper version selection")
	}
	if e = validate(s.Root, s.Platform, true); e != nil {
		return nil, e
	}
	return &s, nil
}

// Legacy installed versions retain their inventoried app-local CRT. New releases
// must carry the system-runtime helper instead; never rewrite old version files.
func legacyWindowsRuntime(root string) bool {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	inventory, err := os.ReadFile(filepath.Join(root, "MANIFEST.sha256"))
	if err != nil {
		return false
	}
	lines := "\n" + strings.TrimSuffix(strings.ReplaceAll(string(inventory), "\r\n", "\n"), "\n") + "\n"
	for _, name := range []string{"msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll", "runtime/decision/msvcp140.dll"} {
		file, err := filepath.EvalSymlinks(filepath.Join(root, name))
		if err != nil || !inside(root, file) {
			return false
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return false
		}
		if !strings.Contains(lines, fmt.Sprintf("\n%x  ./%s\n", sha256.Sum256(data), name)) {
			return false
		}
	}
	return true
}

// EnsurePrerequisites runs before activation and native Windows launch. The
// installer owns prompts/UAC and reports reboot-required as a nonzero exit.
func (s Selection) EnsurePrerequisites() error {
	if runtime.GOOS != "windows" || !strings.HasPrefix(s.Platform, "windows-") {
		return nil
	}
	helper := filepath.Join(s.Root, "ensure-windows-runtime.ps1")
	if _, err := os.Stat(helper); errors.Is(err, os.ErrNotExist) && legacyWindowsRuntime(s.Root) {
		return nil
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return fmt.Errorf("SystemRoot is unavailable; cannot check Windows runtime")
	}
	cmd := exec.Command(filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", helper)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Windows runtime prerequisite failed; previous selection preserved (a requested restart must finish before retrying): %w", err)
	}
	return nil
}
func (s Selection) Executable() string { return executable(s.Root, s.Platform) }
func (s Selection) Env(base []string) []string {
	out := append([]string(nil), base...)
	sep := string(os.PathListSeparator)
	set := func(key, value string) {
		for i, v := range out {
			k, _, _ := strings.Cut(v, "=")
			if strings.EqualFold(k, key) {
				out[i] = key + "=" + value
				return
			}
		}
		out = append(out, key+"="+value)
	}
	get := func(key string) string {
		for _, v := range base {
			k, value, _ := strings.Cut(v, "=")
			if strings.EqualFold(k, key) {
				return value
			}
		}
		return ""
	}
	set("PATH", filepath.Join(s.Root, "bin")+sep+get("PATH"))
	if strings.HasPrefix(s.Platform, "linux-") {
		library := filepath.Join(s.Root, "lib")
		if prior := get("LD_LIBRARY_PATH"); prior != "" {
			library += sep + prior
		}
		set("LD_LIBRARY_PATH", library)
	}
	return out
}
func validate(root, platform string, allowLegacy ...bool) error {
	if _, e := os.Stat(filepath.Join(root, "BUILD-INCOMPLETE")); e == nil {
		return fmt.Errorf("incomplete release folder")
	}
	required := []string{executable(root, platform), filepath.Join(root, "prompts.yaml"), filepath.Join(root, "runtime", "manifest.json"), filepath.Join(root, "runtime", "decision", "requirements.lock"), filepath.Join(root, "MANIFEST.sha256")}
	switch {
	case strings.HasPrefix(platform, "windows-"):
		if len(allowLegacy) == 0 || !allowLegacy[0] || !legacyWindowsRuntime(root) {
			required = append(required, filepath.Join(root, "ensure-windows-runtime.ps1"))
		}
		required = append(required, filepath.Join(root, "kuzu_shared.dll"), filepath.Join(root, "bin", "rg.exe"), filepath.Join(root, "runtime", "decision", "python.exe"))
	case strings.HasPrefix(platform, "darwin-"):
		required = append(required, filepath.Join(root, "lib", "libkuzu.dylib"), filepath.Join(root, "bin", "rg"), filepath.Join(root, "runtime", "decision", "bin", "python3"), filepath.Join(root, "runtime", "worker", "bin", "python3"))
	case strings.HasPrefix(platform, "linux-"):
		required = append(required, filepath.Join(root, "lib", "libkuzu.so"), filepath.Join(root, "bin", "rg"), filepath.Join(root, "runtime", "decision", "bin", "python3"))
	default:
		return fmt.Errorf("unsupported release platform %q", platform)
	}
	for _, p := range required {
		s, e := os.Stat(p)
		if e != nil || !s.Mode().IsRegular() {
			return fmt.Errorf("release missing required file: %s", p)
		}
	}
	if !strings.HasPrefix(platform, "windows-") {
		commands := []string{executable(root, platform), filepath.Join(root, "bin", "rg"), filepath.Join(root, "runtime", "decision", "bin", "python3")}
		if strings.HasPrefix(platform, "darwin-") {
			commands = append(commands, filepath.Join(root, "runtime", "worker", "bin", "python3"))
		}
		for _, name := range commands {
			info, e := os.Stat(name)
			if e != nil || info.Mode().Perm()&0111 == 0 {
				return fmt.Errorf("release command is not executable: %s", name)
			}
		}
	}
	var manifest struct {
		Format   int
		Platform string
	}
	raw, e := os.ReadFile(filepath.Join(root, "runtime", "manifest.json"))
	if e != nil {
		return e
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Format != 1 || manifest.Platform != platform {
		return fmt.Errorf("release runtime platform mismatch")
	}
	return nil
}

// Prepare verifies and extracts a complete version without activating it.
// Existing versions, including a running Windows exe/DLL,
// are never overwritten. The temporary directory is private to this attempt.
func (m Manager) Prepare(ctx context.Context, r Release) (*Selection, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !filepath.IsAbs(m.Home) {
		return nil, fmt.Errorf("update storage must be an absolute user-data path")
	}
	existing := filepath.Join(m.Home, "versions", r.Version, m.Platform, "Caliper")
	if validVersion(r.Version) {
		if _, e := os.Lstat(filepath.Dir(existing)); e == nil {
			if e = validate(existing, m.Platform); e != nil {
				return nil, e
			}
			if e = validateInventory(existing); e != nil {
				return nil, e
			}
			return &Selection{Version: r.Version, Platform: m.Platform, Root: existing}, nil
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	if !validVersion(r.Version) {
		return nil, fmt.Errorf("invalid release version")
	}
	if e := os.MkdirAll(filepath.Join(m.Home, "versions"), 0700); e != nil {
		return nil, e
	}
	temp, e := os.MkdirTemp(filepath.Join(m.Home, "versions"), ".install-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(temp)
	check, e := m.get(ctx, r.ChecksumURL)
	if e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(check.Body)
	check.Body.Close()
	if e != nil {
		return nil, e
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty release checksum")
	}
	sum, e := hex.DecodeString(fields[0])
	if e != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("invalid release SHA256")
	}
	archive, e := os.Create(filepath.Join(temp, "bundle.tar.gz"))
	if e != nil {
		return nil, e
	}
	hash := sha256.New()
	e = m.downloadArchive(ctx, r, io.MultiWriter(archive, hash), fields[0])
	closeErr := archive.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), fields[0]) {
		return nil, fmt.Errorf("release checksum mismatch; existing installation unchanged")
	}
	f, e := os.Open(archive.Name())
	if e != nil {
		return nil, e
	}
	defer f.Close()
	unpack := filepath.Join(temp, "unpacked")
	if e = os.Mkdir(unpack, 0700); e != nil {
		return nil, e
	}
	if e = extract(f, unpack); e != nil {
		return nil, e
	}
	root := filepath.Join(unpack, "Caliper")
	if e = validate(root, m.Platform); e != nil {
		return nil, e
	}
	if e = validateInventory(root); e != nil {
		return nil, e
	}
	target := filepath.Join(m.Home, "versions", r.Version, m.Platform)
	if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return nil, e
	}
	if _, e = os.Lstat(target); e == nil {
		if e = validate(filepath.Join(target, "Caliper"), m.Platform); e != nil {
			return nil, e
		}
		if e = validateInventory(filepath.Join(target, "Caliper")); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	} else if e = os.Rename(unpack, target); e != nil {
		return nil, e
	}
	s := &Selection{Version: r.Version, Platform: m.Platform, Root: filepath.Join(target, "Caliper")}
	return s, nil
}

func (m Manager) Activate(s *Selection) (*Selection, error) {
	if !filepath.IsAbs(m.Home) {
		return nil, fmt.Errorf("update storage must be an absolute user-data path")
	}
	expected := filepath.Join(m.Home, "versions", s.Version, m.Platform, "Caliper")
	if !validVersion(s.Version) || s.Platform != m.Platform || s.Root != expected {
		return nil, fmt.Errorf("invalid activation target")
	}
	if e := validate(s.Root, m.Platform); e != nil {
		return nil, e
	}
	if e := s.EnsurePrerequisites(); e != nil {
		return nil, e
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	receipt, e := os.CreateTemp(m.Home, ".current-")
	if e != nil {
		return nil, e
	}
	name := receipt.Name()
	defer os.Remove(name)
	if _, e = receipt.Write(b); e == nil {
		e = receipt.Sync()
	}
	ce := receipt.Close()
	if e != nil {
		return nil, e
	}
	if ce != nil {
		return nil, ce
	}
	// Rename is atomic on Unix. Windows MoveFileEx supplies replace-existing
	// semantics without opening or renaming any executable in either version.
	if e = replaceFile(name, filepath.Join(m.Home, "current.json")); e != nil {
		return nil, e
	}
	return s, nil
}
func inside(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func extract(input io.Reader, root string) error {
	var e error
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	gz, e := gzip.NewReader(input)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if strings.Contains(h.Name, "\\") || strings.Contains(h.Name, ":") {
			return fmt.Errorf("invalid archive path %q", h.Name)
		}
		p := filepath.Join(root, filepath.FromSlash(h.Name))
		if strings.HasPrefix(h.Name, "/") || filepath.IsAbs(h.Name) || !inside(root, p) {
			return fmt.Errorf("archive path escapes install: %s", h.Name)
		}
		// Check every parent before creating anything; previous archive links may
		// not redirect writes outside the staging tree (including Windows junctions).
		parent := filepath.Dir(p)
		if e = safeParent(root, parent); e != nil {
			return e
		}
		if e = os.MkdirAll(parent, 0755); e != nil {
			return e
		}
		switch h.Typeflag {
		case tar.TypeDir:
			e = os.MkdirAll(p, os.FileMode(h.Mode)&0777)
		case tar.TypeReg, tar.TypeRegA:
			var f *os.File
			f, e = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0777)
			if e == nil {
				_, e = io.Copy(f, tr)
				ce := f.Close()
				if e == nil {
					e = ce
				}
			}
		case tar.TypeSymlink:
			target := filepath.Join(parent, filepath.FromSlash(h.Linkname))
			if filepath.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "\\") || strings.Contains(h.Linkname, ":") || !inside(root, target) {
				return fmt.Errorf("archive link escapes install: %s", h.Name)
			}
			e = os.Symlink(h.Linkname, p)
		case tar.TypeLink:
			target := filepath.Join(root, filepath.FromSlash(h.Linkname))
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil || !inside(root, resolved) {
				return fmt.Errorf("invalid archive hard link: %s", h.Name)
			}
			e = os.Link(resolved, p)
		default:
			return fmt.Errorf("unsupported archive entry %s", h.Name)
		}
		if e != nil {
			return e
		}
	}
}

func validateInventory(root string) error {
	_, failures, e := bundle.VerifyManifest(root)
	if e != nil {
		return e
	}
	if len(failures) > 0 {
		return fmt.Errorf("release inventory failed: %v", failures)
	}
	return nil
}
func safeParent(root, parent string) error {
	for probe := parent; ; probe = filepath.Dir(probe) {
		_, e := os.Lstat(probe)
		if e == nil {
			resolved, e := filepath.EvalSymlinks(probe)
			if e != nil || !inside(root, resolved) {
				return fmt.Errorf("archive parent escapes install: %s", parent)
			}
			return nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if probe == root || filepath.Dir(probe) == probe {
			return fmt.Errorf("invalid archive parent")
		}
	}
}

func (m Manager) Stage(ctx context.Context, r Release) (*Selection, error) {
	s, e := m.Prepare(ctx, r)
	if e != nil {
		return nil, e
	}
	return m.Activate(s)
}
