package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestGenerationBudgetParentAndChildFactories(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		requests++
		if b["max_completion_tokens"] != float64(16384) {
			t.Errorf("factory missing approved default: %v", b)
		}
		io.WriteString(w, `{"model":"fixture","choices":[{"message":{"content":"answer"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	s, err := store.Open(filepath.Join(t.TempDir(), "kuzu"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := config.Defaults()
	c.Model.Server = srv.URL
	c.Recall.TopN = 0
	c.Behavior.Path = ""
	c.Model.Runtime = "openai"
	r, _, err := New(Deps{Cfg: c, Store: s, Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 2, In: strings.NewReader(""), Out: io.Discard}, "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.Model.Chat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	d, setupErr := LaunchDeps(c, s, srv.URL, "fixture", 2, 32768, strings.NewReader(""), io.Discard)
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	// Both worker and reviewer receive independent clients from this same actual factory.
	for i := 0; i < 2; i++ {
		if _, _, _, err := d.NewModel().Chat(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 3 {
		t.Fatalf("unexpected requests %d", requests)
	}
}

func TestGenerationBudgetPartialDoesNotExecuteTools(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	partial := "```tool\ntool: bash\ncmd: touch '" + marker + "'\n```"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["max_completion_tokens"] != float64(32) {
			t.Errorf("partial route cap missing: %v", b)
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": partial}, "finish_reason": "length"}}, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 32}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer srv.Close()
	s, err := store.Open(filepath.Join(dir, "kuzu"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := config.Defaults()
	c.Model.Server = srv.URL
	c.Params.MaxOutputTokens = 32
	c.Recall.TopN = 0
	c.Behavior.Path = ""
	c.Auth.Mode = config.AuthYOLO
	r, _, err := New(Deps{Cfg: c, Store: s, Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 2, In: strings.NewReader(""), Out: io.Discard}, "task")
	if err != nil {
		t.Fatal(err)
	}
	final, _, err := r.Run(context.Background(), "task")
	if err == nil || !strings.Contains(err.Error(), "params.max_output_tokens=32 including reasoning") || !strings.Contains(final, partial) || r.Outcome.ExitCode() == 0 || r.UsedOut != 32 {
		t.Fatalf("partial promoted/lost: final=%q outcome=%+v usage=%d err=%v", final, r.Outcome, r.UsedOut, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("partial tool executed")
	}
}
