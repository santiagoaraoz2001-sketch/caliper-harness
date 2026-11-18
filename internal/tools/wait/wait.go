package wait

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

type result struct {
	Status       string   `json:"status"`
	Reason       string   `json:"reason,omitempty"`
	ElapsedSecs  int64    `json:"elapsed_seconds"`
	DeadlineUnix int64    `json:"deadline_unix,omitempty"`
	Evidence     string   `json:"evidence,omitempty"`
	Unmatched    []string `json:"unmatched,omitempty"`
	NextAction   string   `json:"next_action"`
}

func New(root string, pc *prompts.Catalog) tool.Definition {
	return tool.Definition{
		Name:    "wait",
		Desc:    pc.Text("tools.wait.description"),
		Purpose: pc.Text("tools.wait.purpose"),
		Prompts: pc,
		Params: []tool.Param{
			{Name: "duration_seconds", Type: tool.TypeInt, Desc: pc.Text("tools.wait.parameters.duration_seconds")},
			{Name: "deadline_unix", Type: tool.TypeInt, Desc: pc.Text("tools.wait.parameters.deadline_unix")},
			{Name: "process_id", Type: tool.TypeInt, Desc: pc.Text("tools.wait.parameters.process_id")},
			{Name: "file_path", Type: tool.TypeString, Desc: pc.Text("tools.wait.parameters.file_path")},
			{Name: "file_contains", Type: tool.TypeString, Desc: pc.Text("tools.wait.parameters.file_contains")},
			{Name: "file_absent", Type: tool.TypeBool, Desc: pc.Text("tools.wait.parameters.file_absent")},
			{Name: "http_url", Type: tool.TypeString, Desc: pc.Text("tools.wait.parameters.http_url")},
			{Name: "http_status", Type: tool.TypeInt, Desc: pc.Text("tools.wait.parameters.http_status")},
			{Name: "poll_seconds", Type: tool.TypeInt, Desc: pc.Text("tools.wait.parameters.poll_seconds")},
			{Name: "poll_window_seconds", Type: tool.TypeInt, Desc: pc.Text("tools.wait.parameters.poll_window_seconds")},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			out, err := run(ctx, root, args)
			if err != nil {
				return tool.Result{}, err
			}
			data, err := json.Marshal(out)
			return tool.Result{Text: string(data)}, err
		},
	}
}

func run(ctx context.Context, root string, args tool.Args) (result, error) {
	start := time.Now()
	duration := intArg(args, "duration_seconds")
	deadline := intArg(args, "deadline_unix")
	pid := intArg(args, "process_id")
	filePath := strings.TrimSpace(strArg(args, "file_path"))
	contains := strArg(args, "file_contains")
	absent := boolArg(args, "file_absent")
	httpURL := strings.TrimSpace(strArg(args, "http_url"))
	wantStatus := intArg(args, "http_status")
	if wantStatus == 0 {
		wantStatus = 200
	}
	poll := intArg(args, "poll_seconds")
	if poll <= 0 {
		poll = 5
	}
	if poll > 300 {
		poll = 300
	}
	window := intArg(args, "poll_window_seconds")
	if window <= 0 {
		window = 30
	}
	if window > 120 {
		window = 120
	}
	if duration < 0 || deadline < 0 || pid < 0 {
		return result{}, fmt.Errorf("duration_seconds, deadline_unix, and process_id must not be negative")
	}
	if duration > 0 && deadline == 0 {
		deadline = start.Add(time.Duration(duration) * time.Second).Unix()
	}
	if deadline == 0 && pid == 0 && filePath == "" && httpURL == "" {
		return result{}, fmt.Errorf("wait needs at least one wake condition: duration_seconds/deadline_unix, process_id, file_path, or http_url")
	}
	if contains != "" && filePath == "" {
		return result{}, fmt.Errorf("file_contains requires file_path")
	}
	windowEnd := start.Add(time.Duration(window) * time.Second)
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		now := time.Now()
		var unmatched []string
		if deadline > 0 {
			if now.Unix() >= deadline {
				return finish(start, deadline, "woke", "timer", "deadline reached"), nil
			}
			unmatched = append(unmatched, "timer")
		}
		if pid > 0 {
			alive, err := processAlive(int(pid))
			if err == nil && !alive {
				return finish(start, deadline, "woke", "process", fmt.Sprintf("process %d is not running", pid)), nil
			}
			unmatched = append(unmatched, "process")
		}
		if filePath != "" {
			matched, evidence := checkFile(root, filePath, contains, absent)
			if matched {
				return finish(start, deadline, "woke", "file", evidence), nil
			}
			unmatched = append(unmatched, "file")
		}
		if httpURL != "" {
			matched, evidence := checkHTTP(ctx, client, httpURL, int(wantStatus))
			if matched {
				return finish(start, deadline, "woke", "http", evidence), nil
			}
			unmatched = append(unmatched, "http")
		}
		if !now.Before(windowEnd) {
			return result{Status: "still_waiting", ElapsedSecs: int64(time.Since(start).Seconds()), DeadlineUnix: deadline, Unmatched: unmatched, NextAction: "Call wait again with the same conditions and the same deadline_unix. Do not poll with shell sleep."}, nil
		}
		sleep := time.Duration(poll) * time.Second
		if until := time.Until(windowEnd); until < sleep {
			sleep = until
		}
		if deadline > 0 {
			if until := time.Until(time.Unix(deadline, 0)); until > 0 && until < sleep {
				sleep = until
			}
		}
		if sleep < 50*time.Millisecond {
			sleep = 50 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return result{}, ctx.Err()
		case <-time.After(sleep):
		}
	}
}

func finish(start time.Time, deadline int64, status, reason, evidence string) result {
	return result{Status: status, Reason: reason, ElapsedSecs: int64(time.Since(start).Seconds()), DeadlineUnix: deadline, Evidence: evidence, NextAction: "Continue with the task that waited for this condition."}
}

func checkFile(root, path, contains string, absent bool) (bool, string) {
	full := path
	if !filepath.IsAbs(full) && root != "" {
		full = filepath.Join(root, path)
	}
	_, err := os.Stat(full)
	if absent {
		if os.IsNotExist(err) {
			return true, "file is absent: " + path
		}
		return false, ""
	}
	if err != nil {
		return false, ""
	}
	if contains == "" {
		return true, "file exists: " + path
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return false, ""
	}
	if strings.Contains(string(data), contains) {
		return true, "file contains the requested text: " + path
	}
	return false, ""
}

func checkHTTP(ctx context.Context, client *http.Client, url string, want int) (bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == want {
		return true, fmt.Sprintf("HTTP status %d from %s", resp.StatusCode, url)
	}
	return false, ""
}

func strArg(args tool.Args, name string) string { v, _ := args[name].(string); return v }
func intArg(args tool.Args, name string) int64 {
	switch v := args[name].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
func boolArg(args tool.Args, name string) bool { v, _ := args[name].(bool); return v }
