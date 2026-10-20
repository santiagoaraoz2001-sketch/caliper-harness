package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/llm"
	"strings"
	"testing"
	"time"
)

func TestChatJudgeStreamsWorkerProtocols(t *testing.T) {
	for _, runtime := range []string{"openai", "mlx", "ollama", "rulith"} {
		t.Run(runtime, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				want := "/v1/chat/completions"
				if runtime == "ollama" {
					want = "/api/chat"
				}
				if r.URL.Path != want || req["stream"] != true || req["model"] != "m" {
					t.Errorf("request %s %+v", r.URL, req)
				}
				emitJudge(w, runtime, `{"pick":0,"option":"yes","confidence":0.8}`, "stop")
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "m", "auto", time.Second).WithSelection(Selection{Runtime: runtime, Model: "m", Device: "auto"})
			v, err := c.Judge(context.Background(), Request{Context: "evidence", Options: []string{"yes", "no"}})
			if err != nil || v.Option != "yes" || v.ConfidenceSource != "self_reported" || v.Device != "unknown" {
				t.Fatalf("%+v %v", v, err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}
func emitJudge(w http.ResponseWriter, runtime, content, finish string) {
	if runtime == "ollama" {
		json.NewEncoder(w).Encode(map[string]any{"model": "m", "message": map[string]string{"role": "assistant", "content": content}, "done": true, "done_reason": finish})
		return
	}
	b, _ := json.Marshal(map[string]any{"model": "m", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": content}, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
}
func TestChatJudgeRefusesIncompleteOrMalformed(t *testing.T) {
	for _, tc := range []struct{ name, body, finish string }{{"missing", `{"pick":0,"option":"yes"}`, "stop"}, {"null", `{"pick":0,"option":"yes","confidence":null}`, "stop"}, {"duplicate", `{"pick":1,"pick":0,"option":"yes","confidence":0.8}`, "stop"}, {"mismatch", `{"pick":1,"option":"yes","confidence":0.8}`, "stop"}, {"invalid confidence", `{"pick":0,"option":"yes","confidence":2}`, "stop"}, {"truncated", `{"pick":0,"option":"yes","confidence":0.8}`, "length"}, {"thinking only", "", "stop"}, {"prose", "yes", "stop"}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { emitJudge(w, "openai", tc.body, tc.finish) }))
			defer srv.Close()
			c := New(srv.URL+"/v1", "m", "auto", time.Second).WithSelection(Selection{Runtime: "openai", Model: "m"})
			if v, err := c.Judge(context.Background(), Request{Context: "x", Options: []string{"yes", "no"}}); err == nil || v != (Verdict{}) {
				t.Fatalf("%+v %v", v, err)
			}
		})
	}
}
func TestChatJudgeBudgetCancelAndCatalog(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[{"id":"m"}]}`)
			return
		}
		emitJudge(w, "openai", `{"pick":0,"option":"yes","confidence":0.8}`, "stop")
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1", "m", "gpu", time.Second).WithSelection(Selection{Runtime: "openai", Model: "m", Device: "gpu"})
	h, err := c.Health(context.Background())
	if err != nil || h.Status != "model_available" || h.IsReady() || h.Active != nil || h.Loaded != nil {
		t.Fatalf("%+v %v", h, err)
	}
	r := Request{Context: "x", Options: []string{"yes", "no"}, MaxBytes: 1}
	var capacity *ContextCapacityError
	if _, err := c.Judge(context.Background(), r); !errors.As(err, &capacity) || !capacity.EncodedRequest || calls != 1 {
		t.Fatal("byte budget not enforced", calls, err)
	}
	r.MaxBytes = 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Judge(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestChatJudgeCompleteBodyLimit(t *testing.T) {
	for _, runtime := range []string{"openai", "mlx", "ollama", "rulith"} {
		t.Run(runtime, func(t *testing.T) {
			calls, actual := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				actual = len(body)
				emitJudge(w, runtime, `{"pick":0,"option":"yes","confidence":0.8}`, "stop")
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "m", "auto", time.Second).WithSelection(Selection{Runtime: runtime, Model: "m"})
			req := Request{Context: "Unicode é <body>", Instructions: "complete guidance", Options: []string{"yes", "no"}}
			if _, err := c.Judge(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			req.MaxBytes = actual - 1
			var capacity *ContextCapacityError
			if _, err := c.Judge(t.Context(), req); !errors.As(err, &capacity) || !capacity.EncodedRequest || calls != 1 {
				t.Fatalf("over-limit body reached HTTP or lost typed capacity: %v calls=%d", err, calls)
			}
			req.MaxBytes = actual
			if _, err := c.Judge(t.Context(), req); err != nil || calls != 2 || actual != req.MaxBytes {
				t.Fatalf("exact body did not fit: %v calls=%d bytes=%d", err, calls, actual)
			}
		})
	}
}

func TestChatJudgeRejectsThinkingToolsAndBrokenStream(t *testing.T) {
	bodies := []string{
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"considering\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"one\",\"type\":\"function\",\"function\":{\"name\":\"effect\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
		"data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"{}\"}}]}\n\n",
	}
	for i, body := range bodies {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			c := New(srv.URL+"/v1", "m", "auto", time.Second).WithSelection(Selection{Runtime: "openai", Model: "m"})
			if v, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"yes", "no"}}); err == nil || v != (Verdict{}) {
				t.Fatalf("%+v %v", v, err)
			}
		})
	}
}

func TestChatJudgePreservesCustomAPIBaseAndConfidenceLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if r.URL.Path != "/custom/openai/models" {
				t.Error(r.URL.Path)
			}
			fmt.Fprint(w, `{"data":[{"id":"m"}]}`)
			return
		}
		if r.URL.Path != "/custom/openai/chat/completions" {
			t.Error(r.URL.Path)
		}
		emitJudge(w, "openai", `{"pick":0,"option":"yes","confidence":0.8}`, "stop")
	}))
	defer srv.Close()
	c := New(srv.URL+"/custom/openai", "m", "auto", time.Second).WithSelection(Selection{Runtime: "openai", Model: "m"})
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := Request{Context: "facts", Options: []string{"yes", "no"}}
	v, err := c.Judge(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	recorded := ""
	LogJudgment(func(kind, text string) {
		if kind == KindJudgment {
			recorded = text
		}
	}, "test", r, v, "accepted", 0)
	if !strings.Contains(recorded, "confidence_source=self_reported") {
		t.Fatal(recorded)
	}
	if _, ok := ParseJudgment(recorded); !ok {
		t.Fatal("new confidence provenance broke parsing", recorded)
	}
}

func TestChatJudgeUsesConfiguredGenerationControls(t *testing.T) {
	for _, runtime := range []string{"openai", "mlx", "ollama", "rulith"} {
		t.Run(runtime, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/show" {
					fmt.Fprint(w, `{"capabilities":["completion","thinking"],"thinking":{"values":[false,true]}}`)
					return
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if runtime == "ollama" {
					var options map[string]json.RawMessage
					json.Unmarshal(body["options"], &options)
					if string(options["num_predict"]) != "32768" || string(body["think"]) != "false" {
						t.Errorf("controls %s", body)
					}
				} else {
					if string(body["max_completion_tokens"]) != "32768" {
						t.Errorf("output %s", body)
					}
					if runtime == "mlx" {
						var kwargs map[string]bool
						json.Unmarshal(body["chat_template_kwargs"], &kwargs)
						value, ok := kwargs["enable_thinking"]
						if !ok || value {
							t.Errorf("thinking %s", body)
						}
					}
				}
				emitJudge(w, runtime, `{"pick":0,"option":"yes","confidence":0.8}`, "stop")
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "m", "auto", time.Second).WithSelection(Selection{Runtime: runtime, Model: "m"}).WithParams(llm.Params{MaxOutputTokens: 32768, Thinking: false})
			if _, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"yes", "no"}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatParamsLeaveNativeJudgeWireUnchanged(t *testing.T) {
	st := &stubTransport{status: 200, body: `{"pick":0,"option":"yes","confidence":0.8,"model":"m","device":"cpu"}`}
	c := stubClient(st).WithParams(llm.Params{MaxOutputTokens: 32768, Thinking: true})
	if _, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"yes", "no"}}); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	json.Unmarshal(st.seenBody, &body)
	for _, key := range []string{"messages", "max_completion_tokens", "max_tokens", "temperature", "thinking", "think"} {
		if _, ok := body[key]; ok {
			t.Errorf("chat field %s leaked to native wire", key)
		}
	}
}
