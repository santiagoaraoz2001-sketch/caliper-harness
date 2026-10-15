package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

// Actual scoped Graph/Kuzu effects; only call encoding varies between protocols.
func TestPreparationCheckCommandBinding(t *testing.T) {
	for _, protocol := range []string{"native", "text"} {
		t.Run(protocol, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			limits := config.Defaults().Tools.GraphQL
			owner, root := api.New(s, "test", t.TempDir()), t.TempDir()
			input := tool.Args{"plan_title": "repair", "plan_goal": "correct output", "approach_title": "small fix", "task_title": "repair", "task_prompt": "repair source", "current": "observed problem", "target": "correct output", "sketch": "small change", "acceptance": "Regression test demonstrating chained question display", "tests": "owning test", "verify": "inspect source", "acceptance_command": "test -d ."}
			invoke := func(command string, cap config.GraphQLConfig, supplied tool.Args) (tool.Result, error) {
				g, e := New(owner, cap, root, "binding", s.Identity())
				if e != nil {
					return tool.Result{}, e
				}
				d := Preparation(g, s, cap, command)[0]
				var args tool.Args
				var repair string
				if protocol == "native" {
					raw, _ := json.Marshal(supplied)
					args, repair = tool.ValidateJSON(d, string(raw))
				} else {
					raw := map[string]string{}
					for k, v := range supplied {
						raw[k] = v.(string)
					}
					args, repair = tool.Validate(d, raw)
				}
				if repair != "" {
					return tool.Result{}, fmt.Errorf("%s", repair)
				}
				bound, args, e := d.Bind(context.Background(), args)
				if e != nil {
					return tool.Result{}, e
				}
				return bound.Execute(context.Background(), args)
			}
			copyInput := func(value any, present bool) tool.Args {
				out := tool.Args{}
				for k, v := range input {
					out[k] = v
				}
				if present {
					out["check_command"] = value
				}
				return out
			}
			for _, c := range []struct {
				name, configured, value string
				present                 bool
			}{
				{"empty", "true", "", true}, {"mismatch", "true", "false", true}, {"unset_omission", "", "", false},
			} {
				if _, e := invoke(c.configured, limits, copyInput(c.value, c.present)); e == nil {
					t.Errorf("%s admitted", c.name)
				}
			}
			if protocol == "native" {
				if _, e := invoke("true", limits, copyInput(nil, true)); e == nil {
					t.Error("native null admitted")
				}
			}
			capped := limits
			capped.MaxRequestBytes = 1024
			if _, e := invoke(strings.Repeat("x", 1025), capped, input); e == nil || !strings.Contains(e.Error(), "max_request_bytes") {
				t.Fatalf("default command bypassed effective request cap: %v", e)
			}
			plans, err := plan.ListPlans(s, "")
			if err != nil || len(plans) != 0 {
				t.Fatalf("refusals had effects: %d %v", len(plans), err)
			}
			for _, c := range []struct {
				name, configured, value, source string
				present                         bool
			}{
				{"configured_omission", "printf 'ok'\n", "", "configuredCheckCommand", false},
				{"configured_match", "true", "true", "configuredCheckCommand", true},
				{"unset_authored", "", "true", "proposedCheckCommand", true},
			} {
				result, e := invoke(c.configured, limits, copyInput(c.value, c.present))
				var receipt api.Result
				if e != nil || result.Failed || json.Unmarshal([]byte(result.Text), &receipt) != nil || len(receipt.Effects) != 4 {
					t.Fatalf("%s actual receipt: %s %v", c.name, result.Text, e)
				}
				var data struct{ PrepareTask struct{ Task map[string]any } }
				if e = json.Unmarshal(receipt.Data, &data); e != nil {
					t.Fatal(e)
				}
				id := receipt.Effects[3].ID
				want := c.value
				if c.configured != "" {
					want = c.configured
				}
				actual, e := plan.GetTask(s, id)
				if e != nil || actual.CheckCommand != want || actual.AcceptanceCommand != input["acceptance_command"] || data.PrepareTask.Task["checkCommand"] != want || data.PrepareTask.Task[c.source] != want {
					t.Fatalf("%s owning command/source lost: %+v %s %v", c.name, actual, result.Text, e)
				}
				for _, effect := range receipt.Effects {
					if effect.Status != "applied" || effect.ID == "" {
						t.Fatal("lost actual effect identity")
					}
				}
			}
			// Selection overflow can follow four committed mutations; receipts disclose them.
			capped = limits
			capped.MaxResultBytes = 1200
			result, e := invoke(strings.Repeat("x", 1000), capped, input)
			var partial api.Result
			if e == nil || !result.Failed || len(result.Text) > capped.MaxResultBytes || json.Unmarshal([]byte(result.Text), &partial) != nil || len(partial.Effects) != 4 {
				t.Fatalf("result ceiling lost bounded partial effects: %s %v", result.Text, e)
			}
			for _, effect := range partial.Effects {
				if effect.Status != "applied" || effect.ID == "" {
					t.Fatalf("post-commit omission implied rollback: %+v", partial.Effects)
				}
			}
			actual, e := plan.GetTask(s, partial.Effects[3].ID)
			if e != nil || actual.CheckCommand != strings.Repeat("x", 1000) {
				t.Fatal("partial receipt did not name committed task")
			}
		})
	}
}

