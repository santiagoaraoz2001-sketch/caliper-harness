package run

import (
	"io"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
	"strings"
	"testing"
)

func TestChildRoleKnowledgeFactoryAndHelp(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.PromptCatalog = prompts.Builtin()
	w := &files.Workspace{Path: root, Caps: cfg.Tools.Bash}
	d := launchDeps(cfg, s, "http://unused", "unused", 0, 32768, strings.NewReader(""), io.Discard, w, "")
	worker, err := d.NewKnowledge("actual-child-task", false)
	if err != nil {
		t.Fatal(err)
	}
	call := func(def tool.Definition, q string) (tool.Result, error) {
		return def.Execute(t.Context(), tool.Args{"query": q})
	}
	created, err := call(worker, `mutation{appendKnowledge(key:"project/design/identity",body:"Copper navigation with readable cream text",expectedPreviousId:""){id sessionRef}}`)
	if err != nil || !strings.Contains(created.Text, "actual-child-task") {
		t.Fatalf("worker author: %s %v", created.Text, err)
	}
	for _, q := range []string{
		`mutation{createPlan(title:"forbidden",goal:"no"){id}}`,
		`query{alias:plans{id}}`,
		`query{...Hidden} fragment Hidden on Query{plans{id}}`,
		`query Allowed{knowledge{key}} mutation Hidden{createPlan(title:"no",goal:"no"){id}}`,
		`mutation{appendKnowledge(key:"must-not-exist",body:"no",expectedPreviousId:""){id} createPlan(title:"no",goal:"no"){id}}`,
	} {
		args := tool.Args{"query": q}
		if strings.Contains(q, "query Allowed") {
			args["operation_name"] = "Allowed"
		}
		if result, err := worker.Execute(t.Context(), args); err == nil {
			t.Fatalf("worker admitted %s: %s", q, result.Text)
		}
	}
	entries, err := memory.Knowledge(t.Context(), s, root, "must-not-exist", "", 1, 10000, true)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial role mutation: %v %v", entries, err)
	}
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), root), s, "review", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempts=1`, map[string]any{"id": task.ID}); err != nil {
		t.Fatal(err)
	}
	reviewer, err := d.NewKnowledge(task.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	reg := d.NewRegistry()
	reg.Register(reviewer)
	reg.Restrict([]string{"read", "help", "graphql"})
	identity, err := call(reviewer, `{knowledge(key:"project/design/identity"){body}}`)
	if err != nil || !strings.Contains(identity.Text, "Copper navigation") {
		t.Fatalf("review identity: %s %v", identity.Text, err)
	}
	if _, err := call(reviewer, `mutation{appendKnowledge(key:"review-write",body:"no",expectedPreviousId:""){id}}`); err == nil {
		t.Fatal("reviewer wrote")
	}
	help, _ := reg.Lookup("help")
	for _, topic := range []string{"", "guide", "graphql", "graphql.knowledge", "schema", "schema.graphql", "schema.graphql.knowledge"} {
		result, err := help.Execute(t.Context(), tool.Args{"topic": topic})
		if err != nil {
			t.Fatal(topic, err)
		}
		for _, forbidden := range []string{"appendKnowledge", "prepareTask(", "transitionPlan(", "noticed("} {
			if strings.Contains(result.Text, forbidden) {
				t.Fatalf("%s leaks %s: %s", topic, forbidden, result.Text)
			}
		}
	}
	for _, topic := range []string{"schema.graphql.appendKnowledge", "schema.graphql.createPlan", "graphql.lifecycle"} {
		result, err := help.Execute(t.Context(), tool.Args{"topic": topic})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Text, "```json") || !strings.Contains(strings.ToLower(result.Text), "unavailable") {
			t.Fatalf("forbidden help %s: %s", topic, result.Text)
		}
	}
}
