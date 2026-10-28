package decision

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSystemOneNativeTokenAdmissionAndUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		capacity   bool
	}{
		{"recorded dense token refusal", `{"error":"request (16849 tokens) exceeds the available context size (16384 tokens), try increasing it"}`, 400, true},
		{"not over limit", `{"error":"request (10 tokens) exceeds the available context size (16384 tokens), try increasing it"}`, 400, false},
		{"zero limit", `{"error":"request (16849 tokens) exceeds the available context size (0 tokens), try increasing it"}`, 400, false},
		{"unrelated 400", `{"error":"invalid model"}`, 400, false},
		{"unauthorized", `{"error":"request (16849 tokens) exceeds the available context size (16384 tokens), try increasing it"}`, 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			c := New(srv.URL, "d1:3b", "auto", time.Second).WithSelection(Selection{Runtime: "ollama", Protocol: "system_one", Model: "d1:3b"})
			_, err := c.Judge(t.Context(), Request{Context: "actual evidence", Options: []string{"yes", "no"}})
			var cap *ContextCapacityError
			if errors.As(err, &cap) != tc.capacity || calls != 1 {
				t.Fatalf("capacity=%v calls=%d error=%v", cap, calls, err)
			}
		})
	}
	for _, usage := range []string{"", `,"usage":null`, `,"usage":{"input_tokens":12,"output_tokens":0}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"model":"d1:3b","answers":{"decision":{"type":"choice","choice":"0","probabilities":{"0":0.9,"1":0.1}}}%s}`, usage)
		}))
		c := New(srv.URL, "d1:3b", "auto", time.Second).WithSelection(Selection{Runtime: "ollama", Protocol: "system_one", Model: "d1:3b"})
		v, err := c.Judge(t.Context(), Request{Context: "actual evidence", Options: []string{"yes", "no"}})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		events := map[string]string{}
		LogJudgment(func(k, s string) { events[k] = s }, "discovery", Request{}, v, "continued", 10)
		if strings.Contains(usage, "input_tokens") {
			if !strings.Contains(events["judgment_usage"], `"input_tokens":12`) {
				t.Fatal(events)
			}
		} else if v.Usage != "" || events["judgment_usage"] != "" {
			t.Fatal("invented usage", events)
		}
		if _, ok := ParseJudgment(events["judgment"]); !ok {
			t.Fatal("usage broke verdict parsing")
		}
	}
}