func TestRegisteredGraphQLVariablesPartialAndCapture(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	canonical, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if e = os.Symlink(root, alias); e != nil {
		t.Fatal(e)
	}
	def, e := New(api.New(s, "test", t.TempDir()), config.Defaults().Tools.GraphQL, alias, "actual-session", s.Identity())
	if e != nil {
		t.Fatal(e)
	}
	reg := tool.NewRegistry()
	reg.Register(def)
	d, ok := reg.Lookup("graphql")
	if !ok {
		t.Fatal("missing")
	}
	invoke := func(query, vars string) (tool.Result, error) {
		raw, _ := json.Marshal(map[string]string{"query": query, "variables": vars})
		args, repair := tool.ValidateJSON(d, string(raw))
		if repair != "" {
			t.Fatal(repair)
		}
		return d.Execute(context.Background(), args)
	}
	result, e := invoke(`mutation($body:String!){putKnowledge(key:"colors",body:$body,expectedPreviousId:""){id body}}`, `{"body":"α🙂\n"}`)
	if e != nil || result.Failed || !strings.Contains(result.Text, `α🙂\n`) {
		t.Fatalf("variables %s %v", result.Text, e)
	}
	// Existing public owners accept arbitrary legacy IDs; use a real persisted old ID.
	if e = s.Write(`CREATE(p:Plan {id:'1234567890123456789',title:'legacy',goal:'g',state:'draft',created_at:TIMESTAMP('2026-01-01 00:00:00')})`, nil); e != nil {
		t.Fatal(e)
	}
	result, e = invoke(`mutation($id:ID!){bindPlanProject(id:$id){id projectRoot}}`, `{"id":1234567890123456789}`)
	if e != nil || !strings.Contains(result.Text, `"id":"1234567890123456789"`) {
		t.Fatalf("numeric ID %s %v", result.Text, e)
	}
	actual, e := plan.GetPlan(s, "1234567890123456789")
	if e != nil || actual.ProjectRoot != canonical {
		t.Fatalf("canonical root %+v %v", actual, e)
	}
	result, e = invoke(`mutation {a:putKnowledge(key:"two",body:"one",expectedPreviousId:""){id} b:putKnowledge(key:"two",body:"stale",expectedPreviousId:""){id} c:putKnowledge(key:"three",body:"third",expectedPreviousId:""){id}}`, ``)
	if e == nil || !result.Failed || !strings.Contains(result.Text, `"status":"applied"`) || !strings.Contains(result.Text, `"field":"putKnowledge"`) {
		t.Fatalf("partial receipt lost %s %v", result.Text, e)
	}
	for _, vars := range []string{`null`, `[]`, `{"body":"x"} {}`, strings.Repeat(" ", 65537)} {
		if _, e = invoke(`mutation($body:String!){putKnowledge(key:"never",body:$body,expectedPreviousId:""){id}}`, vars); e == nil {
			t.Fatalf("bad vars accepted %q", vars[:min(len(vars), 30)])
		}
	}
	result, e = invoke(`{ knowledge(key:"never",limit:1){id} }`, ``)
	if e != nil || strings.Contains(result.Text, `"id"`) {
		t.Fatal("invalid variables created note")
	}
}

