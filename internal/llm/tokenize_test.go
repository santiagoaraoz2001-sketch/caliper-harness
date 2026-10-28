package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomainTokenizerUsesExactUnicodeWithoutGeneration(t *testing.T) {
	text := `{"body":"café 🚀 漢字","links":["knowledge:project/guides/ui/index#palette"]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tokenize" {
			t.Errorf("unexpected generation path %s", r.URL.Path)
		}
		var payload struct {
			Content    string `json:"content"`
			AddSpecial bool   `json:"add_special"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Content != text || payload.AddSpecial {
			t.Errorf("payload changed: %+v %v", payload, err)
		}
		w.Write([]byte(`{"tokens":[7,9,12]}`))
	}))
	defer server.Close()
	n, err := New(server.URL+"/v1", "fixture").WithRuntime(RuntimeRulith).CountTokens(t.Context(), text)
	if err != nil || n != 3 {
		t.Fatalf("count %d: %v", n, err)
	}
	if _, err := New(server.URL, "fixture").CountTokens(t.Context(), text); err == nil {
		t.Fatal("generic endpoint silently claimed an exact tokenizer")
	}
}

func TestDomainTokenizerRejectsMalformedAndCancelledResponses(t *testing.T) {
	for _, body := range []string{`{}`, `{"tokens":[]}`, `{"tokens":[null]}`, `{"tokens":[-1]}`, `{"tokens":[1.5]}`, `{"tokens":["one"]}`, `{"tokens":[1]} {}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			if _, err := New(server.URL, "fixture").WithRuntime(RuntimeRulith).CountTokens(t.Context(), "input"); err == nil {
				t.Fatal("malformed token response accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := New("http://127.0.0.1:1", "fixture").WithRuntime(RuntimeRulith).CountTokens(ctx, "input")
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancellation lost: %v", err)
	}
}
