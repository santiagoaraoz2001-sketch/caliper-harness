package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

func TestManualInspectionCompleteAction(t *testing.T) {
	def := tool.Definition{Name: "fixture", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString, Required: true}, {Name: "empty", Type: tool.TypeString, Required: true, AllowEmpty: true}, {Name: "n", Type: tool.TypeInt, Required: true}, {Name: "enabled", Type: tool.TypeBool, Required: true}}}
	raw := `{"cmd":"prefix \u001b[2J\u007f\u009b` + strings.Repeat("pad ", 35) + `ρ-suffix\r\n\tend","empty":"","n":9007199254740993,"enabled":true}`
	args, repair := tool.ValidateJSON(def, raw)
	if repair != "" {
		t.Fatal(repair)
	}
	for _, mode := range []string{config.AuthAll, config.AuthRisky} {
		t.Run(mode, func(t *testing.T) {
			var prompt, judged string
			ran := 0
			g := Gate{Mode: mode, MaxArgChars: 80, Threshold: .7, Judge: func(_ context.Context, r decision.Request) (decision.Verdict, error) {
				judged = r.Context
				return decision.Verdict{Option: "deny", Confidence: .5}, nil
			}, AskFunc: func(_ context.Context, p string) bool { prompt = p; return true }}
			_, err := g.Wrap(func(_ context.Context, _ tool.Definition, actual tool.Args) (tool.Result, error) {
				ran++
				if !reflect.DeepEqual(actual, args) {
					t.Fatal("effect args changed")
				}
				return tool.Result{}, nil
			})(context.Background(), def, args)
			if err != nil || ran != 1 {
				t.Fatalf("effect: %d %v", ran, err)
			}
			_, body, ok := strings.Cut(prompt, "Complete validated action (JSON):\n")
			if !ok {
				t.Fatalf("full action unavailable: %q", prompt)
			}
			if strings.ContainsAny(prompt, "\x1b\x7f\u009b") {
				t.Fatal("literal terminal control in manual prompt")
			}
			var displayed struct {
				Tool string         `json:"tool"`
				Args map[string]any `json:"args"`
			}
			dec := json.NewDecoder(strings.NewReader(body))
			dec.UseNumber()
			if err = dec.Decode(&displayed); err != nil {
				t.Fatal(err)
			}
			if displayed.Tool != def.Name || displayed.Args["cmd"] != args["cmd"] || displayed.Args["empty"] != "" || displayed.Args["enabled"] != true || displayed.Args["n"] != json.Number("9007199254740993") {
				t.Fatalf("displayed typed action changed: %+v", displayed)
			}
			preview, _, _ := strings.Cut(prompt, "\nComplete validated action")
			if !strings.Contains(preview, "…") || strings.Contains(preview, "ρ-suffix") {
				t.Fatal("preview cap changed")
			}
			if mode == config.AuthRisky {
				want, _ := json.Marshal(struct {
					Tool string    `json:"tool"`
					Args tool.Args `json:"args"`
				}{def.Name, args})
				if judged != string(want) {
					t.Fatal("judge bytes changed for display")
				}
			}
		})
	}
}

func TestManualInspectionRegisteredBashAndCLI(t *testing.T) {
	for _, answer := range []string{"n\n", "y\n", ""} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			dir := t.TempDir()
			reg := tool.NewRegistry()
			reg.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
			def, _ := reg.Lookup("bash")
			cmd := ": # " + strings.Repeat("padding ", 50) + "\n" + "printf '%s' 'ρ-suffix' > marker"
			args, repair := tool.Validate(def, map[string]string{"cmd": cmd})
			if repair != "" {
				t.Fatal(repair)
			}
			var out bytes.Buffer
			g := Gate{Mode: config.AuthAll, MaxArgChars: 10, In: strings.NewReader(answer), Out: &out}
			res, err := g.Wrap(agent.Direct)(context.Background(), def, args)
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := os.ReadFile(dir + "/marker")
			if answer == "y\n" {
				if readErr != nil || string(data) != "ρ-suffix" || res.Blocked {
					t.Fatalf("approved effect: %s %+v %v", data, res, readErr)
				}
			} else if !os.IsNotExist(readErr) || !res.Blocked {
				t.Fatalf("denial effects: %+v %v", res, readErr)
			}
			if !strings.Contains(out.String(), "ρ-suffix") || !strings.HasSuffix(out.String(), "[y/n] ") {
				t.Fatal("CLI did not expose complete suffix before its existing read")
			}
		})
	}
}

func TestManualInspectionCancellationAndBadEncoding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ran := 0
	g := Gate{Mode: config.AuthAll, AskFunc: func(context.Context, string) bool { cancel(); return true }}
	_, err := g.Wrap(counting(&ran))(ctx, fakeDef(), tool.Args{"cmd": "x"})
	if err == nil || ran != 0 {
		t.Fatalf("cancel admitted: %d %v", ran, err)
	}
	for _, mode := range []string{config.AuthAll, config.AuthRisky} {
		g = Gate{Mode: mode, AskFunc: func(context.Context, string) bool { t.Fatal("bad action asked"); return true }}
		res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": math.NaN()})
		if !res.Blocked && err == nil {
			t.Fatal("bad action not refused")
		}
	}
}
