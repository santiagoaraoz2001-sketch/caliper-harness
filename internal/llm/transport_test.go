package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const partialEvent = "data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"

func TestStreamRefusesIncompleteTransport(t *testing.T) {
	cases := []struct{ name, body, partial, cause string }{
		{"empty", "", "", "incomplete"},
		{"keepalive only", ": keepalive\n\n", "", "incomplete"},
		{"premature EOF", partialEvent, "partial", "incomplete"},
		{"empty DONE", "data: [DONE]\n\n", "", "empty"},
		{"reasoning only", "data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking\"}}]}\n\ndata: [DONE]\n\n", "", "empty"},
		{"usage only", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n", "", "empty"},
		{"finish then content", partialEvent + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" + partialEvent, "partial", "after stop"},
		{"finish then server error", partialEvent + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" + "data: {\"error\":{\"message\":\"engine stopped\"}}\n\n", "partial", "engine stopped"},
		{"server error object", partialEvent + "data: {\"error\":{\"message\":\"engine stopped\"}}\n\n", "partial", "engine stopped"},
		{"server error string", partialEvent + "data: {\"error\":\"engine stopped\"}\n\n", "partial", "engine stopped"},
		{"empty error envelope", partialEvent + "data: {\"error\":{}}\n\n", "partial", "refused"},
		{"invalid JSON", partialEvent + "data: {broken\n\n", "partial", "bad stream"},
		{"JSON null", partialEvent + "data: null\n\ndata: [DONE]\n\n", "partial", "bad stream"},
		{"length stopped", partialEvent + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", "partial", "length"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := stubClient(t, 200, tc.body)
			var observed strings.Builder
			reply, served, _, err := c.ChatStream(context.Background(), nil, func(s string) { observed.WriteString(s) })
			if err == nil {
				t.Fatalf("accepted failed transport: reply=%q served=%q", reply, served)
			}
			if !strings.Contains(err.Error(), "http://fake:9/v1") || !strings.Contains(err.Error(), tc.cause) {
				t.Errorf("error does not name endpoint/cause: %v", err)
			}
			if reply != tc.partial || observed.String() != tc.partial {
				t.Errorf("lost partial: returned=%q observed=%q want=%q", reply, observed.String(), tc.partial)
			}
			if tc.partial != "" && served != "m" {
				t.Errorf("lost served identity: %q", served)
			}
		})
	}
}

func TestStreamCompletionConventions(t *testing.T) {
	prefix := ": keepalive\n\ndata: \n\ndata: {\"model\":\"m\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning\":\"private reasoning\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n" + partialEvent +
		"data: {\"model\":\"m\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	for _, end := range []string{"", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"} {
		t.Run("end="+end, func(t *testing.T) {
			c, _ := stubClient(t, 200, prefix+end)
			var deltas []string
			reply, served, u, err := c.ChatStream(context.Background(), nil, func(s string) { deltas = append(deltas, s) })
			if err != nil || reply != "partial" || served != "m" {
				t.Fatalf("normal protocol refused: reply=%q served=%q err=%v", reply, served, err)
			}
			want := Usage{In: 10, Out: 3, Reported: true}
			if end != "" {
				want = Usage{In: 11, Out: 4, Reported: true}
			}
			if u != want {
				t.Errorf("usage=%+v want=%+v", u, want)
			}
			if len(deltas) != 1 || deltas[0] != "partial" {
				t.Errorf("non-content frames leaked into callbacks: %q", deltas)
			}
		})
	}
}

type bodyErrorTransport struct{ body io.ReadCloser }

func (s bodyErrorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: s.body, Request: r}, nil
}

type failingBody struct{ prefix *strings.Reader }

func (f *failingBody) Read(b []byte) (int, error) {
	if f.prefix.Len() > 0 {
		return f.prefix.Read(b)
	}
	return 0, io.ErrUnexpectedEOF
}
func (f *failingBody) Close() error { return nil }

func TestStreamReadFailureKeepsPartialUsage(t *testing.T) {
	body := partialEvent + "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":2}}\n\n"
	c := New("http://fixture.invalid/v1", "m")
	c.http = &http.Client{Transport: bodyErrorTransport{&failingBody{strings.NewReader(body)}}}
	reply, served, u, err := c.ChatStream(context.Background(), nil, nil)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error not preserved: %v", err)
	}
	if reply != "partial" || served != "m" || u != (Usage{In: 20, Out: 2, Reported: true}) {
		t.Errorf("lost partial result: %q %q %+v", reply, served, u)
	}
}

