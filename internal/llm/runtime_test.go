package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const nativeOK = `{"model":"served","message":{"content":"answer","thinking":"private \u0060\u0060\u0060tool\\nBash"},"done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":3}`

func TestRuntimeMLXNestedThinkingPlainAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, thinking := range []bool{false, true} {
			body := `{"choices":[{"message":{"content":"answer"}}]}`
			if stream {
				body = "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n"
			}
			c, st := stubClient(t, 200, body)
			c.WithRuntime(RuntimeMLX).WithParams(Params{Thinking: thinking, Temperature: 0, TopK: 0, RepetitionPenalty: 1})
			var err error
			if stream {
				_, _, _, err = c.ChatStream(context.Background(), nil, nil)
			} else {
				_, _, _, err = c.Chat(context.Background(), nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			var r map[string]any
			_ = json.Unmarshal(st.seenBody, &r)
			if r["temperature"] != float64(0) || r["top_k"] != float64(0) || r["repetition_penalty"] != float64(1) || r["chat_template_kwargs"].(map[string]any)["enable_thinking"] != thinking {
				t.Fatalf("mapping lost false/zero: %s", st.seenBody)
			}
			if _, ok := r["enable_thinking"]; ok {
				t.Fatalf("obsolete field sent: %s", st.seenBody)
			}
			status := c.ParameterStatus()
			if status[3].Support != "unknown" || !strings.Contains(c.ControlNotice(), "template") {
				t.Fatalf("thinking falsely confirmed: %+v", status)
			}
		}
	}
}
func TestRuntimeNativeMappingAndMetadata(t *testing.T) {
	for _, suffix := range []string{"", "/v1"} {
		for _, stream := range []bool{false, true} {
			for _, thinking := range []bool{false, true} {
				requests := 0
				shows := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/show" {
						shows++
						var b map[string]any
						_ = json.NewDecoder(r.Body).Decode(&b)
						if b["model"] != "chosen" {
							t.Errorf("metadata wrong model: %v", b)
						}
						io.WriteString(w, `{"capabilities":["completion","thinking"],"thinking":{"values":[false,true]}}`)
						return
					}
					if r.URL.Path != "/api/chat" {
						t.Errorf("fallback or wrong dialect: %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					requests++
					var b map[string]any
					_ = json.NewDecoder(r.Body).Decode(&b)
					if b["think"] != thinking || b["stream"] != stream || b["model"] != "chosen" {
						t.Errorf("native flags: %v", b)
					}
					o := b["options"].(map[string]any)
					if o["temperature"] != float64(0) || o["top_k"] != float64(0) || o["repeat_penalty"] != float64(1) {
						t.Errorf("native sampler fields: %v", o)
					}
					for _, key := range []string{"temperature", "enable_thinking", "top_k", "repetition_penalty", "chat_template_kwargs", "stream_options"} {
						if _, ok := b[key]; ok {
							t.Errorf("compat field %s leaked", key)
						}
					}
					io.WriteString(w, nativeOK+"\n")
				}))
				c := New(srv.URL+suffix, "chosen").WithRuntime(RuntimeOllama).WithParams(Params{Thinking: thinking, Temperature: 0, TopK: 0, RepetitionPenalty: 1})
				if !strings.Contains(c.ControlNotice(), "metadata") {
					t.Fatal("unobserved model falsely supported")
				}
				for i := 0; i < 2; i++ {
					var reply, served string
					var u Usage
					var err error
					var seen strings.Builder
					if stream {
						reply, served, u, err = c.ChatStream(context.Background(), nil, func(s string) { seen.WriteString(s) })
					} else {
						reply, served, u, err = c.Chat(context.Background(), nil)
					}
					if err != nil || reply != "answer" || served != "served" || u != (Usage{7, 3, true}) {
						t.Fatalf("native decode/identity/usage: %q %q %+v %v", reply, served, u, err)
					}
					if stream && seen.String() != "answer" {
						t.Fatalf("thinking leaked into callbacks: %q", seen.String())
					}
				}
				srv.Close()
				if requests != 2 || shows != 1 || c.ParameterStatus()[3].Support != "supported" {
					t.Fatalf("metadata/requests/status: %d %d %+v", requests, shows, c.ParameterStatus())
				}
			}
		}
	}
}
func TestRuntimeThinkingMetadataRefusalsAreDisclosedAndOmitted(t *testing.T) {
	cases := []struct {
		name, body, support string
		status              int
	}{
		{"capability only", `{"capabilities":["thinking"]}`, "unknown", 200},
		{"not thinking", `{"capabilities":["completion"]}`, "unsupported", 200},
		{"named levels", `{"capabilities":["thinking"],"thinking":{"values":["low","high"]}}`, "unsupported", 200},
		{"only true", `{"capabilities":["thinking"],"thinking":{"values":[true]}}`, "unsupported", 200},
		{"empty", `{}`, "unknown", 200}, {"malformed", `{bad`, "unknown", 200},
		{"error", `{"error":"refused"}`, "unknown", 200}, {"trailing", `{"thinking":{"values":[false,true]}} {}`, "unknown", 200},
		{"unavailable", `oops`, "unknown", 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/show" {
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body)
					return
				}
				posts++
				var b map[string]any
				_ = json.NewDecoder(r.Body).Decode(&b)
				if _, ok := b["think"]; ok {
					t.Errorf("unadmitted false sent: %v", b)
				}
				io.WriteString(w, nativeOK)
			}))
			defer srv.Close()
			c := New(srv.URL, "m").WithRuntime(RuntimeOllama)
			off := false
			c.Thinking = &off
			if _, _, _, err := c.Chat(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			status := c.ParameterStatus()
			if posts != 1 || status[0].Support != tc.support || !strings.Contains(c.ControlNotice(), "not applied") {
				t.Fatalf("false supported/applied: %+v", status)
			}
		})
	}
}
func TestRuntimeUnsetNativeDoesNotFetchOrSendOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unset metadata request: %s", r.URL.Path)
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		for _, key := range []string{"think", "options"} {
			if _, ok := b[key]; ok {
				t.Errorf("unset %s sent", key)
			}
		}
		io.WriteString(w, nativeOK)
	}))
	defer srv.Close()
	if _, _, _, err := New(srv.URL, "m").WithRuntime(RuntimeOllama).Chat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeInvalidRefusesBeforeHTTP(t *testing.T) {
	c, st := stubClient(t, 200, `{}`)
	c.WithRuntime("guessed")
	for _, stream := range []bool{false, true} {
		var err error
		if stream {
			_, _, _, err = c.ChatStream(context.Background(), nil, nil)
		} else {
			_, _, _, err = c.Chat(context.Background(), nil)
		}
		if err == nil || st.seenPath != "" {
			t.Fatalf("invalid runtime posted: %s %v", st.seenPath, err)
		}
	}
}
func TestRuntimeNativeIncompletePlainAndNDJSON(t *testing.T) {
	partial := `{"model":"served","message":{"content":"partial"},"prompt_eval_count":7,"eval_count":3}`
	for _, stream := range []bool{false, true} {
		cases := []struct{ name, body, want string }{
			{"empty", "", ""}, {"missing done", partial, "partial"}, {"empty done", `{"done":true}`, ""},
			{"reasoning only", `{"message":{"thinking":"\u0060\u0060\u0060tool\\nBash"},"done":true}`, ""},
			{"length", strings.TrimSuffix(partial, "}") + `,"done":true,"done_reason":"length"}`, "partial"},
			{"server error", strings.TrimSuffix(partial, "}") + `,"error":"engine stopped"}`, "partial"},
			{"malformed", `{bad`, ""}, {"null", `null`, ""},
			{"trailing garbage", nativeOK + "\ntrailing", "answer"}, {"after done", nativeOK + "\n" + partial, "answer"},
			{"thinking after done", nativeOK + "\n" + `{"message":{"thinking":"hidden"}}`, "answer"},
		}
		for _, tc := range cases {
			t.Run(tc.name+strconv.FormatBool(stream), func(t *testing.T) {
				c, _ := stubClient(t, 200, tc.body)
				c.WithRuntime(RuntimeOllama)
				var reply string
				var err error
				var seen strings.Builder
				if stream {
					reply, _, _, err = c.ChatStream(context.Background(), nil, func(s string) { seen.WriteString(s) })
				} else {
					reply, _, _, err = c.Chat(context.Background(), nil)
				}
				if err == nil || reply != tc.want || !strings.Contains(err.Error(), "http://fake:9/v1") {
					t.Fatalf("refusal lost/partial changed: %q want%q %v", reply, tc.want, err)
				}
				if stream && seen.String() != tc.want {
					t.Fatalf("callback changed: %q", seen.String())
				}
			})
		}
	}
}
func TestRuntimeNativeDoneCannotHideReadFailure(t *testing.T) {
	for _, stream := range []bool{false, true} {
		c := New("http://fixture.invalid/v1", "m").WithRuntime(RuntimeOllama)
		c.http = &http.Client{Transport: bodyErrorTransport{&failingBody{strings.NewReader(nativeOK + "\n")}}}
		var reply, served string
		var u Usage
		var err error
		if stream {
			reply, served, u, err = c.ChatStream(context.Background(), nil, nil)
		} else {
			reply, served, u, err = c.Chat(context.Background(), nil)
		}
		if !errors.Is(err, io.ErrUnexpectedEOF) || reply != "answer" || served != "served" || u != (Usage{7, 3, true}) {
			t.Fatalf("read error hidden/evidence lost: %q %q %+v %v", reply, served, u, err)
		}
	}
}
func TestRuntimeNativeBrokenDeclaredLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(nativeOK)+80))
		io.WriteString(w, nativeOK)
	}))
	defer srv.Close()
	for _, stream := range []bool{false, true} {
		c := New(srv.URL, "m").WithRuntime(RuntimeOllama)
		var reply string
		var err error
		if stream {
			reply, _, _, err = c.ChatStream(context.Background(), nil, nil)
		} else {
			reply, _, _, err = c.Chat(context.Background(), nil)
		}
		if !errors.Is(err, io.ErrUnexpectedEOF) || reply != "answer" {
			t.Fatalf("declared body failure became success: %q %v", reply, err)
		}
	}
}
func TestRuntimeTransportKeepsCancellationCause(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := New("http://fixture.invalid", "m").WithRuntime(runtime)
		if _, _, _, err := c.Chat(ctx, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s loses cancellation: %v", runtime, err)
		}
	}
}

