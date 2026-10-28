package decision

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemOneDryAdmissionCountsEncodedEscaping(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer s.Close()
	c := New(s.URL, "d1:3b", "auto", 0).WithSelection(Selection{Runtime: "ollama", Protocol: "system_one"})
	// Raw text fits 64 KiB; JSON HTML escaping exceeds it. Both paths must
	// refuse the exact same encoded body before touching the server.
	req := Request{Context: strings.Repeat("<", 12000), Instructions: "choose", Options: []string{"yes", "no"}}
	pre := c.Preflight(req)
	_, actual := c.Judge(t.Context(), req)
	var capacity *ContextCapacityError
	if !errors.As(pre, &capacity) || !capacity.EncodedRequest || actual == nil || actual.Error() != pre.Error() || calls != 0 {
		t.Fatalf("preflight=%v judge=%v calls=%d", pre, actual, calls)
	}
	req.Context = "current facts"
	if err := c.Preflight(req); err != nil || calls != 0 {
		t.Fatalf("dry admission called provider: %v calls=%d", err, calls)
	}
	req.Context = "x"
	body, err := c.systemOneRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Context = strings.Repeat("x", (64<<10)-len(body)+1)
	if err = c.Preflight(req); err != nil {
		t.Fatalf("exact 64 KiB boundary refused: %v", err)
	}
	req.Context += "x"
	if err = c.Preflight(req); !errors.As(err, &capacity) {
		t.Fatalf("one byte over boundary accepted: %v", err)
	}
}
