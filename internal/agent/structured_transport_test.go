package agent_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/auth"
	"specific-harness/internal/outcome"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/files"
)

func TestStructuredRunnerUsesStreamAndCorrelatedActualBash(t *testing.T) {
	file := filepath.Join(t.TempDir(), "effect")
	requests := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "wrong origin/path", 400)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			args, _ := json.Marshal(map[string]string{"cmd": "printf accepted > '" + file + "'"})
			chunk := map[string]any{"model": "fixture", "choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-one", "type": "function", "function": map[string]any{"name": "bash", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}}
			if body["stream"] != true {
				json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "call-one", "type": "function", "function": map[string]any{"name": "bash", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
				return
			}
			raw, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
		} else {
			fmt.Fprint(w, "data: {\"model\":\"fixture\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server = srv.URL
	cfg.Model.Runtime = "openai"
	if cfg.Model.Server != srv.URL {
		t.Fatal("fixture origin binding lost")
	}
	reg := tool.NewRegistry()
	reg.Register(bash.New(cfg.Tools.Bash))
	runner := agent.Runner{Model: llm.New(cfg.Model.Server, "fixture").WithRuntime(cfg.Model.Runtime), Registry: reg, MaxSteps: 3}
	_, _, err := runner.Run(context.Background(), "component owned marker")
	got, readErr := os.ReadFile(file)
	if err != nil || readErr != nil || string(got) != "accepted" {
		t.Fatalf("actual marker=%q runErr=%v readErr=%v", got, err, readErr)
	}
	if len(requests) != 2 || requests[0]["stream"] != true || requests[0]["tools"] == nil {
		t.Fatalf("actual requests %+v", requests)
	}
	msgs := requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call-one" || !strings.Contains(fmt.Sprint(last["content"]), "tool result") {
		t.Fatalf("uncorrelated receipt %+v", last)
	}
}

type nativeRecordingSink struct {
	events []struct{ kind, text string }
	fail   string
}

