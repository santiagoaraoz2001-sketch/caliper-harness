package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func generationParams(t *testing.T, n int) Params {
	t.Helper()
	var p Params
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"MaxOutputTokens":%d,"Thinking":true}`, n)), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestGenerationBudgetWireAndUnset(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		for _, stream := range []bool{false, true} {
			for _, cap := range []int{2048, 0} {
				t.Run(fmt.Sprintf("%s/stream=%v/cap=%d", runtime, stream, cap), func(t *testing.T) {
					var body map[string]any
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/api/show" {
							io.WriteString(w, `{"capabilities":["completion","thinking"],"thinking":{"values":[true,false]}}`)
							return
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if runtime == RuntimeOllama {
							io.WriteString(w, nativeOK+"\n")
							return
						}
						if stream {
							io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
							return
						}
						io.WriteString(w, `{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}]}`)
					}))
					defer srv.Close()
					p := generationParams(t, cap)
					p.Thinking = stream
					c := New(srv.URL, "chosen").WithRuntime(runtime).WithParams(p)
					var err error
					if stream {
						_, _, _, err = c.ChatStream(context.Background(), nil, nil)
					} else {
						_, _, _, err = c.Chat(context.Background(), nil)
					}
					if err != nil {
						t.Fatal(err)
					}
					var value any
					var has bool
					if runtime == RuntimeOllama {
						if opts, ok := body["options"].(map[string]any); ok {
							value, has = opts["num_predict"]
						}
					} else {
						value, has = body["max_completion_tokens"]
					}
					if (cap > 0 && (!has || value != float64(cap))) || (cap == 0 && has) {
						t.Fatalf("cap mapping absent/wrong: %#v", body)
					}
					if _, bad := body["max_tokens"]; bad {
						t.Fatal("conflicting legacy generation field")
					}
				})
			}
		}
	}
}
func TestGenerationBudgetNegativeNoForward(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", runtime, stream), func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, nativeOK) }))
				defer srv.Close()
				c := New(srv.URL, "chosen").WithRuntime(runtime).WithParams(generationParams(t, -1))
				var err error
				if stream {
					_, _, _, err = c.ChatStream(context.Background(), nil, nil)
				} else {
					_, _, _, err = c.Chat(context.Background(), nil)
				}
				if calls != 0 || err == nil || !strings.Contains(err.Error(), "params.max_output_tokens") {
					t.Fatalf("invalid cap forwarded/unlabelled: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestGenerationBudgetLengthPreservesEvidence(t *testing.T) {
	partial := "```tool\ntool: bash\ncmd: echo must-not-execute\n```"
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", runtime, stream), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/show" {
						io.WriteString(w, `{"capabilities":["completion"]}`)
						return
					}
					if runtime == RuntimeOllama {
						json.NewEncoder(w).Encode(map[string]any{"model": "served", "message": map[string]string{"content": partial}, "done": true, "done_reason": "length", "prompt_eval_count": 7, "eval_count": 32})
						return
					}
					if stream {
						chunk := map[string]any{"model": "served", "choices": []any{map[string]any{"delta": map[string]string{"content": partial}, "finish_reason": "length"}}, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 32}}
						b, _ := json.Marshal(chunk)
						fmt.Fprintf(w, "data: %s\n\n", b)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"model": "served", "choices": []any{map[string]any{"message": map[string]string{"content": partial}, "finish_reason": "length"}}, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 32}})
				}))
				defer srv.Close()
				c := New(srv.URL, "chosen").WithRuntime(runtime).WithParams(generationParams(t, 32))
				var text, served string
				var u Usage
				var err error
				if stream {
					text, served, u, err = c.ChatStream(context.Background(), nil, nil)
				} else {
					text, served, u, err = c.Chat(context.Background(), nil)
				}
				if err == nil || text != partial || served != "served" || !u.Reported || u.In != 7 || u.Out != 32 || !strings.Contains(err.Error(), "params.max_output_tokens=32 including reasoning") {
					t.Fatalf("partial/evidence/cap diagnostic lost: %q %q %+v %v", text, served, u, err)
				}
			})
		}
	}
}
func TestGenerationBudgetBareAndNotice(t *testing.T) {
	c, st := stubClient(t, 200, `{"choices":[{"message":{"content":"answer"}}]}`)
	if _, _, _, err := c.Chat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	json.Unmarshal(st.seenBody, &b)
	if _, has := b["max_completion_tokens"]; has {
		t.Fatal("bare client gained an implicit budget")
	}
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		notice := RequestedControlNotice(runtime, generationParams(t, 0))
		if !strings.Contains(notice, "max_output_tokens 0") || !strings.Contains(notice, "runtime generation policy unknown, not unlimited") {
			t.Fatalf("zero described as known/unlimited: %s", notice)
		}
	}
}
