package agent_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Exact captured mlx48 SSE, losslessly compressed; replay is not inference.
//
//go:embed testdata/partial-mlx48.sse.gz
var partialMLX48 []byte

func failedNativeFixture(t *testing.T, raw []byte, sink agent.SessionLog) (*agent.Runner, llm.Reply, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Error("unexpected fixture request", r.URL)
			http.Error(w, "wrong path", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	reg := tool.NewRegistry()
	reg.Register(bash.New(cfg.Tools.Bash))
	client := llm.New(srv.URL, "fixture").WithParams(llm.Params{Temperature: cfg.Params.Temperature, TopK: cfg.Params.TopK, RepetitionPenalty: cfg.Params.RepetitionPenalty, Thinking: cfg.Params.Thinking, MaxOutputTokens: int(cfg.Params.MaxOutputTokens)}).WithRuntime("mlx")
	defs, err := reg.NativeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	want, transportErr := client.ChatTools(context.Background(), []llm.Message{{Role: "user", Content: "capture control"}}, defs, nil)
	if transportErr == nil {
		t.Fatal("negative fixture unexpectedly completed")
	}
	return &agent.Runner{Model: client, Registry: reg, Session: sink, SessionID: "partial-native", MaxSteps: 1, MaxTokens: 32768, Out: &bytes.Buffer{}}, want, transportErr
}

func readFailedNativeArchive(t *testing.T, events []session.Event, want llm.Reply, transportErr error, window int) {
	t.Helper()
	var raw strings.Builder
	header := false
	for _, e := range events {
		if e.Kind == "native_reply" || e.Kind == "done" || e.Kind == "tool" || e.Kind == "native_result" {
			t.Error("partial became completed/executable", e.Kind)
		}
		if e.Kind != "reasoning" {
			continue
		}
		if !strings.HasPrefix(e.Text, "offset=") {
			var h struct {
				Usage   llm.Usage
				Partial bool
				Bytes   int
			}
			if err := json.Unmarshal([]byte(e.Text), &h); err != nil {
				t.Fatal(err)
			}
			header = h.Partial && h.Usage == want.Usage && h.Bytes > 0
			continue
		}
		prefix, fragment, ok := strings.Cut(e.Text, "\n")
		var offset, total int
		if _, err := fmt.Sscanf(prefix, "offset=%d total=%d", &offset, &total); err != nil || !ok || offset != raw.Len() || len(fragment) > 32<<10 || !utf8.ValidString(fragment) || offset+len(fragment) > total {
			t.Fatal("invalid archive fragment", prefix)
		}
		raw.WriteString(fragment)
	}
	var archived struct {
		Reply llm.Reply
		Error string
	}
	if err := json.Unmarshal([]byte(raw.String()), &archived); err != nil || !reflect.DeepEqual(archived.Reply, want) || archived.Error != transportErr.Error() || !header {
		t.Errorf("failed original not archived exactly: header=%t bytes=%d error=%v", header, raw.Len(), err)
	}
	c, notice, err := session.Restore(events, window)
	if err != nil || c.Validate() != nil || !strings.Contains(notice, "restored bounded continuation") {
		t.Fatalf("invalid restore: %q %v", notice, err)
	}
	encoded, err := c.Encode()
	if err != nil || len(encoded) > 8000+len(transportErr.Error()) {
		t.Errorf("unbounded failure continuation: %d %v", len(encoded), err)
	}
	for _, e := range c.Entries {
		if len(e.Message.ToolCalls) > 0 || e.Message.Reasoning != "" || e.Message.ReasoningContent != "" || e.Message.Thinking != "" || e.Message.Role == "assistant" {
			t.Error("incomplete native fields entered resume", e.Kind)
		}
	}
}

func TestPartialNativeCapturedMLX48ArchivedAndBounded(t *testing.T) {
	z, err := gzip.NewReader(bytes.NewReader(partialMLX48))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(z)
	z.Close()
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != "26bb0a6d2165df5bdfd8c960642f465c6aaf603f3eda825b51a2e7380c24fc93" {
		t.Fatal("captured response drift", err)
	}
	dir := t.TempDir()
	r, want, transportErr := failedNativeFixture(t, raw, thinkingLog{dir: dir})
	if len(want.Message.Reasoning) != 97110 || want.FinishReason != "length" || want.Usage != (llm.Usage{In: 8301, Out: 16384, Reported: true}) {
		t.Fatalf("capture identity drift: reasoning=%d %+v", len(want.Message.Reasoning), want.Usage)
	}
	_, _, err = r.Run(context.Background(), "retain captured failed reasoning")
	if err == nil || r.Outcome.Status != outcome.Failed || r.UsedIn != 8301 || r.UsedOut != 16384 || r.UsageUnknown {
		t.Fatalf("partial failed/usage lost: %+v %v", r.Outcome, err)
	}
	events, err := session.Read(dir, "partial-native")
	if err != nil {
		t.Fatal(err)
	}
	readFailedNativeArchive(t, events, want, transportErr, 32768)
}

func failedCallWire(t *testing.T, marker string, known bool) []byte {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"cmd": "printf must-not-run > '" + marker + "'"})
	chunk := map[string]any{"model": "served-fixture", "choices": []any{map[string]any{"delta": map[string]any{"content": "partial answer", "reasoning": strings.Repeat("é\t\n", 14000), "reasoning_content": "other original channel", "tool_calls": []any{map[string]any{"index": 0, "id": "incomplete-one", "type": "function", "function": map[string]any{"name": "bash", "arguments": string(args)}}}}, "finish_reason": "length"}}}
	if known {
		chunk["usage"] = map[string]int{"prompt_tokens": 19, "completion_tokens": 7}
	}
	b, _ := json.Marshal(chunk)
	return append(append([]byte("data: "), b...), []byte("\n\ndata: [DONE]\n\n")...)
}