func (s *nativeRecordingSink) Append(_ context.Context, _ string, kind, text string) error {
	s.events = append(s.events, struct{ kind, text string }{kind, text})
	if kind == s.fail {
		return fmt.Errorf("owned recording failure %s", kind)
	}
	return nil
}
func TestStructuredRunnerRefusalAndRecordingSettlement(t *testing.T) {
	for _, mode := range []string{"wrong name", "required empty", "duplicate JSON", "denied", "partial", "record reply", "record result"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "forbidden")
			args, _ := json.Marshal(map[string]string{"cmd": "touch '" + marker + "'"})
			name := "bash"
			if mode == "wrong name" {
				name = "BASH_unknown"
			}
			if mode == "required empty" {
				args = []byte(`{}`)
			}
			if mode == "duplicate JSON" {
				args = []byte(`{"cmd":"true","cmd":"false"}`)
			}
			calls := []any{map[string]any{"index": 0, "id": "one", "type": "function", "function": map[string]string{"name": name, "arguments": string(args)}}}
			n := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n++
				if n == 1 {
					raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}}}})
					fmt.Fprintf(w, "data: %s\n\n", raw)
					reason := "tool_calls"
					if mode == "partial" {
						reason = "length"
					}
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", reason)
				} else {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			cfg := config.Defaults()
			cfg.Model.Server = srv.URL
			reg := tool.NewRegistry()
			reg.Register(bash.New(cfg.Tools.Bash))
			sink := &nativeRecordingSink{}
			if mode == "record reply" {
				sink.fail = "native_reply"
			}
			if mode == "record result" {
				sink.fail = "native_result"
			}
			runner := agent.Runner{Model: llm.New(cfg.Model.Server, "fixture"), Registry: reg, MaxSteps: 3, Session: sink, SessionID: "owned"}
			if mode == "denied" {
				gate := auth.Gate{Mode: config.AuthAll, AskFunc: func(context.Context, string) bool { return false }}
				runner.Exec = gate.Wrap(agent.Direct)
			}
			_, _, err := runner.Run(context.Background(), "negative native component")
			_, effectErr := os.Stat(marker)
			if mode == "record result" {
				if effectErr != nil || err == nil || runner.Outcome.ExitCode() == 0 {
					t.Fatalf("settled effect/record error lost %v %v %+v", effectErr, err, runner.Outcome)
				}
			} else if !os.IsNotExist(effectErr) {
				t.Fatalf("negative call executed %v", effectErr)
			}
			if mode == "partial" || mode == "record reply" || mode == "record result" || mode == "duplicate JSON" {
				if err == nil || runner.Outcome.ExitCode() == 0 {
					t.Fatalf("failure promoted %v %+v", err, runner.Outcome)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if e := runner.Continuation.Validate(); e != nil {
					t.Fatal(e)
				}
				receipts := 0
				for _, entry := range runner.Continuation.Entries {
					if entry.Message.Role == "tool" {
						receipts++
						if entry.Kind != "failure" {
							t.Fatal("refusal misclassified")
						}
					}
				}
				expected := 1
				if receipts != expected {
					t.Fatalf("receipts%d expected%d", receipts, expected)
				}
			}
		})
	}
}
func TestStructuredNativeEmptyObjectInvokesZeroParamTool(t *testing.T) {
	calls := 0
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "probe", Execute: func(context.Context, tool.Args) (tool.Result, error) {
		calls++
		return tool.Result{Text: "actual zero-param receipt"}, nil
	}})
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"zero\",\"type\":\"function\",\"function\":{\"name\":\"probe\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server = srv.URL
	r := agent.Runner{Model: llm.New(cfg.Model.Server, "fixture"), Registry: reg, MaxSteps: 2}
	if _, _, e := r.Run(context.Background(), "zero-param native invocation"); e != nil || calls != 1 {
		t.Fatalf("calls%d %v", calls, e)
	}
}
func TestStructuredExplicitRunBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server = srv.URL
	reg := tool.NewRegistry()
	reg.Register(bash.New(cfg.Tools.Bash))
	r := agent.Runner{Model: llm.New(cfg.Model.Server, "fixture"), Registry: reg, MaxSteps: 2, MaxRunSeconds: 1}
	start := time.Now()
	_, _, e := r.Run(context.Background(), "budget component")
	if e == nil || r.Outcome.Status != outcome.Stopped || !strings.Contains(r.Outcome.Reason, "max_run_seconds=1") || time.Since(start) > 2*time.Second {
		t.Fatalf("budget %+v %v", r.Outcome, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.MaxRunSeconds = 10
	_, _, e = r.Run(ctx, "caller cancellation")
	if e == nil || r.Outcome.Status != outcome.Cancelled || strings.Contains(r.Outcome.Reason, "max_run_seconds") {
		t.Fatalf("caller cancellation mislabeled %+v %v", r.Outcome, e)
	}
}

// HTTP responses are controlled; Read, native validation, help and next-wire
// serialization are actual owners. No model inference or edit recovery is claimed.
func TestStructuredMissingEditHashGuidanceReachesNextRequest(t *testing.T) {
	const original = "package fixture\nvar observed = 1\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "source.go")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var requests []struct {
		Messages []llm.Message `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "decode", 400)
			return
		}
		requests = append(requests, body)
		var name, id string
		var args map[string]any
		switch len(requests) {
		case 1:
			name, id, args = "read", "observed-read", map[string]any{"path": "source.go"}
		case 2:
			name, id, args = "edit", "refused-edit", map[string]any{"path": "source.go", "old": "invented source", "start_line": 1, "end_line": 2, "new": "forbidden replacement"}
		case 3:
			name, id, args = "help", "focused-edit-help", map[string]any{"topic": "help.edit"}
		case 4:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"inspection only"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
			return
		default:
			t.Error("unexpected additional request")
			http.Error(w, "extra", 400)
			return
		}
		rawArgs, _ := json.Marshal(args)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(rawArgs)}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer srv.Close()
	cfg := config.Defaults()
	reg := tool.NewRegistry()
	workspace := &files.Workspace{Path: dir, Caps: cfg.Tools.Bash}
	tools.RegisterAt(reg, cfg, nil, nil, nil, workspace)
	dispatched := []string{}
	sink := &nativeRecordingSink{}
	r := &agent.Runner{Model: llm.New(srv.URL, "controlled"), Registry: reg, MaxSteps: 4, Session: sink}
	r.Exec = func(ctx context.Context, d tool.Definition, args tool.Args) (tool.Result, error) {
		dispatched = append(dispatched, d.Name)
		return agent.Direct(ctx, d, args)
	}
	if _, _, err := r.Run(context.Background(), "Inspect source; request no publication"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 4 || strings.Join(dispatched, ",") != "read,help" {
		t.Fatalf("requests=%d actual dispatch=%v", len(requests), dispatched)
	}
	var read, refusal, help string
	for _, m := range requests[2].Messages {
		if m.Role == "tool" && m.ToolCallID == "observed-read" {
			read = m.Content
		}
		if m.Role == "tool" && m.ToolCallID == "refused-edit" {
			refusal = m.Content
		}
	}
	if !strings.Contains(read, fmt.Sprintf("source.go sha256=%x", sha256.Sum256([]byte(original)))) || !strings.Contains(read, original) {
		t.Fatal("actual Read/hash lost before recovery request")
	}
	for _, needed := range []string{"entire call array refused; no calls ran", "expected_hash", "actual read sha256", "never both", "help(topic=help.edit)"} {
		if !strings.Contains(refusal, needed) {
			t.Errorf("correlated refusal missing %q: %s", needed, refusal)
		}
	}
	for _, m := range requests[3].Messages {
		if m.Role == "tool" && m.ToolCallID == "focused-edit-help" {
			help = m.Content
		}
	}
	if !strings.Contains(help, "## help.edit") || !strings.Contains(help, "Current schema:") || !strings.Contains(help, "expected_hash") {
		t.Fatal("actual focused guide topic did not reach next wire")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != original {
		t.Fatalf("refused edit published: %q %v", got, err)
	}
	if err = r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
}
