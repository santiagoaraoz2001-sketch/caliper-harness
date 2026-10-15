package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func chatEnvelope(content, finish string) string {
	b, _ := json.Marshal(map[string]any{"model": "m", "choices": []any{map[string]any{"finish_reason": finish, "message": map[string]any{"role": "assistant", "content": content}}}})
	return string(b)
}
func chatClient(url string) *Client {
	return New(url, "m", DeviceGPU, time.Second).WithSelection(Selection{Model: "m", Device: DeviceGPU, Runtime: "openai"})
}
func TestChatJudgeHTTP(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		if string(body["model"]) != `"m"` || string(body["stream"]) != "false" || body["max_tokens"] != nil || body["device"] != nil {
			t.Errorf("wrong request %s", body)
		}
		var messages []struct{ Role, Content string }
		json.Unmarshal(body["messages"], &messages)
		if len(messages) != 2 || messages[0].Role != "system" || !strings.Contains(messages[0].Content, "self-reported") || !strings.Contains(messages[1].Content, `"options":["a","b"]`) {
			t.Errorf("messages %+v", messages)
		}
		io.WriteString(w, chatEnvelope(`{"pick":1,"option":"b","confidence":0.8}`, "stop"))
	}))
	defer server.Close()
	for _, base := range []string{server.URL, server.URL + "/v1"} {
		c := chatClient(base)
		v, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"a", "b"}})
		if err != nil || v.Pick != 1 || v.Device != "unknown" || v.ConfidenceSource != "model-self-report" || v.Protocol != "openai-chat" {
			t.Fatalf("%+v %v", v, err)
		}
		var logs []string
		LogJudgment(func(kind, text string) { logs = append(logs, kind+":"+text) }, "test", Request{}, v, "chosen", 0)
		if len(logs) != 2 || !strings.Contains(logs[0], "confidence_source=model-self-report") {
			t.Fatal(logs)
		}
		if _, err := c.Health(context.Background()); err == nil {
			t.Fatal("fake health")
		}
		if _, err := c.Activate(context.Background(), *c.expected); err == nil {
			t.Fatal("fake activation")
		}
	}
	if calls != 2 {
		t.Fatalf("extra requests: %d", calls)
	}
}
func TestChatJudgeRefusesMalformed(t *testing.T) {
	good := `{"pick":1,"option":"b","confidence":0.8}`
	cases := map[string]string{
		"missing": `{"pick":1,"option":"b"}`, "null": `{"pick":null,"option":"b","confidence":0.8}`,
		"duplicate":      `{"pick":0,"pick":1,"option":"b","confidence":0.8}`,
		"case duplicate": `{"pick":0,"Pick":1,"option":"b","confidence":0.8}`,
		"wrong option":   `{"pick":1,"option":"a","confidence":0.8}`,
		"out of range":   `{"pick":2,"option":"b","confidence":0.8}`,
		"nonfinite":      `{"pick":1,"option":"b","confidence":1e999}`,
		"negative":       `{"pick":1,"option":"b","confidence":-0.1}`,
		"extra":          `{"pick":1,"option":"b","confidence":0.8,"reason":"x"}`,
		"trailing":       good + good, "fence": "```json\n" + good + "\n```",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			c := chatClient("http://test")
			c.http.Transport = &stubTransport{status: 200, body: chatEnvelope(content, "stop")}
			if _, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"a", "b"}}); err == nil {
				t.Fatalf("accepted %s", content)
			}
		})
	}
	for _, body := range []string{
		chatEnvelope(good, "length"), strings.Replace(chatEnvelope(good, "stop"), `"model":"m"`, `"model":"other"`, 1),
		strings.Replace(chatEnvelope(good, "stop"), `"model":"m"`, `"model":"x","model":"m"`, 1),
		strings.Replace(chatEnvelope(good, "stop"), `"role":"assistant"`, `"role":"assistant","tool_calls":[{}]`, 1),
		strings.Replace(chatEnvelope(good, "stop"), `"model":"m"`, `"model":"m","error":{"message":"failed"}`, 1),
		`{"model":"m","choices":[]}`, `{"model":"m","choices":[`,
	} {
		c := chatClient("http://test")
		c.http.Transport = &stubTransport{status: 200, body: body}
		if _, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"a", "b"}}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
