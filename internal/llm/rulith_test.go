package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func nativeEvent(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func nativeTerminal(prompt, stop string, out int) map[string]any {
	return map[string]any{"content": "", "stop": true, "stop_type": stop, "prompt": prompt, "model": "qwen", "tokens_evaluated": 27, "tokens_predicted": out, "truncated": false}
}

func rulithTestTools() []ToolDefinition {
	tool := ToolDefinition{Type: "function"}
	tool.Function.Name = "record"
	tool.Function.Parameters = map[string]any{"type": "object"}
	return []ToolDefinition{tool}
}

func TestRulithContinuesLiteralBytesWithoutChatOrSyntheticMessages(t *testing.T) {
	original := []Message{{Role: "system", Content: "original rules"}, {Role: "user", Content: "record café 🚚"}}
	tools := []ToolDefinition{{Type: "function"}}
	tools[0].Function.Name = "record"
	tools[0].Function.Parameters = map[string]any{"type": "object"}
	prompt := "<|im_start|>assistant\n<think>\n\n</think>\n\n"
	parts := []string{"<tool_call>\n<function=record>\n<parameter=text>\ncafé ", "🚚 literal <tool_call> in code\n", "</parameter>\n</function>\n</tool_call>"}
	var requests []string
	renders := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/apply-template":
			renders++
			var got []Message
			json.Unmarshal(body["messages"], &got)
			if !reflect.DeepEqual(got, original) || len(body["tools"]) == 0 {
				t.Errorf("changed template input: %s", body)
			}
			json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
		case "/completion":
			var p string
			json.Unmarshal(body["prompt"], &p)
			i := len(requests)
			requests = append(requests, p)
			if p != prompt+strings.Join(parts[:i], "") {
				t.Errorf("request %d changed prefix: %q", i, p)
			}
			if string(body["n_predict"]) != "16" || string(body["stream"]) != "true" || body["messages"] != nil {
				t.Errorf("native request mismatch: %s", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			// Unreliable native token IDs must never be promoted to token replay.
			nativeEvent(w, map[string]any{"content": parts[i], "model": "served-qwen", "tokens": []int{123}})
			stop := "limit"
			if i == 2 {
				stop = "eos"
			}
			terminal := nativeTerminal(p, stop, 16)
			terminal["model"] = "served-qwen"
			nativeEvent(w, terminal)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1", "selected-qwen").WithRuntime(RuntimeRulith)
	limit := 16
	c.MaxOutputTokens = &limit
	ctx := context.Background()
	var reply Reply
	for i := 0; i < 3; i++ {
		var err error
		reply, err = c.ChatTools(ctx, original, tools, nil)
		if reply.RawNative != strings.Join(parts[:i+1], "") || reply.Message.Role != "assistant" || reply.Message.Content != "" || len(reply.Message.ToolCalls) != 0 {
			t.Fatalf("raw assembly %d: %+v", i, reply)
		}
		if reply.Usage.Out != 16 || !reply.Usage.Reported {
			t.Fatalf("physical usage changed: %+v", reply.Usage)
		}
		if i < 2 {
			if err == nil || !reply.OutputLimit || reply.Prefix == nil || reply.Prefix.Validate() != nil {
				t.Fatalf("missing continuation: %+v %v", reply, err)
			}
			if len(reply.Prefix.Tokens) != 0 || reply.Prefix.GeneratedTokenCount() != (i+1)*16 || reply.Prefix.ResponseBytes() != len(reply.RawNative) {
				t.Fatalf("wrong prefix contract: %+v", reply.Prefix)
			}
			ctx = WithResponseContinuation(context.Background(), reply)
		} else if err != nil || reply.OutputLimit || reply.Prefix != nil {
			t.Fatalf("completion: %+v %v", reply, err)
		}
	}
	if renders != 1 || len(requests) != 3 {
		t.Fatalf("render/generation counts %d/%d", renders, len(requests))
	}
}

func TestRulithContinuationRejectsChangedOriginalRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected HTTP request") }))
	defer srv.Close()
	c := New(srv.URL, "qwen").WithRuntime(RuntimeRulith)
	msgs := []Message{{Role: "user", Content: "original"}}
	render := c.request(msgs, false)
	render.Tools = rulithTestTools()
	b, _ := json.Marshal(render)
	prefix := &ResponsePrefix{Strategy: RulithRawPrefix, Prompt: "original rendered", Raw: "partial", InitialState: "normal", GeneratedTokens: 3, RequestSHA256: byteHash(b), PromptSHA256: byteHash([]byte("original rendered"))}
	for _, alter := range []string{"messages", "raw", "prompt", "strategy"} {
		t.Run(alter, func(t *testing.T) {
			p := prefix.Clone()
			prior := Reply{Prefix: p, RawNative: p.Raw}
			input := append([]Message(nil), msgs...)
			switch alter {
			case "messages":
				input[0].Content = "changed"
			case "raw":
				prior.RawNative = "changed"
			case "prompt":
				p.Prompt += "changed"
			case "strategy":
				p.Strategy = "unknown"
			}
			r, err := c.ChatTools(WithResponseContinuation(context.Background(), prior), input, rulithTestTools(), nil)
			if err == nil || r.OutputLimit {
				t.Fatalf("accepted changed %s", alter)
			}
		})
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestRulithTaintedStreamsNeverGrantContinuation(t *testing.T) {
	for _, mode := range []string{"missing-terminal", "malformed", "prompt-mismatch", "context-shift", "missing-usage", "missing-model", "model-change", "after-terminal", "stop-word", "no-progress"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/apply-template" {
					json.NewEncoder(w).Encode(map[string]string{"prompt": "rendered"})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if mode != "no-progress" {
					nativeEvent(w, map[string]any{"content": "partial", "model": "qwen"})
				}
				end := nativeTerminal("rendered", "limit", 4)
				switch mode {
				case "missing-terminal":
					return
				case "malformed":
					fmt.Fprintln(w, "data: {broken}")
					return
				case "prompt-mismatch":
					end["prompt"] = "other"
				case "context-shift":
					end["truncated"] = true
				case "missing-usage":
					delete(end, "tokens_predicted")
				case "missing-model":
					delete(end, "model")
				case "model-change":
					end["model"] = "other"
				case "stop-word":
					end["stop_type"] = "word"
				}
				nativeEvent(w, end)
				if mode == "after-terminal" {
					nativeEvent(w, map[string]any{"content": "extra"})
				}
			}))
			defer srv.Close()
			r, err := New(srv.URL, "qwen").WithRuntime(RuntimeRulith).ChatTools(context.Background(), []Message{{Role: "user", Content: "go"}}, rulithTestTools(), nil)
			if err == nil || r.OutputLimit || r.Prefix != nil {
				t.Fatalf("tainted %s granted continuation: %+v %v", mode, r, err)
			}
			if mode != "no-progress" && r.RawNative != "partial" {
				t.Fatalf("lost partial evidence %q", r.RawNative)
			}
		})
	}
}

