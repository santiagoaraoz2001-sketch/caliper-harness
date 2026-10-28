package bundle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"specific-harness/internal/testfs"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInventoryRefusesWindowsEscapesOnEveryPlatform(t *testing.T) {
	for _, rel := range []string{`./..\outside`, `./C:/outside`, `./sub\..\outside`} {
		if _, err := manifestRel(rel); err == nil {
			t.Fatalf("accepted %q", rel)
		}
	}
}
func TestInventoryRefusesExternalSymlink(t *testing.T) {
	root := t.TempDir()
	writeInventory(t, root, map[string]string{"runtime/data": "bytes"})
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "data"), []byte("bytes"), 0600)
	os.RemoveAll(filepath.Join(root, "runtime"))
	if err := testfs.Symlink(outside, filepath.Join(root, "runtime")); err != nil {
		if runtime.GOOS == "windows" && (os.IsPermission(err) || errors.Is(err, syscall.Errno(1314))) {
			t.Skip("Windows symlink creation requires Developer Mode or privilege")
		}
		t.Fatal(err)
	}
	if _, _, err := VerifyManifest(root); err == nil {
		t.Fatal("external matching bytes accepted")
	}
}
func pythonTar(t *testing.T, entries []*tar.Header) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "python.tar.gz")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	gz.Close()
	f.Close()
	return name
}
func TestPythonArchiveRefusesEscapingPathsAndLinks(t *testing.T) {
	for _, h := range []*tar.Header{
		{Name: `python/..\outside`, Typeflag: tar.TypeReg},
		{Name: "python/../../outside", Typeflag: tar.TypeReg},
		{Name: "python/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"},
		{Name: "python/link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"},
		{Name: "python/link", Typeflag: tar.TypeLink, Linkname: "python/../../outside"},
	} {
		t.Run(h.Name+"-"+h.Linkname, func(t *testing.T) {
			if err := extractPython(pythonTar(t, []*tar.Header{h}), t.TempDir()); err == nil {
				t.Fatal("escape accepted")
			}
		})
	}
}
func TestRepairTransactionRestoresEverySwap(t *testing.T) {
	root := t.TempDir()
	stage := t.TempDir()
	tx := &repairTransaction{stage: stage}
	for _, name := range []string{"worker", "decision", "MANIFEST.sha256"} {
		target := filepath.Join(root, name)
		fresh := filepath.Join(stage, "new-"+name)
		os.WriteFile(target, []byte("old-"+name), 0600)
		os.WriteFile(fresh, []byte("new-"+name), 0600)
		if err := tx.activate(fresh, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.rollback(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker", "decision", "MANIFEST.sha256"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(b) != "old-"+name {
			t.Fatalf("lost %s: %s %v", name, b, err)
		}
	}
}
func TestRepairedInventoryDoesNotBlessOtherChangesOrUserFiles(t *testing.T) {
	root := t.TempDir()
	writeInventory(t, root, map[string]string{"app": "original", "bin/" + rgFileName(): "old-rg"})
	original, _ := os.ReadFile(filepath.Join(root, ManifestFile))
	os.WriteFile(filepath.Join(root, "bin", rgFileName()), []byte("new-rg"), 0755)
	os.WriteFile(filepath.Join(root, "personal-session"), []byte("private"), 0600)
	if err := writeRepairedInventory(root, original, nil, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ManifestFile))
	if strings.Contains(string(data), "personal-session") {
		t.Fatal("user file inventoried")
	}
	os.WriteFile(filepath.Join(root, "app"), []byte("changed"), 0600)
	if err := writeRepairedInventory(root, original, nil, true); err == nil {
		t.Fatal("unrelated edit blessed")
	}
}
func TestRepairRefusesUninventoriedTrustAnchors(t *testing.T) {
	if err := repairAnchorsInventoried([]byte(strings.Repeat("0", 64) + "  ./app\n")); err == nil {
		t.Fatal("missing source trust accepted")
	}
}
func TestVerifyReleasedBundleTiming(t *testing.T) {
	root := os.Getenv("CALIPER_VERIFY_BUNDLE")
	if root == "" {
		t.Skip("read-only installed-bundle measurement requested explicitly")
	}
	start := time.Now()
	checked, failures, err := VerifyManifest(root)
	t.Logf("full inventory: %d files in %s", checked, time.Since(start))
	if err != nil || len(failures) > 0 {
		t.Fatalf("failures=%v error=%v", failures, err)
	}
}

