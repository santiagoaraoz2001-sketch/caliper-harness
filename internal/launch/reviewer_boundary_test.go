package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestReviewerRepairsVerdictWithoutRestartingWork(t *testing.T) {
	workers, reviews := 0, 0
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		if strings.HasPrefix(fixtureRoleRequest(messages), "Review ") {
			reviews++
			if reviews == 1 {
				return "Everything is complete and verified.", nil
			}
			if !strings.Contains(messages[len(messages)-1].Content, "PASS") {
				t.Error("missing verdict feedback")
			}
			return "PASS: actual paired checks support this task", nil
		}
		workers++
		return "component artifact", nil
	})
	d, aid, _, _ := roleHandoffFixture(t, 1, model)
	results, err := Launch(t.Context(), d, aid)
	if err != nil || len(results) != 1 || !results[0].Qualified() || workers != 1 || reviews != 2 {
		t.Fatalf("workers%d reviews%d results%+v err%v", workers, reviews, results, err)
	}
}

// Free-form commands are refused before effects. The frozen check still runs
// its authorized script, whose project mutations must stop qualification.
func TestReviewerShellMutationCannotQualify(t *testing.T) {
	for _, mode := range []string{"bash", "forged check", "check"} {
		t.Run(mode, func(t *testing.T) {
			reviews := 0
			reviewing := false
			model := roleHandoffModel(func(messages []llm.Message) (string, error) {
				if !strings.HasPrefix(fixtureRoleRequest(messages), "Review ") {
					return "worker result", nil
				}
				reviews++
				reviewing = true
				for _, fact := range []string{"reviewing this task only: t0", "Frozen request: prompt-t0", "Target: after", "Acceptance: exit code is 0"} {
					if !strings.Contains(messages[1].Content, fact) {
						t.Errorf("protected role lost frozen fact %q", fact)
					}
				}
				if reviews == 1 {
					if mode == "bash" {
						return "```tool\ntool: bash\ncmd: printf MUTATED > source.js\n```", nil
					}
					if mode == "forged check" {
						return "```tool\ntool: check\ncmd: printf MUTATED > source.js\nacceptance_cmd: printf FIXTURE_ACCEPTANCE\n```", nil
					}
					return "```tool\ntool: check\ncmd: printf FIXTURE_SUITE\nacceptance_cmd: printf FIXTURE_ACCEPTANCE\n```", nil
				}
				return "UNVERIFIED: required additional probe is unavailable; no further execution claimed", nil
			})
			d, aid, _, root := roleHandoffFixture(t, 1, model)
			if err := os.WriteFile(filepath.Join(root, "source.js"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			factory := d.NewRegistry
			d.NewRegistry = func() *tool.Registry {
				original := factory()
				if mode != "check" {
					return original
				}
				// A configured project check script may itself edit source. Execute that
				// actual effect inside the nested Bash path, retaining the frozen schema.
				reg := tool.NewRegistry()
				for _, name := range original.Names() {
					def, _ := original.Lookup(name)
					if name == "bash" {
						run := def.Execute
						def.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
							copy := tool.Args{}
							for k, v := range args {
								copy[k] = v
							}
							if reviewing {
								copy["cmd"] = fmt.Sprint(args["cmd"]) + "; printf MUTATED > source.js"
							}
							return run(ctx, copy)
						}
					}
					reg.Register(def)
				}
				return reg
			}
			results, err := Launch(t.Context(), d, aid)
			content, readErr := os.ReadFile(filepath.Join(root, "source.js"))
			wantContent, wantReviews := "original", 2
			if mode == "check" {
				wantContent, wantReviews = "MUTATED", 1
			}
			if err != nil || readErr != nil || string(content) != wantContent || len(results) != 1 || results[0].Qualified() || reviews != wantReviews || mode == "check" && !strings.Contains(results[0].VerificationReason, "reviewer shell modified") {
				t.Fatalf("mutation escaped reviewer: reviews%d content%q results%+v err%v/%v", reviews, content, results, err, readErr)
			}
		})
	}
}
