package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	graphTool "specific-harness/internal/tools/graph"
)

// Replay the observed discovery route with real GraphQL validation/storage;
// no model inference or hypothetical successful design application is implied.
func TestObservedGraphEntryAndMixedFieldRecovery(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, pc := config.Defaults(), prompts.Builtin()
	root := t.TempDir()
	// This route test owns schema/storage fidelity. Exact tokenizer admission has
	// separate endpoint and boundary controls; supply its count-only dependency.
	a := api.New(s, "entry", t.TempDir()).WithDomainTokenCounter(func(context.Context, string) (int, error) { return 100, nil })
	d, err := graphTool.New(a, cfg.Tools.GraphQL, root, "entry", s.Identity(), pc)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry(pc)
	reg.Register(d)
	reg.Register(helpDefinition(cfg, reg, pc))
	h, err := helpCall(t, reg, "schema.graphql.appendKnowledge")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Text) > 1900 {
		t.Fatalf("entry help grew beyond its compact budget: %d", len(h.Text))
	}
	t.Logf("advertised append schema help: %d bytes", len(h.Text))
	_, tail, found := strings.Cut(h.Text, "```graphql\n")
	query, _, closed := strings.Cut(tail, "```")
	if !found || !closed {
		t.Fatal("advertised write-schema route lacks executable prerequisite read")
	}
	if !strings.Contains(query, `key:"project/design/identity"`) || !strings.Contains(query, `section:"identity"`) {
		t.Fatal(query)
	}
	for _, topic := range []string{"schema.graphql.knowledge", "graphql.knowledge"} {
		other, err := helpCall(t, reg, topic)
		if err != nil || !strings.Contains(other.Text, query) {
			t.Fatalf("%s lost the shared first-use recipe: %v", topic, err)
		}
	}
	read := func() int {
		t.Helper()
		res, err := d.Execute(t.Context(), tool.Args{"query": query})
		if err != nil || res.Failed {
			t.Fatalf("documented read failed: %s %v", res.Text, err)
		}
		var got struct {
			Data struct {
				Identity []struct{ ID, Body string }
				Standard []struct {
					Sections []struct {
						Body       string
						References []struct{ Key, Section, Label string }
					}
				}
			}
		}
		if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data.Standard) != 1 || len(got.Data.Standard[0].Sections) != 1 {
			t.Fatal(res.Text)
		}
		section := got.Data.Standard[0].Sections[0]
		if !strings.Contains(section.Body, "## identity") || strings.Contains(section.Body, "## typography") || len(section.References) != 1 || section.References[0].Key != "caliper/guides/ui/direction" || section.References[0].Section != "brief-to-direction" || section.References[0].Label == "" {
			t.Fatal(res.Text)
		}
		if len(got.Data.Identity) > 0 && (got.Data.Identity[0].ID == "" || got.Data.Identity[0].Body != "Brief-derived direction") {
			t.Fatal(res.Text)
		}
		return len(got.Data.Identity)
	}
	if read() != 0 {
		t.Fatal("new project has invented identity")
	}
	for _, invalid := range []string{
		`{ knowledge(key:"project/design/identity") { id } project { name } }`,
		`{ project { name } knowledge(key:"project/design/identity") { id } }`,
	} {
		res, err := d.Execute(t.Context(), tool.Args{"query": invalid})
		if err == nil || !res.Failed || !strings.Contains(err.Error(), `help(topic="schema.graphql")`) || strings.Contains(err.Error(), `schema.graphql.knowledge`) {
			t.Fatalf("mixed query points to an unrelated first field: %s %v", res.Text, err)
		}
	}
	call := nativeHelpTemplate(t, h.Text)
	write := strings.NewReplacer("<key>", "project/design/identity", "<body>", "Brief-derived direction", "<expectedPreviousId>", "").Replace(call.Arguments["query"].(string))
	res, err := d.Execute(t.Context(), tool.Args{"query": write})
	if err != nil || res.Failed {
		t.Fatalf("documented creation failed: %s %v", res.Text, err)
	}
	if read() != 1 {
		t.Fatal("saved identity not readable through same recipe")
	}
}

func TestKnowledgeHelpAuthorityMatchesCurrentRole(t *testing.T) {
	cfg, pc := config.Defaults(), prompts.Builtin()
	for _, role := range []struct {
		name            string
		fields          []string
		restrictedWrite bool
	}{
		{"ordinary", nil, false}, {"worker", []string{"knowledge", "appendKnowledge"}, true}, {"reviewer", []string{"knowledge"}, false},
	} {
		t.Run(role.name, func(t *testing.T) {
			a := api.New(nil, "role-help", t.TempDir())
			if role.fields != nil {
				a = a.WithAllowedFields(role.fields...)
			}
			d, err := graphTool.New(a, cfg.Tools.GraphQL, t.TempDir(), "role-help", "store", pc)
			if err != nil {
				t.Fatal(err)
			}
			reg := tool.NewRegistry(pc)
			reg.Register(d)
			reg.Register(helpDefinition(cfg, reg, pc))
			res, err := helpCall(t, reg, "graphql.knowledge")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(res.Text, "cannot change PAT lifecycle") != role.restrictedWrite {
				t.Fatalf("wrong role restriction: %s", res.Text)
			}
			if role.name == "reviewer" && strings.Contains(res.Text, "appendKnowledge") {
				t.Fatal("reviewer advertised write")
			}
			if role.name == "ordinary" && d.GraphQLFields != nil {
				t.Fatal("ordinary full authority unexpectedly narrowed")
			}
		})
	}
}
