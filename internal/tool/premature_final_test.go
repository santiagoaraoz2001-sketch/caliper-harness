package tool_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Exact native message.content from Gemma coding Task A, request26.
const gemmaPrematureFinal = "<|channel>::```tool\ntool: bash\ncmd: cat internal/tools/search/search.go\n```"

func TestPrematureFinalRefusesPrefixedToolIntent(t *testing.T) {
	valid := "```tool\ntool: bash\ncmd: true\n```"
	for _, raw := range []string{gemmaPrematureFinal, strings.ReplaceAll(gemmaPrematureFinal, "\n", "\r\n"), "<|channel|>```tool\ntool: bash\ncmd: true\n```", "<|channel>::```tool<audio|>: bash\ncmd: true\n```", valid + "\n" + gemmaPrematureFinal, gemmaPrematureFinal + "\n" + valid} {
		t.Run(raw, func(t *testing.T) {
			c, f, q := tool.Extract(raw)
			if !f || c.Name != "" || c.Slots != nil || !strings.Contains(q, "no calls ran") {
				t.Fatalf("prefixed attempt: call=%+v found=%v repair=%q", c, f, q)
			}
		})
	}
}

func TestPrematureFinalKeepsDataAndValidCalls(t *testing.T) {
	for _, raw := range []string{"ordinary prose", "<|channel>:: ordinary prose", "inline " + gemmaPrematureFinal, "\\" + gemmaPrematureFinal, "> " + gemmaPrematureFinal, "<|channel>::```toolbox\nexample\n```", "````text\n" + gemmaPrematureFinal + "\n````", "~~~text\n" + gemmaPrematureFinal + "\n~~~"} {
		t.Run(raw, func(t *testing.T) {
			c, f, q := tool.Extract(raw)
			if f || q != "" || c.Name != "" {
				t.Fatalf("data classified as attempt: %+v %v %q", c, f, q)
			}
		})
	}
	data := strings.ReplaceAll(gemmaPrematureFinal, "\n", "\r\n") + "\r\n"
	raw := "```tool\r\ntool: bash\r\ncmd: <<DATA\r\n" + data + "DATA\r\n```"
	c, f, q := tool.Extract(raw)
	if !f || q != "" || c.Name != "bash" || c.Slots["cmd"] != data {
		t.Fatalf("literal changed: %+v %v %q", c, f, q)
	}
	c, f, q = tool.Extract("```tool\ntool: bash\ncmd: true\n```")
	if !f || q != "" || c.Name != "bash" || c.Slots["cmd"] != "true" {
		t.Fatalf("valid changed: %+v %v %q", c, f, q)
	}
}

// Scripted component control, actual production Runner and registered Bash.
func TestPrematureFinalRunnerRefusalAndRecovery(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact captured reply", true: "valid then malformed refuses all"}[mixed], func(t *testing.T) {
			dir := t.TempDir()
			forbidden := filepath.Join(dir, "forbidden")
			accepted := filepath.Join(dir, "accepted")
			block := func(cmd string) string { return "```tool\ntool: bash\ncmd: " + cmd + "\n```" }
			raw := gemmaPrematureFinal
			if mixed {
				raw = block("touch '"+forbidden+"'") + "\n<|channel>::" + block("touch '"+forbidden+"'")
			}
			reg := tool.NewRegistry()
			reg.Register(bash.New(config.Defaults().Tools.Bash))
			var log fenceLog
			model := &fenceModel{replies: []string{raw}}
			r := agent.Runner{Model: model, Registry: reg, MaxSteps: 1, Session: &log, SessionID: "owned-component"}
			_, _, err := r.Run(context.Background(), "refuse malformed attempt")
			if err != nil || r.Outcome.Status != outcome.Stopped {
				t.Fatalf("refusal outcome=%+v err=%v", r.Outcome, err)
			}
			if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
				t.Fatalf("ambiguous reply executed: %v", err)
			}
			rawKept, repairKept := false, false
			for _, e := range log {
				rawKept = rawKept || e.kind == "reply" && e.text == raw
				repairKept = repairKept || e.kind == "result" && strings.Contains(e.text, "no calls ran")
			}
			if !rawKept || !repairKept {
				t.Fatal("raw reply or refusal omitted")
			}
			model = &fenceModel{replies: []string{raw, block("printf accepted > '" + accepted + "'"), "done"}}
			r = agent.Runner{Model: model, Registry: reg, MaxSteps: 3}
			_, _, err = r.Run(context.Background(), "repair then execute one valid call")
			got, readErr := os.ReadFile(accepted)
			if err != nil || readErr != nil || string(got) != "accepted" || len(model.seen) != 3 {
				t.Fatalf("recovery: got=%q err=%v readErr=%v requests=%d", got, err, readErr, len(model.seen))
			}
			if !strings.Contains(model.seen[1][len(model.seen[1])-1].Content, "no calls ran") {
				t.Fatal("model did not receive repair")
			}
			if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
				t.Fatalf("recovery also executed forbidden: %v", err)
			}
			t.Logf("raw preserved; malformed effects0; corrected Bash exact=%q; requests=%d", got, len(model.seen))
		})
	}
}
