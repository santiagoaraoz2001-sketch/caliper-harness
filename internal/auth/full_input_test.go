package auth

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

func TestRiskyJudgmentKeepsCompleteTypedAction(t *testing.T) {
	dir := t.TempDir()
	reg := tool.NewRegistry()
	reg.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
	def, ok := reg.Lookup("bash")
	if !ok {
		t.Fatal("missing Bash")
	}
	command := ": # " + strings.Repeat("preview padding ", 20) + "\r\nprintf '%s' 'ρ-suffix' > marker"
	args, repair := tool.Validate(def, map[string]string{"cmd": command})
	if repair != "" {
		t.Fatal(repair)
	}
	var contexts []string
	gate := Gate{Mode: config.AuthRisky, MaxArgChars: 80, Threshold: .7,
		Judge: func(_ context.Context, req decision.Request) (decision.Verdict, error) {
			contexts = append(contexts, req.Context)
			return decision.Verdict{Option: "deny", Confidence: .9}, nil
		}}
	execute := gate.Wrap(agent.Direct)
	result, err := execute(context.Background(), def, args)
	if err != nil || !result.Blocked {
		t.Fatalf("deny: %+v %v", result, err)
	}
	if _, err := os.Stat(dir + "/marker"); !os.IsNotExist(err) {
		t.Fatal("denied command had effects")
	}
	var action struct {
		Tool string `json:"tool"`
		Args struct {
			Cmd     string `json:"cmd"`
			Timeout int64  `json:"timeout"`
		} `json:"args"`
	}
	if err := json.Unmarshal([]byte(contexts[0]), &action); err != nil {
		t.Fatalf("actual context is not typed JSON: %s (%v)", contexts[0], err)
	}
	if action.Tool != "bash" || action.Args.Cmd != command || action.Args.Timeout != args["timeout"] {
		t.Fatalf("full typed action changed: %+v", action)
	}
	gate.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		if req.Context != contexts[0] {
			t.Fatal("nondeterministic action")
		}
		return decision.Verdict{Option: "allow", Confidence: .9}, nil
	}
	result, err = gate.Wrap(agent.Direct)(context.Background(), def, args)
	if err != nil || result.Failed || result.Blocked {
		t.Fatalf("allow: %+v %v", result, err)
	}
	got, err := os.ReadFile(dir + "/marker")
	if err != nil || string(got) != "ρ-suffix" {
		t.Fatalf("actual Bash effect %q %v", got, err)
	}
	if strings.Contains(formatArgs(args, 80), "ρ-suffix") || !strings.Contains(formatArgs(args, 80), "…") {
		t.Fatal("manual preview cap changed")
	}
}

func TestRiskyJudgmentRefusesUnencodableAction(t *testing.T) {
	for _, value := range []any{math.NaN(), make(chan int)} {
		var judged, ran int
		gate := Gate{Mode: config.AuthRisky, Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
			judged++
			return decision.Verdict{Option: "allow", Confidence: .9}, nil
		}}
		result, err := gate.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": value})
		if err == nil && !result.Blocked || judged != 0 || ran != 0 {
			t.Fatalf("unencodable action reached judge/effect: result=%+v err=%v judged=%d ran=%d", result, err, judged, ran)
		}
	}
}

func TestRiskyJudgmentJSONRetainsExactIntegerAndBoolean(t *testing.T) {
	gate := Gate{Mode: config.AuthRisky, Judge: func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		decoder := json.NewDecoder(strings.NewReader(req.Context))
		decoder.UseNumber()
		var action map[string]any
		if err := decoder.Decode(&action); err != nil {
			t.Fatal(err)
		}
		got := action["args"].(map[string]any)
		if got["n"] != json.Number("9007199254740993") || got["enabled"] != true || got["empty"] != "" {
			t.Fatalf("typed JSON changed: %+v", got)
		}
		return decision.Verdict{Option: "deny", Confidence: .9}, nil
	}}
	_, err := gate.Wrap(counting(new(int)))(context.Background(), tool.Definition{Name: "fixture"}, tool.Args{"n": int64(9007199254740993), "enabled": true, "empty": ""})
	if err != nil {
		t.Fatal(err)
	}
}
