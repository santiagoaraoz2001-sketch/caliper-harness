package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNumberedLines(t *testing.T) {
	out := Numbered([]string{"a", "b", "c"}, 40)
	if len(out) != 3 || !strings.Contains(out[0], "1") || !strings.Contains(out[2], "3") {
		t.Fatalf("out=%v", out)
	}
	// Long lines wrap under their number, keeping the count.
	out = Numbered([]string{strings.Repeat("x", 100)}, 20)
	if len(out) < 2 || !strings.Contains(out[0], "1") {
		t.Fatalf("wrap=%v", out)
	}
	for _, ln := range out[1:] {
		if strings.Contains(ln, "2") && !strings.Contains(ln, "x") {
			t.Errorf("wrapped line misnumbered: %q", ln)
		}
	}
}

func TestQuoteRanges(t *testing.T) {
	logical := []string{"l1", "l2", "l3", "l4", "l5"}
	q, err := Quote(logical, "[2-3, 5]")
	if err != nil || q != "l2\nl3\nl5" {
		t.Fatalf("q=%q err=%v", q, err)
	}
	if _, err := Quote(logical, "[1-9]"); err == nil {
		t.Fatal("out-of-range must fail")
	}
	if _, err := Quote(logical, "[0]"); err == nil {
		t.Fatal("line zero must fail")
	}
	if _, err := Quote(logical, "[3-1]"); err == nil {
		t.Fatal("reversed range must fail")
	}
}

func TestGotoScroll(t *testing.T) {
	if got := GotoScroll(100, 135, 20); got != 80 {
		t.Errorf("clamps to end: %d", got)
	}
	if got := GotoScroll(100, 5, 20); got != 4 {
		t.Errorf("line at top: %d", got)
	}
	if got := GotoScroll(10, 5, 20); got != 0 {
		t.Errorf("short file starts at zero: %d", got)
	}
	if got := GotoScroll(100, -3, 20); got != 0 {
		t.Errorf("negative clamps: %d", got)
	}
}

// fileTree builds a temp root: two files, one nested, one hidden,
// one binary.
func fileTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("bee"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hid"), []byte("h"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin"), []byte{0x0, 0x1, 0x2}, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestListFilesHidesHidden(t *testing.T) {
	dir := fileTree(t)
	files, trunc, err := listFiles(dir, false)
	if err != nil || trunc {
		t.Fatalf("err=%v trunc=%v", err, trunc)
	}
	var rels []string
	for _, f := range files {
		rels = append(rels, f.Rel)
	}
	want := []string{"a.txt", "bin", filepath.Join("sub", "b.txt")}
	if strings.Join(rels, ",") != strings.Join(want, ",") {
		t.Fatalf("rels=%v", rels)
	}
	shown, _, err := listFiles(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(shown) != 4 {
		t.Fatalf("hidden toggle must reveal .hid: %v", shown)
	}
}

func TestReadFileBinaryAndMissing(t *testing.T) {
	dir := fileTree(t)
	if _, err := readFile(dir, "bin"); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("err=%v", err)
	}
	if _, err := readFile(dir, "nope.txt"); err == nil {
		t.Error("missing must fail")
	}
	lines, err := readFile(dir, "a.txt")
	if err != nil || len(lines) != 3 || lines[1] != "two" {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}

func TestFilesViewOpensAndEscapes(t *testing.T) {
	m := New(testCfg(), "test")
	m = submit(m, "/files")
	if !m.filesActive() {
		t.Fatal("not on the files view")
	}
}

func loadFiles(m Model) Model {
	um, _ := m.Update(m.refreshFiles()())
	return um.(Model)
}

func TestFilesBrowseCommentGotoCycle(t *testing.T) {
	dir := fileTree(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	m := loadFiles(submit(New(testCfg(), "test"), "/files"))
	if len(m.Files.Files) != 3 {
		t.Fatalf("files=%+v", m.Files.Files)
	}
	m = key(m, "enter")
	cmd := m.openFile("a.txt")
	um, _ := m.Update(cmd())
	m = um.(Model)
	if m.Files.Viewer == nil || m.Files.Viewer.Title != "a.txt" {
		t.Fatalf("viewer=%+v", m.Files.Viewer)
	}
	if out := m.renderFiles(80, 20); !strings.Contains(out, "2") || !strings.Contains(out, "two") {
		t.Errorf("viewer must number lines:\n%s", out)
	}
	m = submit(m, "/goto 3")
	if m.Files.Viewer.Scroll != 0 || !strings.Contains(m.View(), "3  three") {
		t.Fatalf("scroll=%d", m.Files.Viewer.Scroll)
	}
	m = submit(m, "/comment [2] typo here")
	if len(m.Comment) != 1 || m.Comment[0].Ref != "a.txt" || m.Comment[0].Quote != "two" {
		t.Fatalf("comment=%+v", m.Comment)
	}
	m = key(m, "esc")
	if m.Files.Viewer != nil {
		t.Fatal("esc must close the viewer")
	}
	m = key(m, "h")
	m = loadFiles(m)
	if len(m.Files.Files) != 4 {
		t.Fatalf("h must reveal hidden: %+v", m.Files.Files)
	}
	// One esc for the /goto info alert, one for the view: the ladder
	// dismisses alerts before leaving.
	m = key(m, "esc")
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Fatal("esc must walk files back to chat")
	}
}

func TestGotoRefusesWithoutViewer(t *testing.T) {
	m := New(testCfg(), "test")
	n := len(m.Alerts)
	m = submit(m, "/goto 10")
	if len(m.Alerts) != n+1 || !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "open file viewer") {
		t.Fatalf("alerts=%+v", m.Alerts)
	}
}

func TestCommentLinesRefuseWithoutViewer(t *testing.T) {
	m := New(testCfg(), "test")
	m = submit(m, "/comment [1-2] stray")
	if len(m.Comment) != 0 {
		t.Fatalf("comment=%+v", m.Comment)
	}
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "open file viewer") {
		t.Fatalf("alerts=%+v", m.Alerts)
	}
}

func TestCommentRefMismatchRefuses(t *testing.T) {
	dir := fileTree(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	m := loadFiles(submit(New(testCfg(), "test"), "/files"))
	cmd := m.openFile("a.txt")
	um, _ := m.Update(cmd())
	m = um.(Model)
	m = submit(m, "/comment other.txt [1] wrong file")
	if len(m.Comment) != 0 {
		t.Fatalf("comment=%+v", m.Comment)
	}
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "open other.txt") {
		t.Fatalf("alerts=%+v", m.Alerts)
	}
}

func TestIsRefSpotsFiles(t *testing.T) {
	for s, want := range map[string]bool{
		"T1.1":        true,
		"approachX-T": true,
		"sub/a.txt":   true,
		"a.txt":       true,
		"main.go":     true,
		"hello":       false,
		"fix it":      false,
		"e.g.":        false,
		".hid":        false,
		"v2":          true,
	} {
		if got := isRef(s); got != want {
			t.Errorf("isRef(%q)=%v", s, got)
		}
	}
}