func TestRuntimeNativeHTTPRefusalNoFallback(t *testing.T) {
	for _, stream := range []bool{false, true} {
		c, st := stubClient(t, 503, `{"error":"worker unavailable"}`)
		c.WithRuntime(RuntimeOllama)
		var err error
		if stream {
			_, _, _, err = c.ChatStream(context.Background(), nil, nil)
		} else {
			_, _, _, err = c.Chat(context.Background(), nil)
		}
		if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "worker unavailable") || st.seenPath != "/api/chat" {
			t.Fatalf("native refusal hidden/fallback: %s %v", st.seenPath, err)
		}
	}
}
func TestRuntimeNativeOversizeAndScannerErrorsRefuse(t *testing.T) {
	c, _ := stubClient(t, 200, nativeOK+strings.Repeat(" ", 32<<20))
	c.WithRuntime(RuntimeOllama)
	reply, served, _, err := c.Chat(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "transport limit") || reply != "answer" || served != "served" {
		t.Fatalf("plain cap hides truncation: %q %q %v", reply, served, err)
	}
	c, _ = stubClient(t, 200, `{"model":"served","message":{"content":"partial"}}`+"\n"+strings.Repeat("x", 1024*1024+1))
	c.WithRuntime(RuntimeOllama)
	reply, served, _, err = c.ChatStream(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "broke") || reply != "partial" || served != "served" {
		t.Fatalf("scanner failure hidden: %q %q %v", reply, served, err)
	}
}
func TestRuntimeNativeMetadataBindsNewSelection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["model"] == "admitted" {
				io.WriteString(w, `{"thinking":{"values":[false,true]}}`)
			} else {
				io.WriteString(w, `{"capabilities":["completion"]}`)
			}
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		_, sent := b["think"]
		if sent != (b["model"] == "admitted") {
			t.Errorf("stale capability from other selection: %v", b)
		}
		io.WriteString(w, nativeOK)
	}))
	defer srv.Close()
	for _, model := range []string{"admitted", "other"} {
		c := New(srv.URL, model).WithRuntime(RuntimeOllama)
		off := false
		c.Thinking = &off
		if _, _, _, err := c.Chat(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRuntimeConcurrentMetadataWaitObservesCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var shows, posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			shows.Add(1)
			close(started)
			<-release
			io.WriteString(w, `{"thinking":{"values":[false,true]}}`)
			return
		}
		posts.Add(1)
		io.WriteString(w, nativeOK)
	}))
	defer srv.Close()
	c := New(srv.URL, "m").WithRuntime(RuntimeOllama)
	off := false
	c.Thinking = &off
	first := make(chan error, 1)
	go func() { _, _, _, err := c.Chat(context.Background(), nil); first <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { _, _, _, err := c.Chat(ctx, nil); second <- err }()
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("wait lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("cancelled metadata waiter blocked on another request")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if shows.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("metadata duplicated/cancelled generation posted: show%d chat%d", shows.Load(), posts.Load())
	}
}

