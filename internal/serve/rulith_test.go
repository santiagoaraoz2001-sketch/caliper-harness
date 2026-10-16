package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRulithManagerHelper(t *testing.T) {
	if os.Getenv("CALIPER_RULITH_FIXTURE") != "1" {
		return
	}
	if mode := os.Getenv("CALIPER_RULITH_PIPE"); mode != "" {
		if mode == "child" {
			time.Sleep(1800 * time.Millisecond)
			_ = os.WriteFile(os.Getenv("CALIPER_RULITH_MARKER"), []byte("alive"), 0600)
			os.Exit(0)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestRulithManagerHelper$")
		child.Env = append(os.Environ(), "CALIPER_RULITH_PIPE=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(5)
		}
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	var req struct {
		Op   string         `json:"op"`
		Data map[string]any `json:"data"`
	}
	if json.NewDecoder(os.Stdin).Decode(&req) != nil {
		os.Exit(2)
	}
	state := os.Getenv("CALIPER_RULITH_STATE")
	raw, _ := os.ReadFile(state)
	status := string(raw)
	if status == "" {
		status = "stopped"
	}
	var data any
	switch req.Op {
	case "catalog":
		data = map[string]any{"models": []map[string]any{{"id": "catalog-ID", "name": "served-name", "path": "model.gguf", "role": "model", "missing": []string{}}, {"id": "bad", "path": "part.gguf", "role": "model", "missing": []string{"shard"}}, {"path": "arbitrary-file", "role": "model"}}}
	case "start":
		if req.Data["id"] != "catalog-ID" {
			os.Exit(3)
		}
		_ = os.WriteFile(state, []byte("ready"), 0600)
		data = map[string]bool{"accepted": true}
	case "stop":
		_ = os.WriteFile(state, []byte("stopped"), 0600)
		data = map[string]bool{"accepted": true}
	case "status":
		data = map[string]any{"status": status, "model_name": "served-name", "endpoint": os.Getenv("CALIPER_RULITH_ENDPOINT")}
	default:
		os.Exit(4)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "data": data})
	os.Exit(0)
}

func TestRulithManagerRequiresActualCatalogReadiness(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "load-unload", true: "wrong-http-identity"}[wrong], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				name := "served-name"
				if wrong {
					name = "other-model"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": name}}})
			}))
			defer server.Close()
			t.Setenv("CALIPER_RULITH_FIXTURE", "1")
			t.Setenv("CALIPER_RULITH_STATE", filepath.Join(t.TempDir(), "state"))
			t.Setenv("CALIPER_RULITH_ENDPOINT", server.URL+"/v1")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			install := RulithInstall{Python: binary, Manager: "-test.run=^TestRulithManagerHelper$"}
			assets, err := RulithAssets(t.Context(), install)
			if err != nil || len(assets) != 3 || !assets[0].Complete || assets[1].Complete || assets[2].Complete || AssetModelName(assets[0]) != "served-name" {
				t.Fatalf("catalog %+v %v", assets, err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			h, err := SetRulithResident(ctx, server.Client(), install, server.URL+"/v1", "catalog-ID", true)
			if wrong {
				if err == nil || h.Up {
					t.Fatal("manager acceptance falsely proved residency", h)
				}
				return
			}
			if err != nil || !h.Up || len(h.Loaded) != 1 || h.Loaded[0] != "served-name" {
				t.Fatalf("load %+v %v", h, err)
			}
			if _, err = SetRulithResident(t.Context(), server.Client(), install, server.URL+"/different", "catalog-ID", false); err == nil || !strings.Contains(err.Error(), "endpoint") {
				t.Fatal("wrong endpoint accepted", err)
			}
			h, err = SetRulithResident(t.Context(), server.Client(), install, server.URL+"/v1", "catalog-ID", false)
			if err != nil || h.Up {
				t.Fatalf("unload %+v %v", h, err)
			}
		})
	}
}

func TestRulithCancellationSettlesInheritedPipeWithoutKillingServer(t *testing.T) {
	t.Setenv("CALIPER_RULITH_FIXTURE", "1")
	t.Setenv("CALIPER_RULITH_PIPE", "parent")
	marker := filepath.Join(t.TempDir(), "child-alive")
	t.Setenv("CALIPER_RULITH_MARKER", marker)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = rulithCall(ctx, RulithInstall{Python: binary, Manager: "-test.run=^TestRulithManagerHelper$"}, "status", nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancelled manager did not settle: %v after %s", err, time.Since(start))
	}
	for time.Since(start) < 4*time.Second {
		if _, err = os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("manager-owned server child was killed or never ran")
}