type repairTransport func(*http.Request) (*http.Response, error)

func (f repairTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRepairRipgrepCommitsOrRollsBackAfterFullValidation(t *testing.T) {
	for _, tc := range []struct {
		badManifest bool
		ending      string
	}{{false, "\n"}, {true, "\n"}, {false, "\r\n"}, {true, "\r\n"}} {
		badManifest := tc.badManifest
		t.Run(fmt.Sprintf("bad=%t ending=%q", badManifest, tc.ending), func(t *testing.T) {
			root := fixture(t)
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			w, _ := zw.Create("release/" + rgFileName())
			w.Write([]byte("repaired search"))
			zw.Close()
			digest := sha256.Sum256(archive.Bytes())
			source := fmt.Sprintf(`{"platform":%q,"python":{"url":"https://fixture.invalid/python","sha256":%q},"ripgrep":{"url":"https://fixture.invalid/rg.zip","sha256":%q}}`, platform(), strings.Repeat("0", 64), hex.EncodeToString(digest[:]))
			files := map[string]string{"runtime/sources.json": source, Marker: `{"format":1,"platform":"` + platform() + `"}`, "bin/" + rgFileName(): "original search"}
			directory := runtime.GOOS
			if directory == "darwin" {
				directory = "macos"
			}
			files["python/"+directory+"/verify_runtime.py"] = "verified-script"
			if runtime.GOOS == "windows" {
				files["ensure-windows-runtime.ps1"] = "verified prerequisite helper"
				files["python/windows/use_system_runtime.py"] = "verified normalizer"
			}
			for _, p := range requiredProfiles() {
				rel, _ := filepath.Rel(root, Python(root, p))
				files[filepath.ToSlash(rel)] = "interpreter"
				files["runtime/"+p+"/requirements.lock"] = "pinned"
			}
			if badManifest {
				files[Marker] = `{"format":1,"platform":"wrong-platform"}`
			}
			writeInventory(t, root, files)
			original, _ := os.ReadFile(filepath.Join(root, ManifestFile))
			// Python's text writer emits CRLF on Windows; run the actual complete
			// repair transaction against that independent on-disk layout too.
			original = bytes.ReplaceAll(original, []byte("\n"), []byte(tc.ending))
			if err := os.WriteFile(filepath.Join(root, ManifestFile), original, 0600); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(root, "bin", rgFileName()), []byte("broken search"), 0755)
			old := http.DefaultTransport
			http.DefaultTransport = repairTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive.Bytes())), Header: make(http.Header)}, nil
			})
			defer func() { http.DefaultTransport = old }()
			err := Repair(root, []Failure{{Path: "bin/" + rgFileName(), Kind: FailureChanged}}, nil)
			actual, _ := os.ReadFile(filepath.Join(root, "bin", rgFileName()))
			if badManifest {
				if err == nil || string(actual) != "broken search" {
					t.Fatalf("failed validation did not restore old bytes: %q %v", actual, err)
				}
				after, _ := os.ReadFile(filepath.Join(root, ManifestFile))
				if !bytes.Equal(original, after) {
					t.Fatal("failed repair replaced inventory")
				}
			} else {
				if err != nil || string(actual) != "repaired search" {
					t.Fatalf("repair: %q %v", actual, err)
				}
			}
		})
	}
}

