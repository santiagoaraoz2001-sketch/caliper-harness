package serve

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"specific-harness/internal/decision"
	"strings"
	"testing"
	"time"
)

func TestDecisionProbeUnrelated200Refuses(t *testing.T) {
	c := &http.Client{Transport: &stubTransport{routes: map[string]struct {
		status int
		body   string
	}{"/health": {200, `{"status":"ok"}`}}}}
	if h := Probe(c, Endpoint{Kind: KindSidecar, URL: "http://fixture.invalid"}); h.Up {
		t.Fatal("unrelated200 falsely healthy")
	}
}

func TestDecisionRecipeOpenSelectionAndOwnership(t *testing.T) {
	path, rev := "/downloaded native", "fixed"
	s := decision.Selection{Model: "third/compatible", Runtime: "system_one", Device: "gpu", Path: &path, Revision: &rev, TrustRemoteCode: true}
	r, ok := SidecarRecipe("/repo", "http://127.0.0.1:19091", s.Model, s)
	if !ok {
		t.Fatal("missing recipe")
	}
	for _, v := range []string{"system_one", "gpu", path, rev, "--trust-remote-code", s.Model, "19091"} {
		found := false
		for _, a := range r.Argv {
			if a == v {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing argv %q: %q", v, r.Argv)
		}
	}
	custom := Recipe{Name: "sidecar", Kind: KindSidecar, URL: r.URL, Argv: []string{"/chosen/python", "custom.py", "--assets", path}}
	got := MergeRecipes([]Recipe{r}, []Recipe{custom})
	if !reflect.DeepEqual(got[0].Argv, custom.Argv) {
		t.Fatal("custom interpreter/argv overwritten")
	}
	p, err := Start(Recipe{Name: "child", Argv: []string{"sleep", "30"}}, filepath.Join(t.TempDir(), "p.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if p.PID() <= 0 {
		t.Fatal("live child has no PID")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for p.PID() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.PID() != 0 {
		t.Fatal("reaped child remains owned")
	}
}

func TestDecisionRecipeFlagsMatchActualPythonCLI(t *testing.T) {
	script, err := filepath.Abs("../../python/decision/server.py")
	if err != nil {
		t.Fatal(err)
	}
	help, err := exec.Command("python3", script, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("actual parser help: %s %v", help, err)
	}
	path := "/downloaded"
	s := decision.Selection{Model: "open/model", Runtime: "system_one", Device: "auto", Path: &path, TrustRemoteCode: true}
	rec, _ := SidecarRecipe(filepath.Dir(filepath.Dir(filepath.Dir(script))), "http://127.0.0.1:8090", s.Model, s)
	for _, flag := range rec.Argv[2:] {
		if strings.HasPrefix(flag, "--") && !strings.Contains(string(help), flag+" ") && !strings.Contains(string(help), flag+"]") {
			t.Fatalf("recipe flag %q unsupported by actual CLI: %s", flag, help)
		}
	}
}