// Execute the advertised documents through the registered validator and actual scoped API.
func TestRegisteredGraphQLAdvertisedDiscoveryDocuments(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	def, err := New(api.New(s, "test", t.TempDir()), config.Defaults().Tools.GraphQL, t.TempDir(), "discovery", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.Register(def)
	def, _ = reg.Lookup("graphql")
	invoke := func(query string) tool.Result {
		raw, _ := json.Marshal(map[string]string{"query": query})
		args, repair := tool.ValidateJSON(def, string(raw))
		if repair != "" {
			t.Fatal(repair)
		}
		result, err := def.Execute(context.Background(), args)
		if err != nil || result.Failed {
			t.Fatalf("actual GraphQL %s: %s %v", query, result.Text, err)
		}
		return result
	}
	created := invoke(`mutation {createPlan(title:"observed project",goal:"actual work"){id state}}`)
	var envelope struct {
		Data struct{ CreatePlan struct{ ID string } }
	}
	if err := json.Unmarshal([]byte(created.Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.CreatePlan.ID == "" {
		t.Fatalf("no actual ID: %s", created.Text)
	}
	query := `query { plans { id state } }`
	if !strings.Contains(def.Purpose, `help(topic="schema.graphql")`) || !strings.Contains(def.Purpose, `help(topic="schema.graphql.appendKnowledge")`) || !strings.Contains(prompts.Builtin().Text("help.graphql"), query) || !strings.Contains(prompts.Builtin().Text("help.graphql"), `__type(name:"FreshTaskInput")`) {
		t.Fatal("missing reachable compact schema or detailed plan/introspection discovery")
	}
	// Execute the exact first focused-help GraphQL document, including its aliases.
	_, docs, ok := strings.Cut(prompts.Builtin().Text("help.graphql"), "```graphql\n")
	if !ok {
		t.Fatal("missing focused introspection document")
	}
	introspection, _, ok := strings.Cut(docs, "```")
	if !ok {
		t.Fatal("unterminated focused introspection document")
	}
	result := invoke(query)
	if !strings.Contains(result.Text, envelope.Data.CreatePlan.ID) || !strings.Contains(result.Text, `"state":"draft"`) {
		t.Fatalf("project read did not return real node: %s", result.Text)
	}
	result = invoke(introspection)
	var types struct {
		Data struct {
			Type struct {
				InputFields []struct {
					Name string
					Type struct {
						Kind   string
						OfType struct{ Kind, Name string }
					}
				}
			} `json:"input"`
		}
	}
	if err := json.Unmarshal([]byte(result.Text), &types); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, field := range types.Data.Type.InputFields {
		found[field.Name] = field.Type.Kind == "NON_NULL" && field.Type.OfType.Kind == "SCALAR" && field.Type.OfType.Name == "String"
	}
	for _, name := range []string{"planTitle", "planGoal", "approachTitle", "taskTitle", "taskPrompt", "current", "target", "sketch", "acceptance", "tests", "verify", "checkCommand", "acceptanceCommand"} {
		if !found[name] {
			t.Fatalf("advertised introspection missing required fresh-task input %s: %s", name, result.Text)
		}
	}
	if len(found) != 13 {
		t.Fatalf("unexpected fresh-task fields: %s", result.Text)
	}
}
