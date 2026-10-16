package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSelectionJudgeIdentityAndTrailingRefuse(t *testing.T) {
	for _, body := range []string{
		`{"pick":0,"option":"a","confidence":0.8,"model":"wrong","device":"cpu"}`,
		`{"pick":0,"option":"a","confidence":0.8,"model":"m","device":"wrong"}`,
		`{"pick":0,"option":"a","confidence":0.8,"model":"m","device":"cpu"} {}`,
	} {
		c := stubClient(&stubTransport{status: 200, body: body})
		if _, err := c.Judge(context.Background(), Request{Context: "ctx", Options: []string{"a", "b"}}); err == nil {
			t.Errorf("accepted malformed/identity-skewed response %s", body)
		}
	}
}

// Transport doubles prove wire admission, not native-model capability.
type selectionTransport func(*http.Request) (*http.Response, error)

func (f selectionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func selectionHealth(t *testing.T, s Selection) string {
	t.Helper()
	yes, no := true, false
	h := Health{Status: "ok", PID: 42, Loaded: &yes, Ready: &yes, Loading: &no, Model: &s.Model, Requested: &s, Backends: []string{"cpu"},
		Active: &Identity{Model: s.Model, Runtime: s.Runtime, Source: "path", Path: "/resolved", Device: "cpu", NativeDevice: "cpu", Architecture: "native", Parameters: 100, Selection: s}}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func response(s string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s)), Header: make(http.Header)}
}
func TestSelectionBindingPreflightAndWire(t *testing.T) {
	p := "/downloaded"
	rev := "pin"
	s := Selection{Model: "third-org/native", Runtime: "system_one", Device: "auto", Path: &p, Revision: &rev, TrustRemoteCode: true}
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			posts := 0
			active := s
			if mismatch {
				other := "/other"
				active.Path = &other
			}
			c := New("http://owned", s.Model, s.Device, time.Second).WithSelection(s)
			c.http = &http.Client{Transport: selectionTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return response(selectionHealth(t, active)), nil
				}
				posts++
				var body judgeRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Expected == nil || !body.Expected.sameAssets(s) {
					t.Fatal("missing binding")
				}
				return response(`{"pick":0,"option":"a","confidence":0.9,"model":"third-org/native","device":"cpu"}`), nil
			})}
			_, err := c.Judge(context.Background(), Request{Context: "ctx", Options: []string{"a", "b"}})
			if mismatch && (err == nil || posts != 0) || !mismatch && (err != nil || posts != 1) {
				t.Fatalf("posts%d err%v", posts, err)
			}
		})
	}
}
func TestSelectionHealthRefusesMalformedIdentity(t *testing.T) {
	s := Selection{Model: "m", Runtime: "laya", Device: "auto"}
	valid := selectionHealth(t, s)
	for _, body := range []string{`{"status":"ok"}`, valid + ` {}`, strings.Replace(valid, `"parameters":100`, `"parameters":0`, 1), strings.Replace(valid, `"native_device":"cpu"`, `"native_device":"mps"`, 1), strings.Replace(valid, `"ready":true`, `"ready":false`, 1), strings.Repeat(" ", 65537)} {
		if _, err := DecodeHealth(strings.NewReader(body)); err == nil {
			t.Fatalf("admitted %s", body[:min(len(body), 100)])
		}
	}
	if _, err := DecodeHealth(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
}
func TestSelectionReadFailureAndCancellationPreserved(t *testing.T) {
	c := New("http://owned", "m", "auto", time.Second)
	c.http = &http.Client{Transport: selectionTransport(func(r *http.Request) (*http.Response, error) { return nil, context.Canceled })}
	if _, err := c.Health(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Activate(context.Background(), Selection{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Judge(context.Background(), Request{Context: "c", Options: []string{"a", "b"}}); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("canceled judgment mislabeled or cause lost: %v", err)
	}
	c.http = &http.Client{Transport: selectionTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: brokenReader{}}, nil
	})}
	if _, err := c.Judge(context.Background(), Request{Context: "c", Options: []string{"a", "b"}}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenReader) Close() error             { return nil }

func TestSelectionMetadataAndJudgeShareTimeout(t *testing.T) {
	s := Selection{Model: "m", Runtime: "laya", Device: "auto"}
	c := New("http://owned", "m", "auto", 30*time.Millisecond).WithSelection(s)
	c.http = &http.Client{Timeout: 30 * time.Millisecond, Transport: selectionTransport(func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		if r.Method == "GET" {
			return response(selectionHealth(t, s)), nil
		}
		return response(`{"pick":0,"option":"a","confidence":0.9,"model":"m","device":"cpu"}`), nil
	})}
	if _, err := c.Judge(context.Background(), Request{Context: "ctx", Options: []string{"a", "b"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("metadata and judge escaped shared budget: %v", err)
	}
}

// Exact root native D1 receipt body; the current server adds PID and admitted Selection.
// This is a wire replay component control, not a new native inference.
const nativeD1HealthBeforeMetadata = `{
    "status": "ok",
    "loaded": true,
    "ready": true,
    "model": "LiquidAI/d1-3B",
    "active": {
      "model": "LiquidAI/d1-3B",
      "runtime": "system_one",
      "source": "/Volumes/Cold Storage 1/SpecificHarnessModels/d1-3B-eval/models--LiquidAI--d1-3B/snapshots/051bcc464b01b9f92942b364d9586b0ef5912432",
      "path": "/Volumes/Cold Storage 1/SpecificHarnessModels/d1-3B-eval/models--LiquidAI--d1-3B/snapshots/051bcc464b01b9f92942b364d9586b0ef5912432",
      "revision": "051bcc464b01b9f92942b364d9586b0ef5912432",
      "context_limit": 32768,
      "native_device": "mps:0",
      "device": "gpu",
      "parameters": 3123483888,
      "architecture": "D1Model"
    },
    "requested": {
      "model": "LiquidAI/d1-3B",
      "runtime": "system_one",
      "device": "gpu",
      "path": "/Volumes/Cold Storage 1/SpecificHarnessModels/d1-3B-eval/models--LiquidAI--d1-3B/snapshots/051bcc464b01b9f92942b364d9586b0ef5912432",
      "revision": "051bcc464b01b9f92942b364d9586b0ef5912432",
      "trust_remote_code": true
    },
    "loading": false,
    "error": null,
    "backends": [
      "gpu"
    ]
  }`

func TestSelectionActualNativeD1Identity(t *testing.T) {
	var h Health
	if err := json.Unmarshal([]byte(nativeD1HealthBeforeMetadata), &h); err != nil {
		t.Fatal(err)
	}
	h.PID = 123
	h.Active.Selection = *h.Requested
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := DecodeHealth(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if actual.Active.NativeDevice != "mps:0" || actual.Active.Parameters != 3123483888 || !actual.Matches(*h.Requested) {
		t.Fatal("native admitted identity changed")
	}
	for _, native := range []string{"mps", "mps:0", "cuda", "cuda:0", "cuda:12", "mps:-1", "mps:1", "mps:garbage", "cuda:-1", "cuda:+1", "cuda:01", "cuda:garbage"} {
		t.Run(native, func(t *testing.T) {
			h.Active.NativeDevice = native
			b, _ := json.Marshal(h)
			_, err := DecodeHealth(strings.NewReader(string(b)))
			valid := native == "mps" || native == "mps:0" || native == "cuda" || native == "cuda:0" || native == "cuda:12"
			if (err == nil) != valid {
				t.Fatalf("native%q err%v", native, err)
			}
		})
	}
}

func TestSelectionNativeD1ChunkedAttentionHealth(t *testing.T) {
	var h Health
	if err := json.Unmarshal([]byte(nativeD1HealthBeforeMetadata), &h); err != nil {
		t.Fatal(err)
	}
	h.PID = 123
	h.Active.Selection = *h.Requested
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	wire := strings.Replace(string(b), `"native_device":"mps:0"`, `"mps_chunked_attention":true,"native_device":"mps:0"`, 1)
	actual, err := DecodeHealth(strings.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if !actual.Matches(*h.Requested) || actual.Active.MPSChunkedAttention == nil || !*actual.Active.MPSChunkedAttention {
		t.Fatal("native identity/diagnostic mismatch")
	}
}
