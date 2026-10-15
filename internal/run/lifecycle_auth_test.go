package run

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func lifecycleAuthConfig(t *testing.T) config.Config {
	t.Helper()
	c := config.Defaults()
	c.Templates.Path = "../../templates"
	c.Behavior.Path = filepath.Join(t.TempDir(), "missing-behavior")
	c.Sessions.Path = filepath.Join(t.TempDir(), "sessions")
	return c
}
func TestLifecyclePublicLaunchFactoryAppliesConfiguredAll(t *testing.T) {
	cfg := lifecycleAuthConfig(t)
	cfg.Auth.Mode = config.AuthAll
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := LaunchDeps(cfg, s, "http://unused", "component", 2, 32768, strings.NewReader("n\n"), io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if d.AuthMode != config.AuthAll || d.ReviewGate == nil {
		t.Fatal("actual public LaunchDeps factory omitted configured child auth")
	}
	bash, _ := d.NewRegistry().Lookup("bash")
	marker := filepath.Join(t.TempDir(), "denied-child")
	result, e := d.ReviewGate(agent.Direct)(t.Context(), bash, tool.Args{"cmd": "touch '" + marker + "'"})
	if e != nil || !result.Blocked {
		t.Fatal("actual factory child denial failed", result, e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("denied child effect", e)
	}
}

type lifecycleAuthRoleModel struct{ workers *int }

func (m lifecycleAuthRoleModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	if strings.HasPrefix(msgs[len(msgs)-1].Content, "Review ") {
		return "PASS: component review after actual pair", "component", llm.Usage{}, nil
	}
	*m.workers++
	return "component worker completed", "component", llm.Usage{}, nil
}

// Model/decision answers are explicit component doubles. Public factory, shared
// human callback, owning contracts/claim/check/review/events are actual owners.
func TestLifecycleRiskyFirstAndNextTaskApproval(t *testing.T) {
	for _, denyFirst := range []bool{true, false} {
		t.Run(strings.ToUpper(strings.TrimSpace(map[bool]string{true: "first", false: "next"}[denyFirst])), func(t *testing.T) {
			cfg := lifecycleAuthConfig(t)
			cfg.Auth.Mode = config.AuthRisky
			cfg.Model.Runtime = "openai"
			cfg.Decision.Model, cfg.Decision.Runtime, cfg.Decision.Device = "component", "laya", "cpu"
			cfg.Subagents.Mode = config.SubagentsSequential
			bindDecisionFixture(t, &cfg, nil)
			s, e := store.Open(filepath.Join(t.TempDir(), "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			root, _ := os.Getwd()
			root, e = filepath.EvalSymlinks(root)
			if e != nil {
				t.Fatal(e)
			}
			ctx := plan.WithProject(t.Context(), root)
			p, e := plan.CreatePlanContext(ctx, s, "actual plan", "actual goal")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = plan.TransitionPlan(s, p.ID, plan.PlanActive); e != nil {
				t.Fatal(e)
			}
			a, e := plan.CreateApproach(s, p.ID, "actual strategy")
			if e != nil {
				t.Fatal(e)
			}
			ids := []string{}
			for _, name := range []string{"first", "next"} {
				x, e := plan.CreateTask(s, a.ID, name, "complete bounded fixture")
				if e != nil {
					t.Fatal(e)
				}
				ids = append(ids, x.ID)
				if _, e = plan.SetTaskBlock(s, x.ID, "current", "target", "inspect implement regression", "actual pair exits0", "fixture suite", "Review actual evidence", "printf SUITE", "printf ACCEPTANCE"); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = plan.Ready(ctx, s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); e != nil {
				t.Fatal(e)
			}
			workers, taskAsks, completionBeforeNext := 0, 0, false
			confirm := func(_ context.Context, text string) bool {
				if !strings.HasPrefix(text, "Approve completed task ") {
					return true
				}
				taskAsks++
				if denyFirst {
					return false
				}
				if strings.Contains(text, ids[1]) {
					entries, e := session.List(cfg.Sessions.Path)
					if e == nil && len(entries) == 1 {
						events, e := session.Read(cfg.Sessions.Path, entries[0])
						if e == nil {
							for _, ev := range events {
								if ev.Kind == "task_settled" && strings.Contains(ev.Text, ids[0]) && strings.Contains(ev.Text, "qualified=true") {
									completionBeforeNext = true
								}
							}
						}
					}
					return false
				}
				return true
			}
			d, e := LaunchDeps(cfg, s, "http://unused", "component", 2, 32768, nil, io.Discard, confirm)
			if e != nil {
				t.Fatal(e)
			}
			d.NewModel = func() agent.Model { return lifecycleAuthRoleModel{&workers} }
			results, e := launch.Launch(ctx, d, a.ID)
			if e == nil {
				t.Fatal("declined task admission was hidden")
			}
			first, _ := plan.GetTask(s, ids[0])
			next, _ := plan.GetTask(s, ids[1])
			if next.State != plan.TaskQueued || next.Attempts != 0 || len(results) != 2 {
				t.Fatal("denied next task executed", next, results, e)
			}
			if denyFirst {
				if workers != 0 || first.State != plan.TaskQueued || first.Attempts != 0 || taskAsks != 1 {
					t.Fatal("first denial admitted work", workers, first, taskAsks)
				}
			} else if workers != 1 || !first.Qualified() || taskAsks != 2 || !completionBeforeNext {
				t.Fatal("qualified handoff/notification/next authority incorrect", workers, first, taskAsks, completionBeforeNext)
			}
		})
	}
}
