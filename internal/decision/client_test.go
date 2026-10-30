package decision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stubTransport struct {
	status   int
	body     string
	calls    int
	sleep    time.Duration
	seenBody []byte
}

func (s *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	s.seenBody, _ = io.ReadAll(r.Body)
	if s.sleep > 0 {
		select {
		case <-time.After(s.sleep):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", s.status, http.StatusText(s.status)),
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    r,
	}, nil
}

func stubClient(st *stubTransport) *Client {
	c := New("http://fake:9", "m", DeviceAuto, time.Minute)
	c.http = &http.Client{Transport: st}
	return c
}

func TestJudgeRoundTrip(t *testing.T) {
	st := &stubTransport{status: 200, body: `{"pick":1,"option":"b","confidence":0.87,"model":"m","device":"cpu"}`}
	v, err := stubClient(st).Judge(context.Background(), Request{Context: "ctx", Options: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("judge: %s", err)
	}
	if v.Pick != 1 || v.Option != "b" || v.Confidence != 0.87 || v.Model != "m" || v.Device != "cpu" {
		t.Fatalf("verdict=%+v", v)
	}
}

func TestJudgeRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http error carries status and body", 400, `{"error":"unknown model"}`, "HTTP 400"},
		{"http error names the cause", 400, `{"error":"unknown model"}`, "unknown model"},
		{"empty error body said plainly", 500, ``, "empty body"},
		{"malformed json rejected", 200, `{"pick":`, "malformed JSON"},
		{"unknown field rejected", 200, `{"pick":1,"option":"b","confidence":0.5,"model":"m","device":"cpu","extra":1}`, "malformed JSON"},
		{"missing pick rejected", 200, `{"option":"b","confidence":0.5,"model":"m","device":"cpu"}`, "without a pick"},
		{"pick out of range rejected", 200, `{"pick":5,"option":"b","confidence":0.5,"model":"m","device":"cpu"}`, "picked 5 for 2 options"},
		{"echo mismatch rejected", 200, `{"pick":1,"option":"zzz","confidence":0.5,"model":"m","device":"cpu"}`, "mismatches option 1"},
		{"missing confidence rejected", 200, `{"pick":1,"option":"b","model":"m","device":"cpu"}`, "without a confidence"},
		{"confidence over 1 rejected", 200, `{"pick":1,"option":"b","confidence":1.5,"model":"m","device":"cpu"}`, "not a probability"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubTransport{status: tc.status, body: tc.body}
			_, err := stubClient(st).Judge(context.Background(), Request{Context: "ctx", Options: []string{"a", "b"}})
			if err == nil {
				t.Fatal("bad verdict accepted, want rejection")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestJudgeValidatesBeforeDial(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"empty context", Request{Options: []string{"a", "b"}}, "needs a context"},
		{"one option", Request{Context: "c", Options: []string{"a"}}, "at least 2 options"},
		{"blank option", Request{Context: "c", Options: []string{"a", ""}}, "option 1 is blank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubTransport{status: 200, body: `{}`}
			_, err := stubClient(st).Judge(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if st.calls != 0 {
				t.Fatalf("invalid request dialed %d times", st.calls)
			}
		})
	}
}

func TestJudgeSendsDeviceWant(t *testing.T) {
	for _, want := range []string{DeviceAuto, DeviceCPU} {
		st := &stubTransport{status: 200, body: `{"pick":0,"option":"a","confidence":0.9,"model":"m","device":"cpu"}`}
		c := New("http://fake:9", "m", want, time.Minute)
		c.http = &http.Client{Transport: st}
		if _, err := c.Judge(context.Background(), Request{Context: "c", Options: []string{"a", "b"}}); err != nil {
			t.Fatalf("judge: %s", err)
		}
		if !strings.Contains(string(st.seenBody), `"device":"`+want+`"`) {
			t.Fatalf("device %q not on the wire: %s", want, st.seenBody)
		}
	}
}

func TestJudgeTimeout(t *testing.T) {
	st := &stubTransport{status: 200, body: `{}`, sleep: 2 * time.Second}
	c := New("http://fake:9", "m", DeviceAuto, 20*time.Millisecond)
	c.http = &http.Client{Transport: st, Timeout: 20 * time.Millisecond}
	_, err := c.Judge(context.Background(), Request{Context: "c", Options: []string{"a", "b"}})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("got %v, want truthful timeout with wrapped deadline cause", err)
	}
}

// Complete bodies have no byte ceiling; provider token admission is the only limit.
func TestJudgeCompleteBodyLimit(t *testing.T) {
	var calls atomic.Int32
	bodies := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodies <- body
		fmt.Fprint(w, `{"pick":0,"option":"a","confidence":0.9,"model":"m","device":"cpu"}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "m", DeviceAuto, time.Minute)
	req := Request{Context: "Unicode é <body>", Instructions: "complete guidance", Options: []string{"a", "b"}}
	if _, err := c.Judge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	actual := len(<-bodies)
	// No byte ceiling: a much larger complete body still reaches the server.
	req.Context = strings.Repeat("large legitimate brief ", 20000)
	if _, err := c.Judge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := len(<-bodies); got <= actual {
		t.Fatalf("large body did not reach HTTP: %d <= %d", got, actual)
	}
}

func TestNativeCapacityRefusalIsTypedOnlyForExactAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"configured input capacity", 400, `{"error":"request has 16000 tokens; decision input limit is 15999; shorten context/options"}`, true},
		{"configured input not exceeded", 400, `{"error":"request has 15999 tokens; decision input limit is 15999; shorten context/options"}`, false},
		{"configured trailing text", 400, `{"error":"request has 16000 tokens; decision input limit is 15999; shorten context/options extra"}`, false},
		{"native real capacity", 400, `{"error":"request has 40000 tokens; model context limit is 32768; shorten context/options"}`, true},
		{"native Laya capacity", 400, `{"error":"complete context does not fit the selected Laya model; shorten context/options"}`, true},
		{"not actually over capacity", 400, `{"error":"request has 10 tokens; model context limit is 32768; shorten context/options"}`, false},
		{"zero capacity", 400, `{"error":"request has 40000 tokens; model context limit is 0; shorten context/options"}`, false},
		{"identity failure", 400, `{"error":"requested model pending activation"}`, false},
		{"head budget is irreducible", 400, `{"error":"Laya question/options do not fit the selected head budget; shorten instructions/options"}`, false},
		{"untrusted trailing assertion", 400, `{"error":"request has 40000 tokens; model context limit is 32768; shorten context/options trailing assertion"}`, false},
		{"wrong HTTP status", 500, `{"error":"request has 40000 tokens; model context limit is 32768; shorten context/options"}`, false},
		{"malformed body", 400, `request has 40000 tokens; model context limit is 32768; shorten context/options`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubTransport{status: tc.status, body: tc.body}
			_, err := stubClient(st).Judge(t.Context(), Request{Context: "actual supplied evidence", Options: []string{"a", "b"}})
			var capacity *ContextCapacityError
			if err == nil || errors.As(err, &capacity) != tc.want || st.calls != 1 {
				t.Fatalf("refusal mislabeled/retried: %v calls=%d", err, st.calls)
			}
		})
	}
}
