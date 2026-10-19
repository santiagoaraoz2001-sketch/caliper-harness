package bundle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if err := os.Symlink(outside, filepath.Join(root, "runtime")); err != nil {
		if runtime.GOOS == "windows" && os.IsPermission(err) {
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
	for _, badManifest := range []bool{false, true} {
		t.Run(fmt.Sprint(badManifest), func(t *testing.T) {
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
				files["msvcp140.dll"] = "verified CRT source"
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
		if runtime.GOOS == "windows" && os.IsPermission(err) {
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

func TestWindowsRepairCopiesPrivateRuntimeDLL(t *testing.T) {
	root, fresh := t.TempDir(), t.TempDir()
	want := []byte("inventoried Windows CRT bytes")
	if err := os.WriteFile(filepath.Join(root, "msvcp140.dll"), want, 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyWindowsRuntimeDLL(root, fresh); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(fresh, "msvcp140.dll"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("private runtime DLL differs: %q, %v", got, err)
	}
	// Copying must not move/delete the verified app-root source.
	got, err = os.ReadFile(filepath.Join(root, "msvcp140.dll"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("app-root source changed: %q, %v", got, err)
	}
}

func TestWindowsRepairMissingRuntimeSourceRefuses(t *testing.T) {
	root, fresh := t.TempDir(), t.TempDir()
	err := copyWindowsRuntimeDLL(root, fresh)
	if err == nil || !strings.Contains(err.Error(), "msvcp140.dll") {
		t.Fatalf("missing app-root source accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fresh, "msvcp140.dll")); !os.IsNotExist(err) {
		t.Fatalf("failed copy created a private DLL: %v", err)
	}
}
