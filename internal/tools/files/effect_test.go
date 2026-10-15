package files

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestFileEffectActualPublicationAndMatchRemedy(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	raw := "same\nsame\n"
	os.WriteFile(filepath.Join(dir, "a"), []byte(raw), 0600)
	for _, c := range []struct{ old, want string }{{"", "old is empty"}, {"missing", "old matched 0 times"}, {"same", "old matched 2 times"}} {
		var r tool.Result
		var e error
		if c.old == "" { // Execute boundary: validator intentionally rejects empty old before execution.
			for _, d := range w.Definitions() {
				if d.Name == "edit" {
					r, e = d.Execute(t.Context(), map[string]any{"path": "a", "old": "", "new": "new", "expected_hash": digest([]byte(raw))})
				}
			}
		} else {
			r, e = invoke(t, w, "edit", map[string]string{"path": "a", "old": c.old, "new": "new", "expected_hash": digest([]byte(raw))})
		}
		if e == nil || !strings.Contains(e.Error(), c.want) || r.Changed {
			t.Errorf("remedy %+v %v want %s", r, e, c.want)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "a"))
		if string(b) != raw {
			t.Fatal("refusal published")
		}
	}
	res, e := invoke(t, w, "edit", map[string]string{"path": "./a", "old": raw, "new": "exact\t\n", "expected_hash": digest([]byte(raw))})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), "FileEffect") {
		t.Fatal("machine metadata leaked through Result JSON")
	}
	m := effectMetadata(res)
	if !res.Changed || m == nil || m.Path != "a" || m.BeforeHash != digest([]byte(raw)) || m.AfterHash != digest([]byte("exact\t\n")) || m.Created {
		t.Fatalf("actual metadata %s", b)
	}
	res, e = invoke(t, w, "write", map[string]string{"path": "empty", "content": ""})
	b, _ = json.Marshal(res)
	m = effectMetadata(res)
	if e != nil || m == nil || !m.Created || m.BeforeHash != m.AfterHash {
		t.Fatalf("exclusive empty create %s %v", b, e)
	}
	res, e = invoke(t, w, "write", map[string]string{"path": "a", "content": "exact\t\n", "expected_hash": digest([]byte("exact\t\n"))})
	b, _ = json.Marshal(res)
	m = effectMetadata(res)
	if e != nil || res.Changed || m != nil {
		t.Fatalf("noop invented effect %s %v", b, e)
	}
}

// Reflection keeps the same behavioral witness compilable against the original Result.
func effectMetadata(res any) *struct {
	Path, BeforeHash, AfterHash string
	Created                     bool
} {
	v := reflect.ValueOf(res).FieldByName("FileEffect")
	if !v.IsValid() || v.IsNil() {
		return nil
	}
	b, _ := json.Marshal(v.Interface())
	var m *struct {
		Path, BeforeHash, AfterHash string
		Created                     bool
	}
	json.Unmarshal(b, &m)
	return m
}
