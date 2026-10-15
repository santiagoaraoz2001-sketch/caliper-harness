package tool_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Captured from the actual GPU Gemma Task A; model text is not corrected.
const gemmaMalformedFence = "```tool<audio|>: bash\ncmd: <<END\nls -R\necho \"Listing files to identify the project structure.\"\nEND\n```"

func TestAttemptedFenceRefusesMalformedCalls(t *testing.T) {
	valid := "```tool\ntool: bash\ncmd: true\n```"
	for _, raw := range []string{gemmaMalformedFence, "```tool bash\ncmd: true\n```", "```tool: bash\ncmd: true\n```", "```tool=bash\ncmd: true\n```", "```tool\tbash", valid + "\n" + gemmaMalformedFence, gemmaMalformedFence + "\n" + valid, strings.ReplaceAll(gemmaMalformedFence, "\n", "\r\n")} {
		t.Run(raw, func(t *testing.T) {
			c, f, q := tool.Extract(raw)
			if !f || c.Name != "" || c.Slots != nil || !strings.Contains(q, "no calls ran") {
				t.Fatalf("attempt admitted: found=%v repair=%q call=%+v", f, q, c)
			}
		})
	}
}

func TestAttemptedFenceSourceAndLiteralData(t *testing.T) {
	exact := "```tool\ntool: bash\ncmd: true\n```"
	for _, raw := range []string{"ordinary prose", "inline ```tool<audio|>: bash", "\\```tool<audio|>: bash", "> ```tool<audio|>: bash", "```toolbox\nhello\n```", "```toolman\nhello\n```", "```tool_name\nhello\n```", "```tool-example\nhello\n```", "````text\n" + exact + "\n````", "~~~text\n" + exact + "\n~~~", "````text\n" + gemmaMalformedFence + "\n````", "~~~text\n" + gemmaMalformedFence + "\n~~~~", "````text\n```\n" + gemmaMalformedFence + "\n````"} {
		t.Run(raw, func(t *testing.T) {
			c, f, q := tool.Extract(raw)
			if f || q != "" || c.Name != "" {
				t.Fatalf("source became tool: %+v found=%v repair=%q", c, f, q)
			}
		})
	}
	data := "# keep comment\r\n" + strings.ReplaceAll(gemmaMalformedFence, "\n", "\r\n") + "\r\n~~~text\r\n````\r\n"
	raw := "```tool\r\n# cmd: <<COMMENT\r\ntool: bash\r\ncmd: <<RAW\r\n" + data + "RAW\r\n```"
	c, f, q := tool.Extract(raw)
	if !f || q != "" || c.Name != "bash" || c.Slots["cmd"] != data {
		t.Fatalf("literal bytes drift: %+v %v %q", c, f, q)
	}
	c, f, q = tool.Extract("````text\n" + exact + "\n````\n" + exact)
	if !f || q != "" || c.Name != "bash" || c.Slots["cmd"] != "true" {
		t.Fatalf("standalone counterpart lost: %+v %v %q", c, f, q)
	}
}

type fenceModel struct {
	replies []string
	seen    [][]llm.Message
}

func (m *fenceModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	m.seen = append(m.seen, append([]llm.Message(nil), msgs...))
	reply := m.replies[0]
	m.replies = m.replies[1:]
	return reply, "scripted-component", llm.Usage{}, nil
}

type fenceLog []struct{ kind, text string }

func (l *fenceLog) Append(_ context.Context, _ string, kind, text string) error {
	*l = append(*l, struct{ kind, text string }{kind, text})
	return nil
}

// Scripted component control uses actual Runner/registered Bash, not inference.
func TestAttemptedFenceRunnerNoEffectsAndRecovery(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "source example"}[source], func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "effect")
			valid := "```tool\ntool: bash\ncmd: printf accepted > '" + file + "'\n```"
			raw := strings.Replace(valid, "```tool", "```tool<audio|>: bash", 1)
			if source {
				raw = "````text\n" + valid + "\n````"
			}
			reg := tool.NewRegistry()
			reg.Register(bash.New(config.Defaults().Tools.Bash))
			model := &fenceModel{replies: []string{raw}}
			var log fenceLog
			r := agent.Runner{Model: model, Registry: reg, MaxSteps: 1, Session: &log, SessionID: "owned-component"}
			_, _, _ = r.Run(context.Background(), "do not execute source examples or malformed calls")
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatalf("refused/data command executed: %v", err)
			}
			if !source {
				if r.Outcome.Status == outcome.Completed {
					t.Fatal("malformed attempt settled completed")
				}
				repaired := false
				rawKept := false
				for _, e := range log {
					if e.kind == "result" && strings.Contains(e.text, "no calls ran") {
						repaired = true
					}
					if e.kind == "reply" && e.text == raw {
						rawKept = true
					}
				}
				if !repaired || !rawKept {
					t.Fatal("repair result/raw reply not retained")
				}
				model = &fenceModel{replies: []string{raw, valid, "done"}}
				r = agent.Runner{Model: model, Registry: reg, MaxSteps: 3}
				_, _, err := r.Run(context.Background(), "component recovery")
				got, readErr := os.ReadFile(file)
				if err != nil || readErr != nil || string(got) != "accepted" {
					t.Fatalf("recovery actual Bash: %q %v %v", got, err, readErr)
				}
				if !strings.Contains(model.seen[1][len(model.seen[1])-1].Content, "no calls ran") {
					t.Fatal("second request lacked repair")
				}
			}
		})
	}
}