func TestChatJudgeBudgetAndCancellation(t *testing.T) {
	st := &stubTransport{status: 200, body: chatEnvelope(`{"pick":0,"option":"a","confidence":0.5}`, "stop")}
	c := chatClient("http://test")
	c.http.Transport = st
	r := Request{Context: "facts", Options: []string{"a", "b"}}
	if _, err := c.Judge(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	n := len(st.seenBody)
	r.MaxBytes = n - 1
	if _, err := c.Judge(context.Background(), r); err == nil || st.calls != 1 {
		t.Fatal("byte admission failed", err, st.calls)
	}
	r.MaxBytes = n
	if _, err := c.Judge(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			c := chatClient("http://test")
			c.http.Timeout = 10 * time.Millisecond
			c.http.Transport = &stubTransport{sleep: time.Second}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := context.DeadlineExceeded
			if cancelled {
				cancel()
				want = context.Canceled
			}
			if _, err := c.Judge(ctx, Request{Context: "facts", Options: []string{"a", "b"}}); !errors.Is(err, want) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestChatJudgeLongReasoningEnvelope(t *testing.T) {
	body := chatEnvelope(`{"pick":0,"option":"a","confidence":0.5}`, "stop")
	reasoning, _ := json.Marshal(strings.Repeat("thought ", 20000))
	body = strings.Replace(body, `"role":"assistant"`, `"role":"assistant","reasoning_content":`+string(reasoning), 1)
	c := chatClient("http://test")
	c.http.Transport = &stubTransport{status: 200, body: body}
	if _, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
}

func TestJudgeConfiguredOutputAllowance(t *testing.T) {
	for _, limit := range []int{32768, 0} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			c := chatClient("http://test")
			configurable, ok := any(c).(interface{ WithOutputTokens(int) *Client })
			if !ok {
				t.Fatal("chat judge cannot carry configured output allowance")
			}
			configurable.WithOutputTokens(limit)
			st := &stubTransport{status: 200, body: chatEnvelope(`{"pick":0,"option":"a","confidence":0.5}`, "stop")}
			c.http.Transport = st
			if _, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"a", "b"}}); err != nil {
				t.Fatal(err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(st.seenBody, &body); err != nil {
				t.Fatal(err)
			}
			if limit > 0 && string(body["max_tokens"]) != fmt.Sprint(limit) {
				t.Fatalf("output allowance absent or changed: %s", st.seenBody)
			}
			if limit == 0 && body["max_tokens"] != nil {
				t.Fatalf("zero should defer to server: %s", st.seenBody)
			}
		})
	}
	c := New("http://test", "m", DeviceGPU, time.Second)
	st := &stubTransport{status: 200, body: `{"pick":0,"option":"a","confidence":0.5,"model":"m","device":"gpu"}`}
	c.http.Transport = st
	req := Request{Context: "facts", Options: []string{"a", "b"}}
	if _, err := c.Judge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	before := string(st.seenBody)
	configurable, ok := any(c).(interface{ WithOutputTokens(int) *Client })
	if !ok {
		t.Fatal("missing output configuration")
	}
	configurable.WithOutputTokens(32768)
	if _, err := c.Judge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if string(st.seenBody) != before {
		t.Fatalf("native protocol changed: before=%s after=%s", before, st.seenBody)
	}
}

func TestProductionJudgeOutputWiring(t *testing.T) {
	sites := map[string]string{"../../cmd/specific-harness/main.go": "cfg.Params.MaxOutputTokens", "../tui/chat.go": "cfg.Params.MaxOutputTokens", "../run/run.go": ""}
	count := 0
	for path, want := range sites {
		tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		newCalls, configured := 0, 0
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "decision" && sel.Sel.Name == "New" {
				newCalls++
			}
			if sel.Sel.Name != "WithOutputTokens" {
				return true
			}
			var receiver bytes.Buffer
			format.Node(&receiver, token.NewFileSet(), sel.X)
			if !strings.HasPrefix(receiver.String(), "decision.New(") {
				return true
			}
			if len(call.Args) != 1 {
				t.Fatal("output setter requires configured allowance")
			}
			var arg bytes.Buffer
			format.Node(&arg, token.NewFileSet(), call.Args[0])
			expected := want
			if expected == "" {
				expected = "cfg.Params.MaxOutputTokens"
				if strings.HasPrefix(receiver.String(), "decision.New(d.Cfg.") {
					expected = "d.Cfg.Params.MaxOutputTokens"
				}
			}
			expected = "int(" + expected + ")"
			if arg.String() != expected {
				t.Errorf("%s uses %s want %s", path, arg.String(), expected)
			}
			configured++
			return true
		})
		if newCalls != configured {
			t.Errorf("%s has %d judge clients but %d configured allowances", path, newCalls, configured)
		}
		count += configured
	}
	if count != 5 {
		t.Errorf("production configured judge clients=%d want5", count)
	}
}

func TestChatJudgeTruncationNamesFinishReason(t *testing.T) {
	c := chatClient("http://test")
	c.http.Transport = &stubTransport{status: 200, body: chatEnvelope("{", "length")}
	_, err := c.Judge(context.Background(), Request{Context: "facts", Options: []string{"a", "b"}})
	if err == nil || !strings.Contains(err.Error(), `finish_reason="length"`) {
		t.Fatalf("missing actual finish reason: %v", err)
	}
}
