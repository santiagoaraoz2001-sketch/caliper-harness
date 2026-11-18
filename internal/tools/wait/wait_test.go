package wait

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func runWait(t *testing.T, root string, args tool.Args) map[string]any {
	t.Helper()
	d := New(root, prompts.Builtin())
	res, err := d.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil {
		t.Fatalf("wait result is not JSON: %v %q", err, res.Text)
	}
	return out
}

func TestWaitTimerVisiblePollingAndWake(t *testing.T) {
	root := t.TempDir()
	first := runWait(t, root, tool.Args{"duration_seconds": int64(2), "poll_seconds": int64(1), "poll_window_seconds": int64(1)})
	if first["status"] != "still_waiting" || first["deadline_unix"] == nil {
		t.Fatalf("first poll = %+v", first)
	}
	deadline := int64(first["deadline_unix"].(float64))
	time.Sleep(1200 * time.Millisecond)
	second := runWait(t, root, tool.Args{"deadline_unix": deadline, "poll_seconds": int64(1), "poll_window_seconds": int64(2)})
	if second["status"] != "woke" || second["reason"] != "timer" {
		t.Fatalf("second poll = %+v", second)
	}
}

func TestWaitFileAndHTTPEvents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "done.txt"), []byte("CI complete"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runWait(t, root, tool.Args{"duration_seconds": int64(60), "file_path": "done.txt", "file_contains": "complete", "poll_window_seconds": int64(1)})
	if got["status"] != "woke" || got["reason"] != "file" {
		t.Fatalf("file wait = %+v", got)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	got = runWait(t, root, tool.Args{"http_url": srv.URL, "http_status": int64(204), "poll_window_seconds": int64(1)})
	if got["status"] != "woke" || got["reason"] != "http" {
		t.Fatalf("http wait = %+v", got)
	}
	if !strings.Contains(New(root, prompts.Builtin()).Name, "wait") {
		t.Fatal("wait tool name")
	}
}