func TestRulithLeadingProseStreamsOnceAndHoldsSplitMarker(t *testing.T) {
	parts := [][]string{{"Hello café ", "<"}, {"tool_call>", "private args</tool_call>"}}
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apply-template" {
			json.NewEncoder(w).Encode(map[string]string{"prompt": "rendered"})
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range parts[count] {
			nativeEvent(w, map[string]any{"content": part})
		}
		stop := "limit"
		if count > 0 {
			stop = "eos"
		}
		count++
		nativeEvent(w, nativeTerminal(body.Prompt, stop, 3))
	}))
	defer srv.Close()
	c := New(srv.URL, "qwen").WithRuntime(RuntimeRulith)
	var visible strings.Builder
	first, err := c.ChatTools(context.Background(), nil, rulithTestTools(), func(s string) { visible.WriteString(s) })
	if err == nil || !first.OutputLimit {
		t.Fatalf("missing length %v", err)
	}
	last, err := c.ChatTools(WithResponseContinuation(context.Background(), first), nil, rulithTestTools(), func(s string) { visible.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if visible.String() != "Hello café " || last.RawContentStreamed != visible.String() {
		t.Fatalf("leaked or duplicated raw frame: %q %+v", visible.String(), last)
	}
}

func TestRulithCancellationPreservesRawWithoutContinuation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apply-template" {
			json.NewEncoder(w).Encode(map[string]string{"prompt": "<think>"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		nativeEvent(w, map[string]any{"content": "private reasoning"})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := New(srv.URL, "qwen").WithRuntime(RuntimeRulith)
	// Cancellation occurs after the transport has consumed the raw chunk.
	c.streamObserver = func(state string) {
		if state == "generating" {
			cancel()
		}
	}
	deadline := time.AfterFunc(2*time.Second, cancel)
	defer deadline.Stop()
	r, err := c.ChatTools(ctx, nil, rulithTestTools(), func(s string) { t.Errorf("reasoning leaked as prose %q", s) })
	if err == nil || r.OutputLimit || r.Prefix != nil || r.RawNative != "private reasoning" || r.NativeInitialState != "reasoning" {
		t.Fatalf("cancellation lost evidence or authorized continuation: %+v %v", r, err)
	}
}

func TestRulithObservationIndependentOfDisplayAndContinuation(t *testing.T) {
	for _, display := range []bool{false, true} {
		t.Run(fmt.Sprintf("display=%t", display), func(t *testing.T) {
			parts := [][]string{{"Hello ", "café "}, {"🚚 ", "ready <", "tool_call>private arguments</tool_call>"}}
			request := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/apply-template" {
					json.NewEncoder(w).Encode(map[string]string{"prompt": "rendered"})
					return
				}
				var body struct {
					Prompt string `json:"prompt"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, part := range parts[request] {
					nativeEvent(w, map[string]any{"content": part})
				}
				stop := "limit"
				if request > 0 {
					stop = "eos"
				}
				request++
				nativeEvent(w, nativeTerminal(body.Prompt, stop, 5))
			}))
			defer srv.Close()
			client := New(srv.URL, "qwen").WithRuntime(RuntimeRulith)
			var visible strings.Builder
			var onDelta func(string)
			if display {
				onDelta = func(s string) { visible.WriteString(s) }
			}
			var observed [2][]AuxDelta
			ctx := context.Background()
			var reply Reply
			for physical := 0; physical < 2; physical++ {
				i := physical
				observing := WithAuxObserver(ctx, func(_ context.Context, d AuxDelta) error { observed[i] = append(observed[i], d); return nil })
				var err error
				reply, err = client.ChatTools(observing, nil, rulithTestTools(), onDelta)
				if physical == 0 {
					if err == nil || !reply.OutputLimit {
						t.Fatalf("missing boundary: %+v %v", reply, err)
					}
					ctx = WithResponseContinuation(context.Background(), reply)
				} else if err != nil {
					t.Fatal(err)
				}
			}
			want := [2][]AuxDelta{{{Field: "content", Delta: "Hello "}, {Field: "content", Delta: "café "}}, {{Field: "content", Delta: "🚚 "}, {Field: "content", Delta: "ready "}}}
			if !reflect.DeepEqual(observed, want) {
				t.Fatalf("observer lost, duplicated or leaked bytes: %#v", observed)
			}
			wantVisible := ""
			if display {
				wantVisible = "Hello café 🚚 ready "
			}
			if visible.String() != wantVisible || reply.RawContentStreamed != wantVisible {
				t.Fatalf("display differs: %q / %q", visible.String(), reply.RawContentStreamed)
			}
		})
	}
}

func TestRulithHeadlessObserverFailureRetainsUnexecutedRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apply-template" {
			json.NewEncoder(w).Encode(map[string]string{"prompt": "rendered"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		nativeEvent(w, map[string]any{"content": "observed prefix"})
		nativeEvent(w, nativeTerminal("rendered", "limit", 5))
	}))
	defer srv.Close()
	ctx := WithAuxObserver(context.Background(), func(_ context.Context, d AuxDelta) error { return fmt.Errorf("observer failed") })
	r, err := New(srv.URL, "qwen").WithRuntime(RuntimeRulith).ChatTools(ctx, nil, rulithTestTools(), nil)
	if err == nil || !strings.Contains(err.Error(), "observer failed") || r.OutputLimit || r.Prefix != nil || r.RawNative != "observed prefix" || r.RawContentStreamed != "" {
		t.Fatalf("headless observer refusal lost: %+v %v", r, err)
	}
}

func TestRulithReasoningProjectionAcrossLimitsAndSplitClose(t *testing.T) {
	for _, initialReasoning := range []bool{false, true} {
		for _, display := range []bool{false, true} {
			t.Run(fmt.Sprintf("initialReasoning=%t/display=%t", initialReasoning, display), func(t *testing.T) {
				prompt, opening := "rendered", "<think>"
				if initialReasoning {
					prompt, opening = "rendered<think>\n", ""
				}
				parts := [][]string{{opening[:len(opening)/2], opening[len(opening)/2:] + "plan café "}, {"more 🚚 ", "</th"}, {"ink>Answer ", "< 3 and <b>ok</b> ", "<tool", "_call>private arguments</tool_call>"}}
				request := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/apply-template" {
						json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
						return
					}
					var body struct {
						Prompt string `json:"prompt"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					w.Header().Set("Content-Type", "text/event-stream")
					for _, part := range parts[request] {
						nativeEvent(w, map[string]any{"content": part})
					}
					stop := "limit"
					if request == 2 {
						stop = "eos"
					}
					request++
					nativeEvent(w, nativeTerminal(body.Prompt, stop, 5))
				}))
				defer srv.Close()
				client := New(srv.URL, "qwen").WithRuntime(RuntimeRulith)
				var observed [3][]AuxDelta
				var visible strings.Builder
				var onDelta func(string)
				if display {
					onDelta = func(s string) { visible.WriteString(s) }
				}
				ctx := context.Background()
				var reply Reply
				for physical := 0; physical < 3; physical++ {
					i := physical
					observing := WithAuxObserver(ctx, func(_ context.Context, d AuxDelta) error { observed[i] = append(observed[i], d); return nil })
					var err error
					reply, err = client.ChatTools(observing, nil, rulithTestTools(), onDelta)
					if physical < 2 {
						if err == nil || !reply.OutputLimit {
							t.Fatalf("boundary %d: %+v %v", physical, reply, err)
						}
						ctx = WithResponseContinuation(context.Background(), reply)
					} else if err != nil {
						t.Fatal(err)
					}
				}
				wantParts := []string{"plan café ", "more 🚚 ", "Answer < 3 and <b>ok</b> "}
				for physical, deltas := range observed {
					wantField := "reasoning_content"
					if physical == 2 {
						wantField = "content"
					}
					var got strings.Builder
					for _, d := range deltas {
						if d.Field != wantField {
							t.Fatalf("wrong field: %#v", observed)
						}
						got.WriteString(d.Delta)
					}
					if got.String() != wantParts[physical] {
						t.Fatalf("request %d replayed/lost/leaked bytes: %q", physical, got.String())
					}
				}
				wantVisible := ""
				if display {
					wantVisible = wantParts[2]
				}
				if visible.String() != wantVisible || reply.RawContentStreamed != wantVisible {
					t.Fatalf("display lost/duplicated/leaked: %q / %q", visible.String(), reply.RawContentStreamed)
				}
				if reply.RawNative != strings.Join(parts[0], "")+strings.Join(parts[1], "")+strings.Join(parts[2], "") {
					t.Fatal("projection changed raw transport")
				}
			})
		}
	}
}

