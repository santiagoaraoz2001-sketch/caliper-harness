package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	graphapi "specific-harness/internal/graph"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/ask"
)

// Only provider/decision replies are scripted. Factory binding, GraphQL,
// readiness, regular-file input, owner state and durable SessionLog are real.
func readinessProvider(t *testing.T, protocol string, calls []codingCall) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		generation := count.Add(1)
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/chat/completions" || req["stream"] != true {
			t.Error("unexpected model route/protocol", r.URL.Path)
		}
		delta := map[string]any{}
		finish := "tool_calls"
		if protocol == "text" {
			call := calls[0]
			body := "```tool\ntool: " + call.name + "\n"
			for k, v := range call.args {
				body += fmt.Sprintf("%s: %v\n", k, v)
			}
			delta["content"], finish = body+"```", "stop"
		} else {
			batch := []any{}
			batchCalls := calls
			if generation > 1 {
				batchCalls = calls[:1]
			}
			for i, call := range batchCalls {
				raw, _ := json.Marshal(call.args)
				batch = append(batch, map[string]any{"index": i, "id": fmt.Sprintf("ready-%d-%d", generation, i), "type": "function", "function": map[string]string{"name": call.name, "arguments": string(raw)}})
			}
			delta["tool_calls"] = batch
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	t.Cleanup(srv.Close)
	return srv, count
}

type readinessReceiptFailure struct{ agent.SessionLog }

func (s readinessReceiptFailure) Append(ctx context.Context, id, kind, text string) error {
	if kind == "approval" {
		return errors.New("controlled approval persistence failure")
	}
	return s.SessionLog.Append(ctx, id, kind, text)
}

func TestReadinessStopSignalPreservesOtherDecisionDispositions(t *testing.T) {
	for _, mode := range []string{"yes", "cancel", "revise", "record-failure", "judge-unavailable", "schema", "manual-disabled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			s, err := store.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, task := terminalDraft(t, s, root)
			snapshot, err := plan.ReadinessSnapshot(s, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Auth.Mode, cfg.Behavior.Path, cfg.Recall.TopN = config.AuthYOLO, "", 0
			cfg.Coding.CheckCommand = "true"
			cfg.Sessions.Path = filepath.Join(root, "sessions")
			confidence := .6
			pick := 0
			if mode == "revise" {
				confidence, pick = .99, 1
			}
			judgments, prompts := 0, 0
			bindDecisionFixture(t, &cfg, func(decision.Request) int { judgments++; return pick }, confidence)
			if mode == "judge-unavailable" {
				unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "controlled unavailable decision", 503) }))
				defer unavailable.Close()
				cfg.Decision.Server = unavailable.URL
			}
			if mode == "manual-disabled" {
				cfg.Decision.Disabled = true
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var sink agent.SessionLog = graphapi.NewSessionLog(graphapi.New(s, "fixture", cfg.Sessions.Path))
			if mode == "record-failure" {
				sink = readinessReceiptFailure{sink}
			}
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "not-requested", Window: 32768, Session: sink, SessionID: "disposition", Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: s.Identity(), Kind: "approach", ID: a.ID}}, Out: io.Discard, AskYesNo: func(context.Context, string) bool {
				prompts++
				if mode == "cancel" {
					cancel()
					return true
				}
				return mode == "yes"
			}}, "Implement the prepared task")
			if err != nil {
				t.Fatal(err)
			}
			def, ok := r.Registry.Lookup("graphql")
			if !ok {
				t.Fatal("owning GraphQL unavailable")
			}
			query := fmt.Sprintf(`mutation{transitionApproach(id:%q,to:"ready"){id state}}`, a.ID)
			if mode == "schema" {
				query = `mutation{notARealField}`
			}
			_, err = def.Execute(ctx, tool.Args{"query": query})
			var stop *outcome.ReadinessNotApprovedError
			if mode == "manual-disabled" {
				if !errors.As(err, &stop) || stop.Store != s.Identity() || stop.ApproachID != a.ID || stop.Snapshot != snapshot {
					t.Fatal("manual denial lost exact owner", err)
				}
			} else if errors.As(err, &stop) {
				t.Fatalf("%s became manual approval stop: %v", mode, err)
			}
			if mode == "yes" && err != nil || mode != "yes" && err == nil {
				t.Fatalf("wrong %s disposition: %v", mode, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if (mode == "revise" || mode == "schema" || mode == "judge-unavailable") && prompts != 0 {
				t.Fatal("nonmanual failure prompted", mode)
			}
			if mode == "manual-disabled" && judgments != 0 {
				t.Fatal("disabled decision inferred")
			}
			got, _ := plan.GetApproach(s, a.ID)
			want := plan.ApproachDraft
			if mode == "yes" {
				want = plan.ApproachReady
			}
			if got.State != want {
				t.Fatal("wrong owning state", mode, got.State)
			}
			x, _ := plan.GetTask(s, task.ID)
			if x.State != plan.TaskQueued || x.Attempts != 0 || x.Target != task.Target {
				t.Fatal("control changed task/claim")
			}
			if mode == "record-failure" {
				events, _ := session.Read(cfg.Sessions.Path, "disposition")
				for _, event := range events {
					if event.Kind == "approval" {
						t.Fatal("failed recording appeared durable")
					}
				}
			}
		})
	}
}

