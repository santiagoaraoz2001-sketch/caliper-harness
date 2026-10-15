package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

type episodeLog struct {
	dir         string
	calls, fail int
	cancel      context.CancelFunc
}

func (s *episodeLog) Append(ctx context.Context, id, kind, text string) error {
	s.calls++
	if s.calls == s.fail {
		return errors.New("owned durable receipt failure")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
	if s.calls == 2 && s.cancel != nil {
		s.cancel()
	}
	return err
}
func episodeRunner(t *testing.T, acceptance string) (*agent.Runner, tool.Definition, *episodeLog, string) {
	t.Helper()
	dir := t.TempDir()
	suite := "printf SUITE > suite.txt"
	state, _ := json.Marshal(map[string]any{"task_id": "owned-task", "attempt": 1, "workspace": dir, "command": suite, "acceptance_command": acceptance})
	var c session.CodingState
	if err := json.Unmarshal(state, &c); err != nil {
		t.Fatal(err)
	}
	sink := &episodeLog{dir: t.TempDir()}
	r := &agent.Runner{Session: sink, SessionID: "paired", Workspace: dir, CheckCommand: suite, CodingRequired: true, Continuation: session.Continuation{Coding: &c}}
	return r, r.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, dir)), sink, dir
}
func TestPairedEpisodeBehaviorFailureStopsPassingSuite(t *testing.T) {
	r, def, sink, dir := episodeRunner(t, `printf ACTUAL=; cat behavior.txt; test "$(cat behavior.txt)" = EXPECTED`)
	if err := os.WriteFile(filepath.Join(dir, "behavior.txt"), []byte("WRONG"), 0600); err != nil {
		t.Fatal(err)
	}
	// The independent broad suite passes even with the wrong behavior.
	suite := bash.NewAt(config.Defaults().Tools.Bash, dir)
	broad, err := suite.Execute(context.Background(), tool.Args{"cmd": r.CheckCommand, "timeout": int64(1)})
	if err != nil || broad.ExitCode == nil || *broad.ExitCode != 0 {
		t.Fatalf("broad control %+v %v", broad, err)
	}
	if err := os.Remove(filepath.Join(dir, "suite.txt")); err != nil {
		t.Fatal(err)
	}
	args, repair := tool.ValidateJSON(def, `{}`)
	if repair != "" {
		t.Fatal(repair)
	}
	res, err := def.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if _, e := os.Stat(filepath.Join(dir, "suite.txt")); !os.IsNotExist(e) {
		t.Fatalf("passing suite ran after behavioral failure: %v", e)
	}
	if !res.Failed || r.Continuation.Coding.Passed() {
		t.Fatalf("behavioral failure qualified: %+v", res)
	}
	events, err := session.Read(sink.dir, "paired")
	if err != nil || len(events) != 2 || !strings.Contains(events[1].Text, "ACTUAL=WRONG") || !strings.Contains(events[1].Text, `"status":"unexecuted"`) {
		t.Fatalf("actual failure/tail not archived: %+v %v", events, err)
	}
	if err := r.Continuation.Coding.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestPairedEpisodeApprovalCurrentnessAndDurability(t *testing.T) {
	for _, mode := range []string{"pass", "deny", "cancel-between", "sink-before", "sink-between", "sink-final", "wrong-command"} {
		t.Run(mode, func(t *testing.T) {
			r, def, sink, dir := episodeRunner(t, "printf BEHAVIOR > behavior.txt; printf OBSERVED")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cancel-between":
				sink.cancel = cancel
			case "sink-before":
				sink.fail = 1
			case "sink-between":
				sink.fail = 2
			case "sink-final":
				sink.fail = 4
			}
			args, repair := tool.ValidateJSON(def, `{}`)
			if repair != "" {
				t.Fatal(repair)
			}
			if mode == "wrong-command" {
				args["acceptance_cmd"] = "printf forged"
			}
			var prompt string
			gate := auth.Gate{Mode: config.AuthAll, AskFunc: func(_ context.Context, p string) bool { prompt = p; return mode != "deny" }}
			res, err := gate.Wrap(r.CodingExec(agent.Direct))(ctx, def, args)
			if !strings.Contains(prompt, r.CheckCommand) || !strings.Contains(prompt, args["acceptance_cmd"].(string)) {
				t.Fatalf("approval hid command pair: %s", prompt)
			}
			c := r.Continuation.Coding
			behavior, be := os.ReadFile(filepath.Join(dir, "behavior.txt"))
			_, se := os.Stat(filepath.Join(dir, "suite.txt"))
			if mode == "pass" {
				if err != nil || res.Failed || string(behavior) != "BEHAVIOR" || be != nil || se != nil || !c.Passed() {
					t.Fatalf("pair not current %+v %v %+v", res, err, c)
				}
				events, e := session.Read(sink.dir, "paired")
				if e != nil || len(events) != 4 || !strings.Contains(events[1].Text, "OBSERVED") {
					t.Fatalf("pair durable %+v %v", events, e)
				}
				clone := r.Continuation.Clone()
				c.Receipt.Acceptance.Text = "later live change"
				*c.Receipt.Acceptance.ExitCode = 3
				if clone.Coding.Receipt.Acceptance.Text != "OBSERVED" || !clone.Coding.Passed() {
					t.Fatal("frozen pair aliases live receipt")
				}
				c = clone.Coding
				r.Continuation.Coding = c
				_, e = r.CodingExec(agent.Direct)(context.Background(), bash.NewAt(config.Defaults().Tools.Bash, dir), tool.Args{"cmd": "printf LATER", "timeout": int64(1)})
				if e != nil || c.Passed() || c.Receipt.Status != "stale" {
					t.Fatalf("later opaque effect kept pair current %v %+v", e, c)
				}
				if e = c.Validate(); e != nil {
					t.Fatal(e)
				}
			} else {
				if c.Passed() {
					t.Fatalf("%s promoted %+v", mode, c)
				}
				if mode != "sink-final" && !os.IsNotExist(se) {
					t.Fatalf("tail ran in %s: %v", mode, se)
				}
				if mode == "sink-before" && (c.Receipt.Acceptance.Started != "" || c.Receipt.Acceptance.Status != "unexecuted") {
					t.Fatal("sink-before fabricated attempted acceptance")
				}
				if mode == "deny" || mode == "sink-before" || mode == "wrong-command" {
					if !os.IsNotExist(be) {
						t.Fatalf("first effect ran in %s", mode)
					}
				}
				if mode != "deny" && err == nil {
					t.Fatalf("%s lost failure %+v", mode, res)
				}
				if e := c.Validate(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
