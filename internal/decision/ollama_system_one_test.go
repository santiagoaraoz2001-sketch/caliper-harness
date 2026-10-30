package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOllamaSystemOneContract(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Model, State string
			Questions    map[string]struct {
				Type, Instructions string
				Criteria           map[string]string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		q := body.Questions["decision"]
		if r.URL.Path != "/v1/systemone" || body.Model != "d1:3b" || body.State != "evidence" || q.Type != "choice" || q.Instructions != "choose" || q.Criteria["0"] != "yes" || q.Criteria["1"] != "no" {
			t.Errorf("unexpected request: %s %+v", r.URL, body)
		}
		fmt.Fprint(w, `{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":0.8,"1":0.2},"confidence":0.6}},"usage":{"input_tokens":12,"output_tokens":0}}`)
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1", "d1:3b", "auto", time.Second).WithSelection(Selection{Runtime: "ollama", Protocol: "system_one", Model: "d1:3b"})
	req := Request{Context: "evidence", Instructions: "choose", Options: []string{"yes", "no"}}
	v, err := c.Judge(t.Context(), req)
	if err != nil || v.Pick != 0 || v.Option != "yes" || v.Confidence != .8 || v.ConfidenceSource != "native_probability" || v.Device != "unknown" {
		t.Fatalf("%+v %v", v, err)
	}
	req.Context = strings.Repeat("x", 70000)
	var capacity *ContextCapacityError
	if _, err = c.Judge(t.Context(), req); !errors.As(err, &capacity) || !capacity.EncodedRequest || calls != 1 {
		t.Fatalf("provider limit not predicted locally: calls=%d err=%v", calls, err)
	}
	req.Context = "evidence"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = c.Judge(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOllamaSystemOneRejectsInvalidEvidence(t *testing.T) {
	for _, body := range []string{
		`{"model":"wrong","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":0.8,"1":0.2}}}}`,
		`{"model":"d1:3b","answers":{"wrong":{"type":"choice","choice":"0","probabilities":{"0":0.8,"1":0.2}}}}`,
		`{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"2","probabilities":{"0":0.8,"1":0.2}}}}`,
		`{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":0.8}}}}`,
		`{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":0.2,"1":0.8}}}}`,
		`{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":0.8,"1":0.8}}}}`,
		`{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":1.1,"1":-0.1}}}}`,
		`{"model":"d1:3b","model":"wrong","answers":{}}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			c := New(srv.URL, "d1:3b", "auto", time.Second).WithSelection(Selection{Runtime: "ollama", Protocol: "system_one"})
			if v, err := c.Judge(t.Context(), Request{Context: "evidence", Options: []string{"yes", "no"}}); err == nil || v != (Verdict{}) {
				t.Fatalf("%+v %v", v, err)
			}
		})
	}
}

func TestOllamaSystemOneLive(t *testing.T) {
	server := os.Getenv("CALIPER_TEST_OLLAMA_D1")
	if server == "" {
		t.Skip("set CALIPER_TEST_OLLAMA_D1 for an installed Ollama d1:3b integration check")
	}
	c := New(server, "d1:3b", "auto", 120*time.Second).WithSelection(Selection{Runtime: "ollama", Protocol: "system_one", Model: "d1:3b"})
	v, err := c.Judge(t.Context(), Request{Context: "The build completed successfully and all 12 tests passed.", Instructions: "Classify the result.", Options: []string{"pass", "fail"}})
	if err != nil || v.Option != "pass" || v.ConfidenceSource != "native_probability" {
		t.Fatalf("%+v %v", v, err)
	}
	t.Logf("validated live D1 verdict: %+v", v)
}
