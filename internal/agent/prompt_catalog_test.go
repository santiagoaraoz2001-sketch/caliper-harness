package agent

import (
	"context"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestPromptCatalogEditReachesNativeWireAndRunSnapshot(t *testing.T) {
	var doc struct {
		Texts map[string]string `yaml:"texts"`
	}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prompts.yaml")
	save := func(marker string) {
		doc.Texts["system.native.orientation"] = marker
		raw, e := yaml.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save("RUN_A")
	pc, e := prompts.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
			return
		}
		calls++
		want := "RUN_A"
		if calls == 3 {
			want = "RUN_B"
		}
		if len(req.Messages) == 0 || !strings.HasPrefix(req.Messages[0].Content, want+" ") {
			t.Errorf("wire%d lost snapshot: %+v", calls, req.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls == 1 {
			save("RUN_B")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"))
			return
		}
		if calls == 2 {
			found := false
			for _, m := range req.Messages {
				found = found || m.Role == "tool" && m.ToolCallID == "call1" && strings.Contains(m.Content, "ACTUAL_ECHO")
			}
			if !found {
				t.Error("actual correlated tool receipt missing")
			}
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"complete\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	makeRunner := func(c *prompts.Catalog) *Runner {
		reg := tool.NewRegistry(c)
		reg.Register(tool.Definition{Name: "echo", Execute: func(context.Context, tool.Args) (tool.Result, error) { return tool.Result{Text: "ACTUAL_ECHO"}, nil }})
		return &Runner{Prompts: c, Model: llm.New(srv.URL, "component-model").WithToolProtocol("structured"), Registry: reg, MaxSteps: 3}
	}
	if _, _, e := makeRunner(pc).Run(context.Background(), "exact user request"); e != nil {
		t.Fatal(e)
	}
	fresh, e := prompts.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := makeRunner(fresh).Run(context.Background(), "next user request"); e != nil {
		t.Fatal(e)
	}
	if calls != 3 {
		t.Fatalf("requests=%d", calls)
	}
}