func TestPlainRefusesEmptyAndUnfinishedReplies(t *testing.T) {
	for _, body := range []string{`{"choices":[{"message":{}}]}`, `{"choices":[{"message":{"content":""}}]}`, `{"choices":[{"message":{"content":null}}]}`, `{"choices":[{"message":{"content":"  "}}]}`, `{"error":{"message":"engine stopped"}}`, `{"error":"engine stopped"}`, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`} {
		t.Run(body, func(t *testing.T) {
			c, _ := stubClient(t, 200, body)
			reply, _, _, err := c.Chat(context.Background(), nil)
			if err == nil {
				t.Fatalf("accepted empty/failed reply: %q", reply)
			}
			if !strings.Contains(err.Error(), "http://fake:9/v1") {
				t.Errorf("missing endpoint: %v", err)
			}
			if strings.Contains(body, `"partial"`) && reply != "partial" {
				t.Errorf("partial reply lost: %q", reply)
			}
		})
	}
}

func TestStreamFinishDoesNotHideReadFailure(t *testing.T) {
	body := partialEvent + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	c := New("http://fixture.invalid/v1", "m")
	c.http = &http.Client{Transport: bodyErrorTransport{&failingBody{strings.NewReader(body)}}}
	reply, served, _, err := c.ChatStream(context.Background(), nil, nil)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) || reply != "partial" || served != "m" {
		t.Fatalf("finish hid broken transfer/partial evidence: %q %q %v", reply, served, err)
	}
}
func TestPlainBodyReadFailureKeepsPartial(t *testing.T) {
	body := `{"model":"m","choices":[{"message":{"content":"partial"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`
	c := New("http://fixture.invalid/v1", "m")
	c.http = &http.Client{Transport: bodyErrorTransport{&failingBody{strings.NewReader(body)}}}
	reply, served, u, err := c.Chat(context.Background(), nil)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error not preserved: %v", err)
	}
	if reply != "partial" || served != "m" || u != (Usage{In: 5, Out: 2, Reported: true}) {
		t.Errorf("partial plain response lost: %q %q %+v", reply, served, u)
	}
}
func TestPlainMalformedTrailingJSONRefuses(t *testing.T) {
	for _, tail := range []string{" trailing", ` {"error":"engine stopped"}`} {
		c, _ := stubClient(t, 200, `{"model":"m","choices":[{"message":{"content":"partial"}}]}`+tail)
		_, _, _, err := c.Chat(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "JSON") {
			t.Fatalf("malformed trailing JSON accepted: %v", err)
		}
	}
}

func TestPlainOversizeResponseRefuses(t *testing.T) {
	// The old LimitReader silently accepted a small valid JSON answer followed by
	// enough whitespace to reach its cap. The whole HTTP body is not complete.
	body := `{"choices":[{"message":{"content":"partial"}}]}` + strings.Repeat(" ", 32<<20)
	c, _ := stubClient(t, 200, body)
	reply, _, _, err := c.Chat(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "transport limit") || reply != "partial" {
		t.Fatalf("cap became successful completion/lost partial: %q %v", reply, err)
	}
}

func TestStreamDONERequiresCompleteHTTPTransfer(t *testing.T) {
	body := partialEvent + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1", "m")
	var seen strings.Builder
	reply, served, _, err := c.ChatStream(context.Background(), nil, func(s string) { seen.WriteString(s) })
	if !errors.Is(err, io.ErrUnexpectedEOF) || reply != "partial" || seen.String() != "partial" || served != "m" {
		t.Fatalf("DONE hid broken declared transfer: reply=%q callback=%q served=%q err=%v", reply, seen.String(), served, err)
	}
}

func TestStreamDONERefusesLaterData(t *testing.T) {
	for _, tc := range []struct{ name, tail, cause string }{
		{"error", "data: {\"error\":{\"message\":\"post-DONE failure\"}}\n\n", "post-DONE failure"},
		{"content", partialEvent, "after [DONE]"},
		{"malformed", "data: {broken\n\n", "bad stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := stubClient(t, 200, partialEvent+"data: [DONE]\n\n"+tc.tail)
			var seen strings.Builder
			reply, _, _, err := c.ChatStream(context.Background(), nil, func(s string) { seen.WriteString(s) })
			if err == nil || !strings.Contains(err.Error(), tc.cause) || reply != "partial" || seen.String() != "partial" {
				t.Fatalf("post-terminal data accepted/extended: reply=%q callback=%q err=%v", reply, seen.String(), err)
			}
		})
	}
}

func TestStreamRefusalPreservesEnvelopeMetadata(t *testing.T) {
	for _, tc := range []struct{ name, prefix, tail, partial, served string }{
		{"first error", "", "data: {\"model\":\"error-model\",\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":9},\"error\":\"engine failed\"}\n\n", "", "error-model"},
		{"error after DONE", partialEvent + "data: [DONE]\n\n", "data: {\"model\":\"m\",\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":9},\"error\":\"engine failed\"}\n\n", "partial", "m"},
		{"data after DONE", partialEvent + "data: [DONE]\n\n", "data: {\"model\":\"m\",\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":9},\"choices\":[{\"delta\":{\"content\":\"must not append\"}}]}\n\n", "partial", "m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := stubClient(t, 200, tc.prefix+tc.tail)
			var seen strings.Builder
			reply, served, u, err := c.ChatStream(context.Background(), nil, func(s string) { seen.WriteString(s) })
			if err == nil || reply != tc.partial || seen.String() != tc.partial || served != tc.served || u != (Usage{In: 31, Out: 9, Reported: true}) {
				t.Fatalf("refusal lost metadata or appended refused content: reply=%q callback=%q served=%q usage=%+v err=%v", reply, seen.String(), served, u, err)
			}
		})
	}
}
