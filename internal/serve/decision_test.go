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

func TestDecisionProbeCatalogContainsOnlyReadyActiveModel(t *testing.T) {
	ready := `{"pid":123,"status":"ok","loaded":true,"ready":true,"loading":false,"model":"active/judge","active":{"model":"active/judge","runtime":"system_one","source":"/models/active","path":"/models/active","revision":null,"device":"gpu","native_device":"mps:0","architecture":"D1Model","parameters":123,"selection":{"model":"active/judge","runtime":"system_one","device":"gpu","path":null,"revision":null,"trust_remote_code":false}},"requested":{"model":"pending/judge","runtime":"system_one","device":"gpu","path":null,"revision":null,"trust_remote_code":false},"error":null,"backends":["gpu"]}`
	for _, tc := range []struct {
		name, body string
		up         bool
		available  []string
	}{
		{"ready-active-not-requested", ready, true, []string{"active/judge"}},
		{"unready-request-is-not-catalog", `{"pid":123,"status":"not_ready","loaded":false,"ready":false,"loading":true,"model":null,"active":null,"requested":{"model":"pending/judge","runtime":"system_one","device":"gpu","path":null,"revision":null,"trust_remote_code":false},"error":null,"backends":[]}`, true, nil},
		{"malformed-active-refused", strings.Replace(ready, `"model":"active/judge","active"`, `"model":"wrong/judge","active"`, 1), false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := client(map[string]struct {
				status int
				body   string
			}{"/health": route(200, tc.body)})
			h := Probe(c, Endpoint{Kind: KindSidecar, URL: "http://fixture.invalid"})
			if h.Up != tc.up || !reflect.DeepEqual(h.Available, tc.available) || len(h.Loaded) != 0 {
				t.Fatalf("native catalog: %+v", h)
			}
		})
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
	p, err := Start(Recipe{Name: "child", Argv: napCmd("30")}, filepath.Join(t.TempDir(), "p.log"))
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
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python 3 not on PATH — actual CLI flag contract unexercised")
	}
	help, err := exec.Command(python, script, "--help").CombinedOutput()
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
