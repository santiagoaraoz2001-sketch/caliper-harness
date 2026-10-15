package prompts_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools"
	"specific-harness/internal/tools/files"
	graphTool "specific-harness/internal/tools/graph"
)

// The executor is the real model help route. It renders the operation schema
// and advice from the same active registry/catalog used in requests; no model
// inference, mutation, or graph outcome is fabricated by this control.
func TestEmittedKnowledgeHelpReusesTheRetainedHead(t *testing.T) {
	cfg := config.Defaults()
	pc := prompts.Builtin()
	cfg.PromptCatalog = pc
	reg := tool.NewRegistry(pc)
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash, Prompts: pc}
	tools.RegisterAt(reg, cfg, nil, io.Discard, nil, w)
	g, err := graphTool.New(api.New(nil, "component-help", "sessions"), cfg.Tools.GraphQL, w.Path, "component-help", "component-store", pc)
	if err != nil {
		t.Fatal(err)
	}
	reg.Register(g)
	helper, ok := reg.Lookup("help")
	if !ok {
		t.Fatal("help missing from the actual registry")
	}
	for _, topic := range []string{"schema.graphql.appendKnowledge", "schema.graphql.putKnowledge"} {
		result, err := helper.Execute(context.Background(), tool.Args{"topic": topic})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Text, "first read knowledge") || !strings.Contains(result.Text, "already in context") {
			t.Errorf("actual help %s demands redundant readback: %s", topic, result.Text)
		}
		if !strings.Contains(result.Text, "expectedPreviousId") || !strings.Contains(result.Text, "Never guess") {
			t.Errorf("actual help %s lost version/authority rules: %s", topic, result.Text)
		}
	}
}
