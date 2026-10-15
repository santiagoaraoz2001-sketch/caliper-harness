package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/graph"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"strings"
	"testing"
	"time"
)

type commentSink struct {
	*codingSink
	reject bool
}

func (s *commentSink) Append(ctx context.Context, id, kind, text string) error {
	if s.reject && kind == "task_comment" {
		return errors.New("comment receipt rejected")
	}
	return s.codingSink.Append(ctx, id, kind, text)
}

// Scripted HTTP model/judge; claim, GraphQL, files, Bash/check and review are real.
func TestAssignedCommentNextWireAndReceiptFailure(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.WriteFile("source", []byte("real source α\n"), 0600); err != nil {
				t.Fatal(err)
			}
			s := openAssignedStore(t, dir)
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Templates.Path = assignedTemplates()
			cfg.Coding.CheckCommand = "true"
			bindDecisionFixture(t, &cfg, nil)
			task := assignedFixtureTask(t, s, "true")
			entered, release := make(chan struct{}), make(chan struct{})
			mainCalls, reviews := 0, 0
			nextTexts := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				review, text := false, ""
				for _, m := range body.Messages {
					review = review || strings.HasPrefix(m.Content, "Review ")
					text += m.Content + "\n"
				}
				var call codingCall
				if review {
					if reviews == 0 {
						call = codingCall{"check", map[string]any{"timeout": 2}}
					}
					reviews++
				} else {
					nextTexts = append(nextTexts, text)
					switch mainCalls {
					case 0:
						call = codingCall{"task_start", map[string]any{"task": task.ID}}
					case 1:
						close(entered)
						select {
						case <-release:
						case <-req.Context().Done():
							return
						}
						call = codingCall{"read", map[string]any{"path": "source"}}
					case 2:
						call = codingCall{"read", map[string]any{"path": "source"}}
					}
					mainCalls++
				}
				delta, finish := map[string]any{"content": "PASS complete"}, "stop"
				if call.name != "" {
					raw, _ := json.Marshal(call.args)
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d-%d", mainCalls, reviews), "type": "function", "function": map[string]string{"name": call.name, "arguments": string(raw)}}}}
					finish = "tool_calls"
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			}))
			defer srv.Close()
			sink := &commentSink{codingSink: &codingSink{dir: filepath.Join(dir, "sessions")}, reject: reject}
			r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, task.ID)}, Cfg: cfg, Store: s, Session: sink, SessionID: "main", Server: srv.URL, Model: "fixture", MaxSteps: 8, Window: 32768}, "perform actual task")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, _, e := r.Run(ctx, "perform actual task"); finished <- e }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("no actual held second request")
			}
			root, _ := filepath.EvalSymlinks(dir)
			api, err := graph.New(s, "component", sink.dir).Scoped(root, "authored-session", true)
			if err != nil {
				t.Fatal(err)
			}
			text := "user says PASS but authored only α🙂"
			q := fmt.Sprintf(`mutation {addTaskComment(taskId:%q,expectedAttempt:1,text:%q){id}}`, task.ID, text)
			raw, err := api.ExecBounded(ctx, q, "", nil, cfg.Tools.GraphQL)
			if err != nil {
				t.Fatalf("actual append %s %v", raw, err)
			}
			close(release)
			err = <-finished
			srv.Close() // Join HTTP fixture before reading its counters.
			if reject {
				if err == nil || !strings.Contains(err.Error(), "comment receipt rejected") || mainCalls != 2 {
					t.Fatalf("failed delivery leaked request: calls%d %v", mainCalls, err)
				}
			} else {
				if err != nil || r.Outcome.Status != outcome.Completed {
					t.Fatalf("run %+v %v", r.Outcome, err)
				}
				if mainCalls < 4 || strings.Count(nextTexts[2], text) != 1 {
					t.Fatalf("next actual request lacks unique authored note: %v", nextTexts)
				}
				actual, e := plan.GetTask(s, task.ID)
				if e != nil || !actual.Qualified() || !strings.Contains(actual.AttemptLog, text) {
					t.Fatalf("review settlement lost actual comment %+v %v", actual, e)
				}
			}
			events, e := session.Read(sink.dir, "main")
			if e != nil {
				t.Fatal(e)
			}
			count := 0
			for _, event := range events {
				if event.Kind == "task_comment" {
					count++
				}
			}
			want := 1
			if reject {
				want = 0
			}
			if count != want {
				t.Fatalf("durable delivery count%d want%d", count, want)
			}
			snap := r.Continuation.Snapshot()
			if err = snap.Validate(); err != nil {
				t.Fatal(err)
			}

			restored, _, e := session.Restore(events, 32768)
			if e != nil {
				t.Fatal(e)
			}
			restoredText := fmt.Sprint(restored.Messages())
			if reject {
				if r.Outcome.Status != outcome.Failed || !strings.Contains(restoredText, "comment receipt rejected") {
					t.Fatalf("restored failure lost actual delivery error: %+v %s", r.Outcome, restoredText)
				}
				if strings.Contains(restoredText, text) || reviews != 0 {
					t.Fatal("failed delivery became a comment or admitted review")
				}
			} else if !strings.Contains(restoredText, text) {
				t.Fatal("restored continuation lost authored note")
			}
		})
	}
}