func TestRulithReasoningObserverCancellationRetainsRaw(t *testing.T) {
	for _, initialReasoning := range []bool{false, true} {
		t.Run(fmt.Sprintf("initialReasoning=%t", initialReasoning), func(t *testing.T) {
			prompt, raw := "rendered", "<think>observed reasoning 🚚"
			if initialReasoning {
				prompt, raw = "rendered<think>", "observed reasoning 🚚"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/apply-template" {
					json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				nativeEvent(w, map[string]any{"content": raw})
				nativeEvent(w, nativeTerminal(prompt, "limit", 5))
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := 0
			ctx = WithAuxObserver(ctx, func(_ context.Context, d AuxDelta) error {
				if d.Field != "reasoning_content" || d.Delta != "observed reasoning 🚚" {
					t.Errorf("wrong reasoning observation: %+v", d)
				}
				observed++
				cancel()
				return nil
			})
			r, err := New(srv.URL, "qwen").WithRuntime(RuntimeRulith).ChatTools(ctx, nil, rulithTestTools(), nil)
			if err == nil || observed != 1 || r.OutputLimit || r.Prefix != nil || r.RawNative != raw || r.RawContentStreamed != "" {
				t.Fatalf("observer cancellation lost: %+v %v count=%d", r, err, observed)
			}
		})
	}
}

func TestRulithNonToolCallersRetainParsedChatStream(t *testing.T) {
	for _, mode := range []string{"nil-tools", "empty-tools", "text-with-tools"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("non-tool caller routed to %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["stream"] != true || body["model"] != "qwen" || body["tools"] != nil {
					t.Errorf("changed ordinary chat request: %+v", body)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				nativeEvent(w, map[string]any{"model": "qwen", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": "checked", "reasoning_content": "thought"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3}})
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()
			client := New(srv.URL+"/v1", "qwen").WithRuntime(RuntimeRulith)
			var tools []ToolDefinition
			switch mode {
			case "empty-tools":
				tools = []ToolDefinition{}
			case "text-with-tools":
				tools = rulithTestTools()
				client.WithToolProtocol("text")
			}
			observed := map[string]string{}
			ctx := WithAuxObserver(context.Background(), func(_ context.Context, d AuxDelta) error { observed[d.Field] += d.Delta; return nil })
			reply, err := client.ChatTools(ctx, []Message{{Role: "user", Content: "judge or summarize"}}, tools, nil)
			if err != nil || calls != 1 || reply.Message.Content != "checked" || reply.Message.ReasoningContent != "thought" || reply.RawNative != "" || reply.Prefix != nil || reply.OutputLimit || reply.FinishReason != "stop" || reply.Served != "qwen" || reply.Usage != (Usage{In: 10, Out: 3, Reported: true}) {
				t.Fatalf("ordinary caller contract changed: calls=%d %+v %v", calls, reply, err)
			}
			if observed["content"] != "checked" || observed["reasoning_content"] != "thought" {
				t.Fatalf("ordinary streaming observation lost: %+v", observed)
			}
		})
	}
}

func TestRulithContinuationCannotFallThroughAfterToolOrProtocolChange(t *testing.T) {
	for _, change := range []string{"remove-tools", "empty-tools", "change-tools", "text-protocol", "mlx-runtime"} {
		t.Run(change, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Path {
				case "/apply-template":
					json.NewEncoder(w).Encode(map[string]string{"prompt": "original rendered"})
				case "/completion":
					w.Header().Set("Content-Type", "text/event-stream")
					nativeEvent(w, map[string]any{"content": "<tool_call>unfinished"})
					nativeEvent(w, nativeTerminal("original rendered", "limit", 5))
				default:
					t.Errorf("raw continuation fell through to %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			client := New(srv.URL+"/v1", "qwen").WithRuntime(RuntimeRulith)
			msgs := []Message{{Role: "user", Content: "original request"}}
			tools := rulithTestTools()
			first, err := client.ChatTools(context.Background(), msgs, tools, nil)
			if err == nil || !first.OutputLimit || first.Prefix == nil || calls != 2 {
				t.Fatalf("no initial raw prefix: calls=%d %+v %v", calls, first, err)
			}
			switch change {
			case "remove-tools":
				tools = nil
			case "empty-tools":
				tools = []ToolDefinition{}
			case "change-tools":
				tools = rulithTestTools()
				tools[0].Function.Name = "other"
			case "text-protocol":
				client.WithToolProtocol("text")
			case "mlx-runtime":
				client.WithRuntime(RuntimeMLX)
			}
			reply, err := client.ChatTools(WithResponseContinuation(context.Background(), first), msgs, tools, nil)
			if err == nil || reply.OutputLimit || calls != 2 {
				t.Fatalf("changed owner made HTTP or continued: calls=%d %+v %v", calls, reply, err)
			}
		})
	}
}