func TestRuntimeGenericOmitsUnconfirmedExtensions(t *testing.T) {
	c, st := stubClient(t, 200, `{"choices":[{"message":{"content":"hi"}}]}`)
	c.WithParams(Params{Temperature: 0, TopK: 10, RepetitionPenalty: 1.2, Thinking: false})
	if _, _, _, err := c.Chat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(st.seenBody, &got); err != nil {
		t.Fatal(err)
	}
	if got["temperature"] != float64(0) {
		t.Fatalf("explicit zero lost: %s", st.seenBody)
	}
	for _, key := range []string{"top_k", "repetition_penalty", "enable_thinking"} {
		if _, ok := got[key]; ok {
			t.Errorf("generic unsupported extension %s sent: %s", key, st.seenBody)
		}
	}
}
func TestRuntimeNativeAfterDoneRefusalNamesCause(t *testing.T) {
	c, _ := stubClient(t, 200, nativeOK+"\n"+`{"error":"post-done worker failure"}`+"\n")
	c.WithRuntime(RuntimeOllama)
	reply, _, _, err := c.ChatStream(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "post-done worker failure") || reply != "answer" {
		t.Fatalf("post-terminal refusal cause/evidence lost: %q %v", reply, err)
	}
}

func TestRuntimeNativePreservesHistoryWithoutTruncateOrShift(t *testing.T) {
	for _, mode := range []string{"plain", "stream", "text-tools", "native-tools"} {
		t.Run(mode, func(t *testing.T) {
			msgs := []Message{{Role: "system", Content: "protected policy β\n"}, {Role: "user", Content: "first request"}, {Role: "assistant", Content: "retained observation"}, {Role: "user", Content: "next request"}}
			if mode == "native-tools" {
				msgs[2] = Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "owned-call", Name: "bash", Arguments: `{"cmd":"printf β"}`}}}
				msgs = append(msgs[:3], Message{Role: "tool", ToolCallID: "owned-call", Name: "bash", Content: "actual retained result"}, msgs[3])
			}
			want, err := json.Marshal(nativeMessages(msgs))
			if err != nil {
				t.Fatal(err)
			}
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/show" {
					io.WriteString(w, `{"capabilities":["tools","thinking"],"thinking":{"values":[false,true]}}`)
					return
				}
				posts++
				var b map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
					t.Error(err)
					return
				}
				for _, key := range []string{"truncate", "shift"} {
					if string(b[key]) != "false" {
						t.Errorf("%s must explicitly refuse history loss; got %s", key, b[key])
					}
				}
				got, _ := json.Marshal(json.RawMessage(b["messages"]))
				if string(got) != string(want) {
					t.Errorf("history changed: %s != %s", got, want)
				}
				var opts map[string]any
				_ = json.Unmarshal(b["options"], &opts)
				if opts["num_predict"] != float64(32768) {
					t.Errorf("output ceiling changed: %v", opts)
				}
				if _, ok := opts["num_ctx"]; ok {
					t.Error("invented context override", opts)
				}
				if mode == "native-tools" {
					io.WriteString(w, `{"model":"served","message":{"tool_calls":[{"function":{"name":"bash","arguments":{"cmd":"next"}}}]},"done":true,"done_reason":"stop"}`+"\n")
				} else {
					io.WriteString(w, nativeOK+"\n")
				}
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "chosen").WithRuntime(RuntimeOllama).WithParams(Params{Thinking: true, MaxOutputTokens: 32768})
			switch mode {
			case "plain":
				text, _, _, e := c.Chat(t.Context(), msgs)
				if e != nil || text != "answer" {
					t.Fatalf("%q %v", text, e)
				}
			case "stream":
				var seen strings.Builder
				text, _, _, e := c.ChatStream(t.Context(), msgs, func(s string) { seen.WriteString(s) })
				if e != nil || text != "answer" || seen.String() != text {
					t.Fatalf("%q %q %v", text, seen.String(), e)
				}
			default:
				if mode == "text-tools" {
					c.WithToolProtocol("text")
				}
				reply, e := c.ChatTools(t.Context(), msgs, []ToolDefinition{fixtureTool()}, nil)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "text-tools" && reply.Message.Content != "answer" || mode == "native-tools" && (len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].Arguments != `{"cmd":"next"}`) {
					t.Fatalf("reply lost %+v", reply)
				}
			}
			if posts != 1 {
				t.Fatal("unexpected generation count", posts)
			}
		})
	}
}