func terminalDraft(t *testing.T, s *store.Store, root string) (*plan.Approach, *plan.Task) {
	t.Helper()
	ctx := plan.WithProject(t.Context(), root)
	p, err := plan.CreatePlanContext(ctx, s, "Approved implementation", "Deliver actual requested behavior")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlanContext(ctx, s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproachContext(ctx, s, p.ID, "One bounded approach")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTaskContext(ctx, s, a.ID, "One task", "Implement the approved behavior")
	if err != nil {
		t.Fatal(err)
	}
	x, err = plan.SetTaskBlockContext(ctx, s, x.ID, "current", "target", "method", "public assertions", "existing adequate checker", "independent review", "true", "true")
	if err != nil {
		t.Fatal(err)
	}
	return a, x
}

func TestReadinessManualDenialStopsFactoryWithoutAnotherGeneration(t *testing.T) {
	for _, protocol := range []string{"structured", "text"} {
		for _, answer := range []string{"n\n", ""} {
			t.Run(protocol+fmt.Sprintf("/%q", answer), func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				s, err := store.Open(filepath.Join(t.TempDir(), "db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				a, task := terminalDraft(t, s, root)
				snapshot, err := plan.ReadinessSnapshot(s, a.ID)
				if err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(task)
				query := fmt.Sprintf(`mutation{ready:transitionApproach(id:%q,to:"ready"){id state}}`, a.ID)
				calls := []codingCall{{"graphql", map[string]any{"query": query}}}
				if protocol == "structured" {
					calls = append(calls, codingCall{"graphql", map[string]any{"query": fmt.Sprintf(`mutation{setTaskBlock(id:%q,current:"",target:"",sketch:"",acceptance:"",tests:"",verify:""){id}}`, task.ID)}})
				}
				provider, generations := readinessProvider(t, protocol, calls)
				cfg := config.Defaults()
				cfg.Auth.Mode = config.AuthYOLO
				cfg.Model.Runtime = "openai"
				cfg.Model.ToolProtocol = protocol
				cfg.Behavior.Path, cfg.Recall.TopN, cfg.Agent.LoopStall, cfg.Agent.LoopRepeat = "", 0, 0, 0
				cfg.Coding.CheckCommand = "true"
				cfg.Sessions.Path = filepath.Join(root, "sessions")
				judgments := 0
				bindDecisionFixture(t, &cfg, func(decision.Request) int { judgments++; return 0 }, .6)
				inputPath := filepath.Join(root, "answer")
				if err := os.WriteFile(inputPath, []byte(answer), 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.Open(inputPath)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				input, err := ask.NewFileInput(file)
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				sink := graphapi.NewSessionLog(graphapi.New(s, "fixture", cfg.Sessions.Path))
				r, _, err := New(Deps{Cfg: cfg, Store: s, Server: provider.URL, Model: "fixture", Window: 32768, MaxSteps: 4, Session: sink, SessionID: "readiness-denial", In: input, Out: io.Discard, Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: s.Identity(), Kind: "approach", ID: a.ID}}}, "Implement the prepared task")
				if err != nil {
					t.Fatal(err)
				}
				text, _, err := r.Run(t.Context(), "Implement the prepared task")
				if err != nil || r.Outcome.Status != outcome.Stopped || !strings.Contains(r.Outcome.Reason, "manual readiness approval was not granted") || !strings.Contains(r.Outcome.Reason, snapshot) || text != r.Outcome.Reason || generations.Load() != 1 || judgments != 1 {
					t.Fatalf("no authoritative stop: status=%+v generations=%d judgments=%d err=%v", r.Outcome, generations.Load(), judgments, err)
				}
				after, err := plan.GetTask(s, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				got, _ := json.Marshal(after)
				if string(got) != string(before) {
					t.Fatal("readiness denial changed frozen task")
				}
				parent, _ := plan.GetApproach(s, a.ID)
				if parent.State != plan.ApproachDraft {
					t.Fatal("denial admitted approach")
				}
				events, err := session.Read(cfg.Sessions.Path, "readiness-denial")
				if err != nil {
					t.Fatal(err)
				}
				approvals, results, stops, finals := 0, 0, 0, 0
				for _, event := range events {
					switch event.Kind {
					case "approval":
						var receipt auth.ApprovalEvent
						if err := json.Unmarshal([]byte(event.Text), &receipt); err != nil {
							t.Fatal(err)
						}
						if receipt.Kind == "readiness" {
							approvals++
							if receipt.Approved || receipt.Cancelled || receipt.ID != a.ID || receipt.Snapshot != snapshot || receipt.Store != s.Identity() || receipt.Workspace != root {
								t.Fatal("incorrect durable refusal", receipt)
							}
						}
					case "native_result":
						var entry session.Entry
						if err := json.Unmarshal([]byte(event.Text), &entry); err != nil {
							t.Fatal(err)
						}
						results++
						if results == 1 && (entry.Message.ToolCallID != "ready-1-0" || !strings.Contains(entry.Message.Content, snapshot)) {
							t.Fatal("uncorrelated readiness result")
						}
						if results == 2 && (entry.Message.ToolCallID != "ready-1-1" || !strings.Contains(entry.Message.Content, "call not executed:")) {
							t.Fatal("native array tail ran or lost correlation")
						}
					case "stopped":
						stops++
					case "done":
						finals++
					}
				}
				if approvals != 1 || stops != 1 || finals != 0 || protocol == "structured" && results != 2 {
					t.Fatalf("approval/result/terminal records: %d/%d/%d/%d", approvals, results, stops, finals)
				}
				if r.Continuation.Assignment != nil {
					t.Fatal("denial created a claim")
				}
				// Resume the exact saved draft with explicit approval. The former
				// typed stop is not restored and the brief/snapshot is unchanged.
				rContinuation, _, err := session.Restore(events, 32768)
				if err != nil {
					t.Fatal(err)
				}
				resume, _, err := New(Deps{Cfg: cfg, Store: s, Server: provider.URL, Model: "fixture", Window: 32768, MaxSteps: 1, Session: sink, SessionID: "readiness-resume", Continuation: rContinuation, Out: io.Discard, AskYesNo: func(context.Context, string) bool { return true }}, "Approve this exact current brief and resume")
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = resume.Run(t.Context(), "Approve this exact current brief and resume")
				parent, _ = plan.GetApproach(s, a.ID)
				if err != nil || parent.State != plan.ApproachReady || strings.Contains(resume.Outcome.Reason, "manual readiness approval was not granted") || generations.Load() != 2 {
					t.Fatalf("resume retained stale stop or failed admission: %+v %s %v", resume.Outcome, parent.State, err)
				}
				after, _ = plan.GetTask(s, task.ID)
				if after.State != plan.TaskQueued || after.Attempts != 0 {
					t.Fatal("resume approval claimed task")
				}
			})
		}
	}
}
