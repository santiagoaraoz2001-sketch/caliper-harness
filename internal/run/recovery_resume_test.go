package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// Actual New/Runner/store scope across separate session IDs; model replies only
// are controlled. Foreign-scope advice and ambiguous legacy guidance are retained.
func TestPreparationRecoveryFreshSessionScope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	bindDecisionFixture(t, &cfg, nil)
	requests, second := 0, false
	var active *agent.Runner
	var originalRefusal llm.Message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message
			Tools    []llm.ToolDefinition
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if second {
			// A fresh session may publish its authenticated current question, but
			// cannot revive superseded preparation advice. Derive the expected
			// header from graph facts and the actual request registry, not the
			// production publication/retirement predicate.
			node, err := launch.ScopeNode(s, active.Continuation.Workflow)
			if err != nil {
				t.Error(err)
				return
			}
			def, ok := active.Registry.Lookup("plan")
			if !ok {
				t.Error("resumed current planning question missing")
				return
			}
			header := active.Prompts.Format("system.workflow.scope_header", node.Kind, node.Ref, node.ID, node.State, node.Ancestors, node.Children, strings.Join(active.Registry.Names(), ", "))
			want := session.Entry{Kind: "schema", PreparationScope: s.Identity() + "\n" + dir, Message: llm.Message{Role: "user", Content: header + "\n" + def.Schema()}}
			own := 0
			for _, e := range active.Continuation.Entries {
				if e.PreparationScope == want.PreparationScope {
					own++
					if !reflect.DeepEqual(e, want) {
						t.Errorf("resumed owned entry is not the exact authenticated current schema: %+v", e)
					}
				}
			}
			if own != 1 {
				t.Errorf("resumed current schema count=%d, want exactly one and no obsolete advice", own)
			}
			wireSchema, wireRefusal := 0, 0
			for _, message := range body.Messages {
				if reflect.DeepEqual(message, want.Message) {
					wireSchema++
				}
				if reflect.DeepEqual(message, originalRefusal) {
					wireRefusal++
				}
				if strings.Contains(message.Content, active.Prompts.Text("continuation.preparation.current_frame")) {
					t.Error("fresh-session request revived obsolete preparation advice")
				}
			}
			if wireSchema != 1 || wireRefusal != 1 {
				t.Errorf("resumed wire lost or duplicated exact schema/refusal: schema=%d refusal=%d", wireSchema, wireRefusal)
			}
			if requests == 0 {
				currentSchema, refusal, intent, selected := false, false, false, false
				for _, def := range body.Tools {
					if def.Function.Name == "plan" {
						properties, ok := def.Function.Parameters["properties"].(map[string]any)
						_, articles := properties["articles"]
						currentSchema = ok && articles
					}
				}
				for _, message := range body.Messages {
					selected = selected || strings.Contains(message.Content, "Selected workflow: root ")
					refusal = refusal || message.Role == "tool" && message.ToolCallID == "scopefalse" && strings.Contains(message.Content, "plan_title")
					intent = intent || message.Role == "user" && message.Content == "same project request"
				}
				if !currentSchema || !refusal || !intent || !selected {
					t.Errorf("resumed context/schema lost actual evidence: schema=%t refusal=%t intent=%t", currentSchema, refusal, intent)
				}
			}
			for _, retained := range []string{"legacy preparation guidance", "different store advice", "different project advice"} {
				found := false
				for _, m := range body.Messages {
					found = found || m.Content == retained
				}
				if !found {
					t.Errorf("%q lost across resume", retained)
				}
			}
		}
		delta, finish := map[string]any{"reasoning": "component explicit stop"}, "stop"
		if requests == 0 {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("scope", second), "type": "function", "function": map[string]string{"name": "prepare_task", "arguments": "{}"}}}}
			finish = "tool_calls"
		}
		requests++
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	defer server.Close()
	cfg.Model.Server = server.URL
	makeRunner := func(id string, c session.Continuation) *agent.Runner {
		t.Helper()
		r, _, err := New(Deps{Tools: "workflow", Cfg: cfg, Store: s, Server: server.URL, Model: "component", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: id, Continuation: c, Window: 32768, MaxSteps: 2}, "same project request")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	active = makeRunner("first", session.Continuation{Workflow: selectedFixtureScope(t, s, "root")})
	if _, _, err := active.Run(context.Background(), "same project request"); err != nil {
		t.Fatal(err)
	}
	c := active.Continuation.Snapshot()
	for _, entry := range c.Entries {
		if entry.Message.Role == "tool" && entry.Message.ToolCallID == "scopefalse" {
			originalRefusal = entry.Message
		}
	}
	if originalRefusal.Role != "tool" || !strings.Contains(originalRefusal.Content, "plan_title") {
		t.Fatal("first session did not record the actual preparation refusal")
	}
	c.AddGuidance("legacy preparation guidance", nil)
	c.AddGuidance("different store advice", nil)
	c.Entries[len(c.Entries)-1].PreparationScope = "/different/store\n" + active.Workspace
	c.AddGuidance("different project advice", nil)
	c.Entries[len(c.Entries)-1].PreparationScope = active.Assigned.Store + "\n/different/project"
	requests, second = 0, true
	active = makeRunner("second", c)
	if _, _, err := active.Run(context.Background(), "same project request"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("resume requests%d", requests)
	}
	if !reflect.DeepEqual(active.Continuation.Workflow, c.Workflow) {
		t.Fatal("resume changed selected workflow identity")
	}
	for _, e := range c.Entries {
		if e.Message.Content == "legacy preparation guidance" || e.PreparationScope != "" && e.PreparationScope != active.Assigned.Store+"\n"+active.Workspace {
			found := false
			for _, retained := range active.Continuation.Entries {
				found = found || reflect.DeepEqual(e, retained)
			}
			if !found {
				t.Fatal("legacy or foreign owner/project provenance mutated")
			}
		}
	}
	if active.Continuation.Assignment != nil {
		t.Fatal("resume invented old claim")
	}
}
