package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDomainTokenizerMarksOnlyKnownUnsupportedCapabilities(t *testing.T) {
	for _, runtime := range []string{"", RuntimeOpenAI, RuntimeMLX, RuntimeOllama, "typo"} {
		t.Run(runtime, func(t *testing.T) {
			client := New("http://127.0.0.1:1", "unused").WithRuntime(runtime)
			_, err := client.CountTokens(t.Context(), "漢🚀")
			if err == nil || errors.Is(err, ErrTokenCountingUnsupported) != (runtime != "typo") {
				t.Fatalf("wrong capability: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = client.CountTokens(ctx, "input")
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTokenCountingUnsupported) {
				t.Fatalf("cancel became capability: %v", err)
			}
		})
	}
}

func TestDomainNativeTokenizerFailureNeverOffersFallback(t *testing.T) {
	for _, mode := range []string{"malformed", "http", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "malformed":
					w.Write([]byte(`{"tokens":[null]}`))
				case "http":
					http.Error(w, "native token counting unsupported", 500)
				case "deadline":
					select {
					case <-r.Context().Done():
					case <-release:
					}
				}
			}))
			defer srv.Close()
			defer close(release)
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			_, err := New(srv.URL, "fixture").WithRuntime(RuntimeRulith).CountTokens(ctx, "input")
			if err == nil || errors.Is(err, ErrTokenCountingUnsupported) {
				t.Fatalf("native failure became capability: %v", err)
			}
		})
	}
}
