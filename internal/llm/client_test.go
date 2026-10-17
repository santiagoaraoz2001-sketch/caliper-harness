package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// stubTransport fakes the server without sockets: hermetic by construction.
type stubTransport struct {
	t        *testing.T
	wantPath string
	status   int
	body     string
	seenPath string
	seenBody []byte
}

func (s *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.seenPath = r.URL.Path
	if r.Body != nil {
		s.seenBody, _ = io.ReadAll(r.Body)
	}
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", s.status, http.StatusText(s.status)),
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    r,
	}, nil
}

func stubClient(t *testing.T, status int, body string) (*Client, *stubTransport) {
	st := &stubTransport{t: t, status: status, body: body}
	c := New("http://fake:9/v1", "fake-1")
	c.http = &http.Client{Transport: st}
	return c, st
}

func TestChatRoundTrip(t *testing.T) {
	c, st := stubClient(t, 200, `{"model":"fake-1","choices":[{"message":{"content":"hello back"}}]}`)
	reply, served, u, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if st.seenPath != "/v1/chat/completions" {
		t.Errorf("path: got %s", st.seenPath)
	}
	if reply != "hello back" {
		t.Errorf("reply: got %q", reply)
	}
	if served != "fake-1" {
		t.Errorf("served identity: got %q", served)
	}
	if u.Reported {
		t.Errorf("usage reported from a body without usage: %+v", u)
	}
}

func TestChatParsesUsage(t *testing.T) {
	c, _ := stubClient(t, 200, `{"model":"fake-1","choices":[{"message":{"content":"hi"}}],`+
		`"usage":{"prompt_tokens":128,"completion_tokens":9,"total_tokens":137}}`)
	_, _, u, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if !u.Reported || u.In != 128 || u.Out != 9 {
		t.Errorf("usage=%+v", u)
	}
}

func TestChatStreamCapturesUsage(t *testing.T) {
	body := "data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	c, _ := stubClient(t, 200, body)
	_, _, u, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if !u.Reported || u.In != 10 || u.Out != 3 {
		t.Errorf("usage=%+v", u)
	}
}

func TestChatStreamAssemblesDeltas(t *testing.T) {
	body := "data: {\"model\":\"fake-1\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n" +
		"data: {\"model\":\"fake-1\",\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n" +
		"data: [DONE]\n\n"
	c, _ := stubClient(t, 200, body)
	var deltas []string
	reply, served, _, err := c.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if reply != "hello" {
		t.Errorf("assembled reply: got %q", reply)
	}
	if served != "fake-1" {
		t.Errorf("served identity: got %q", served)
	}
	if len(deltas) != 2 {
		t.Errorf("expected 2 delta callbacks, got %d", len(deltas))
	}
}

type refuseTransport struct{}

func (refuseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9}, Err: fmt.Errorf("connection refused")}
}

func TestChatRefusedIsPlainLanguage(t *testing.T) {
	c := New("http://127.0.0.1:9/v1", "x")
	c.http = &http.Client{Transport: refuseTransport{}}
	_, _, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for refused connection, got nil")
	}
	if !strings.Contains(err.Error(), "cannot reach model server") || !strings.Contains(err.Error(), "is it running?") {
		t.Errorf("error must say what failed and what to check, got: %v", err)
	}
}

func TestChatServerErrorNamesStatusAndBody(t *testing.T) {
	c, _ := stubClient(t, 404, `{"error":"no such model"}`)
	c.model = "ghost"
	_, _, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	for _, want := range []string{"404", "no such model", "ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}

func TestChatBadJSONFails(t *testing.T) {
	c, _ := stubClient(t, 200, `not json`)
	if _, _, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected error for bad JSON, got nil")
	}
}

func TestChatStreamSkipsKeepalives(t *testing.T) {
	// Byte shape captured from the live mlx_lm.server on :8080.
	body := ": keepalive 10/11\n\n" +
		"data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning\":\"\\n\"}}]}\n\n" +
		"data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"
	c, _ := stubClient(t, 200, body)
	reply, served, _, err := c.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if reply != "hi" {
		t.Errorf("reply: got %q, want %q", reply, "hi")
	}
	if served != "m" {
		t.Errorf("served identity: got %q", served)
	}
}

func TestStreamBadChunkFails(t *testing.T) {
	c, _ := stubClient(t, 200, "data: {broken\n\n")
	if _, _, _, err := c.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("expected error for bad chunk, got nil")
	}
}

func TestChatSendsParams(t *testing.T) {
	st := &stubTransport{t: t, status: 200,
		body: `{"model":"m","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`}
	c := &Client{baseURL: "http://x", model: "m", http: &http.Client{Transport: st}}
	c.WithRuntime(RuntimeMLX).WithParams(Params{Temperature: 0.3, TopK: 10, RepetitionPenalty: 1.2, Thinking: false})
	if _, _, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(st.seenBody, &got); err != nil {
		t.Fatal(err)
	}
	if got["temperature"] != 0.3 || got["top_k"] != 10.0 || got["repetition_penalty"] != 1.2 || got["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Errorf("body=%v", got)
	}
}

func TestWithoutThinkingSendsExplicitFalseAndLeavesOriginal(t *testing.T) {
	st := &stubTransport{t: t, status: 200,
		body: `{"model":"m","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`}
	c := &Client{baseURL: "http://x", model: "m", http: &http.Client{Transport: st}}
	c.WithRuntime(RuntimeMLX).WithParams(Params{Temperature: 0.7, Thinking: true})
	if _, _, _, err := c.WithoutThinking().Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(st.seenBody, &got); err != nil {
		t.Fatal(err)
	}
	if got["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Errorf("summary client must send enable_thinking=false: %v", got)
	}
	if c.Thinking == nil || !*c.Thinking {
		t.Fatal("WithoutThinking mutated the original client")
	}
}

func TestChatOmitsUnsetParams(t *testing.T) {
	st := &stubTransport{t: t, status: 200,
		body: `{"model":"m","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`}
	c := &Client{baseURL: "http://x", model: "m", http: &http.Client{Transport: st}}
	if _, _, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(st.seenBody, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"temperature", "top_k", "repetition_penalty", "enable_thinking"} {
		if _, ok := got[k]; ok {
			t.Errorf("unset %s must stay off the wire: %v", k, got)
		}
	}
}