func TestManagedInterpreterCannotEscapeBundle(t *testing.T) {
	root := fixture(t)
	outside := filepath.Join(t.TempDir(), "python")
	os.WriteFile(outside, []byte("external interpreter"), 0755)
	interpreter := Python(root, requiredProfiles()[0])
	os.Remove(interpreter)
	if err := os.Symlink(outside, interpreter); err != nil {
		if runtime.GOOS == "windows" && (os.IsPermission(err) || errors.Is(err, syscall.Errno(1314))) {
			t.Skip("Windows symlink creation requires Developer Mode or privilege")
		}
		t.Fatal(err)
	}
	if err := Check(root); err == nil {
		t.Fatal("external interpreter accepted")
	}
}
func TestHashRefusesDirectories(t *testing.T) {
	if _, err := hashFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("nonfile accepted: %v", err)
	}
}

func TestWindowsRepairNormalizationFailurePreservesInstalledRuntime(t *testing.T) {
	root, fresh := t.TempDir(), t.TempDir()
	prior := filepath.Join(root, "runtime", "decision", "msvcp140.dll")
	if err := os.MkdirAll(filepath.Dir(prior), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prior, []byte("prior runtime"), 0644); err != nil {
		t.Fatal(err)
	}
	// A missing verified archive must refuse normalization without changing
	// the installed runtime.
	if err := normalizeWindowsRuntime(root, fresh, filepath.Join(root, "missing-python.tar.gz")); err == nil || !strings.Contains(err.Error(), "normalization failed") {
		t.Fatalf("failed normalizer accepted: %v", err)
	}
	got, err := os.ReadFile(prior)
	if err != nil || string(got) != "prior runtime" {
		t.Fatalf("installed runtime changed: %q %v", got, err)
	}
}

func TestWindowsNormalizerRunsOutsideTargetRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable process-boundary fixture uses a Unix executable; native Windows build verifies actual Python")
	}
	for _, exit := range []int{0, 9} {
		t.Run(fmt.Sprint(exit), func(t *testing.T) {
			root, stage := t.TempDir(), t.TempDir()
			fresh := filepath.Join(stage, "new-decision")
			if err := os.MkdirAll(filepath.Dir(interpreterIn(fresh)), 0755); err != nil {
				t.Fatal(err)
			}
			// Selecting the target interpreter is the regression: it must never run.
			if err := os.WriteFile(interpreterIn(fresh), []byte("#!/bin/sh\nexit 97\n"), 0755); err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$0\" \"$1\" \"$2\" \"$3\" > \"$3/normalizer-command.txt\"\nexit %d\n", exit)
			var data bytes.Buffer
			gz := gzip.NewWriter(&data)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: "python/bin/python3", Mode: 0755, Size: int64(len(script))}); err != nil {
				t.Fatal(err)
			}
			tw.Write([]byte(script))
			tw.Close()
			gz.Close()
			archive := filepath.Join(stage, "python.tar.gz")
			if err := os.WriteFile(archive, data.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			err := normalizeWindowsRuntime(root, fresh, archive)
			if (err != nil) != (exit != 0) {
				t.Fatalf("exit %d: %v", exit, err)
			}
			raw, err := os.ReadFile(filepath.Join(fresh, "normalizer-command.txt"))
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(args) != 4 || strings.HasPrefix(args[0], fresh+string(os.PathSeparator)) || args[1] != "-I" || args[2] != filepath.Join(root, "python/windows/use_system_runtime.py") || args[3] != fresh {
				t.Fatalf("wrong normalizer command: %q", args)
			}
			if _, err := os.Stat(args[0]); !os.IsNotExist(err) {
				t.Fatalf("temporary runner retained: %v", err)
			}
			original, _ := os.ReadFile(interpreterIn(fresh))
			if string(original) != "#!/bin/sh\nexit 97\n" {
				t.Fatal("target interpreter changed")
			}
		})
	}
}