func TestPartialNativeCallNeverExecutesAndRetainsUnknownUsage(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			dir := t.TempDir()
			r, want, transportErr := failedNativeFixture(t, failedCallWire(t, marker, known), thinkingLog{dir: dir})
			final, _, err := r.Run(context.Background(), "refuse incomplete call")
			if !strings.Contains(final, "partial reply (not complete; not executed):\npartial answer") || !strings.Contains(r.Out.(*bytes.Buffer).String(), final) {
				t.Error("labeled partial content lost from returned/display output")
			}
			if err == nil || r.Outcome.Status != outcome.Failed || r.UsageUnknown == known {
				t.Fatalf("failure/usage disposition lost: %+v %v", r.Outcome, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("incomplete call produced effect", err)
			}
			events, err := session.Read(dir, "partial-native")
			if err != nil {
				t.Fatal(err)
			}
			readFailedNativeArchive(t, events, want, transportErr, 32768)
		})
	}
}

func TestPartialNativeArchiveWriteFailureIsLoud(t *testing.T) {
	for _, failure := range []string{"reasoning", "fragment", "partial", "continuation", "no-sink"} {
		t.Run(failure, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			var sink agent.SessionLog = thinkingLog{dir: t.TempDir(), fail: failure}
			if failure == "no-sink" {
				sink = nil
			}
			r, _, _ := failedNativeFixture(t, failedCallWire(t, marker, true), sink)
			_, _, err := r.Run(context.Background(), "record interrupted native evidence")
			if err == nil || r.Outcome.Status != outcome.Failed || !strings.Contains(err.Error(), "length") {
				t.Fatalf("original transport cause lost: %+v %v", r.Outcome, err)
			}
			if failure == "no-sink" {
				if !strings.Contains(err.Error(), "no session archive") {
					t.Fatal("missing archive hidden", err)
				}
			} else if !errors.Is(err, thinkingRecordError) {
				t.Fatal("recording failure hidden", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("recording failure executed partial call", err)
			}
		})
	}
}

func TestPartialNativeLongErrorHealthyArchive(t *testing.T) {
	first := `data: {"model":"served-original","choices":[{"delta":{"reasoning":"é\t\noriginal"},"finish_reason":"length"}]}`
	late, _ := json.Marshal(map[string]any{"error": map[string]string{"message": strings.Repeat("é", 20000)}})
	raw := []byte(first + "\n\ndata: " + string(late) + "\n\ndata: [DONE]\n\n")
	dir := t.TempDir()
	r, want, cause := failedNativeFixture(t, raw, thinkingLog{dir: dir})
	if len(cause.Error()) <= 32<<10 {
		t.Fatal("long transport error fixture not admitted")
	}
	_, _, err := r.Run(context.Background(), "retain full original transport failure")
	if err == nil || r.Outcome.Status != outcome.Failed {
		t.Fatal("partial failure became successful", err)
	}
	events, err := session.Read(dir, "partial-native")
	if err != nil {
		t.Fatal(err)
	}
	readFailedNativeArchive(t, events, want, cause, 65536)
	if restored, notice, err := session.Restore(events, 32768); err != nil || !strings.Contains(notice, "retained history") || restored.Estimate() <= 32768 {
		t.Error("oversized factual error must be retained with pressure disclosure for owning cleanup", err, notice)
	}
	for _, e := range events {
		if e.Kind == "reasoning" && !strings.HasPrefix(e.Text, "offset=") && len(e.Text) > 256 {
			t.Error("partial header duplicated unbounded provenance")
		}
	}
}
