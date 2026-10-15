package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
)

var errTestDown = errors.New("sidecar down")

func fakeDef() tool.Definition {
	return tool.Definition{Name: "bash", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString}}}
}

func namedDef(name string) tool.Definition {
	return tool.Definition{Name: name, Params: []tool.Param{{Name: "q", Type: tool.TypeString}}}
}

func counting(next *int) agent.Exec {
	return func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		*next++
		return tool.Result{Text: "ran"}, nil
	}
}

func stubJudge(v decision.Verdict, err error) Judge {
	return func(context.Context, decision.Request) (decision.Verdict, error) { return v, err }
}

func TestYoloRunsFree(t *testing.T) {
	var ran int
	g := &Gate{Mode: config.AuthYOLO, In: strings.NewReader(""), Out: &bytes.Buffer{},
		Judge: func(context.Context, decision.Request) (decision.Verdict, error) { panic("yolo must not judge") }}
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "x"})
	if err != nil || res.Text != "ran" || ran != 1 {
		t.Errorf("res=%v err=%v ran=%d", res, err, ran)
	}
}

func TestAllApprovesOnYes(t *testing.T) {
	var ran int
	var out bytes.Buffer
	g := &Gate{Mode: config.AuthAll, In: strings.NewReader("y\n"), Out: &out}
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "rm -rf /"})
	if err != nil || res.Text != "ran" || ran != 1 {
		t.Fatalf("res=%v err=%v ran=%d", res, err, ran)
	}
	if !strings.Contains(out.String(), `Preview: cmd="rm -rf /"`) || !strings.Contains(out.String(), "Complete validated action (JSON):") {
		t.Errorf("prompt=%q", out.String())
	}
}

func TestAllDeclinesAsResult(t *testing.T) {
	var ran int
	g := &Gate{Mode: config.AuthAll, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}}
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "x"})
	if err != nil {
		t.Fatalf("decline must not error: %v", err)
	}
	if res.Text != "declined by user" || ran != 0 {
		t.Errorf("res=%v ran=%d", res, ran)
	}
}

func riskyGate(j Judge) (*Gate, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Gate{Mode: config.AuthRisky, In: strings.NewReader(""), Out: out,
		Judge: j, Threshold: 0.7, Judged: []string{"bash", "fetch"}, Free: []string{"search", "ask"}}, out
}

func TestRiskyAllowsConfident(t *testing.T) {
	var ran int
	var got decision.Request
	g, out := riskyGate(func(ctx context.Context, r decision.Request) (decision.Verdict, error) {
		got = r
		return decision.Verdict{Option: "allow", Confidence: 0.9}, nil
	})
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "ls"})
	if err != nil || res.Text != "ran" || ran != 1 {
		t.Fatalf("res=%v err=%v ran=%d", res, err, ran)
	}
	if out.Len() != 0 {
		t.Errorf("confident verdict must not prompt, out=%q", out.String())
	}
	if len(got.Options) != 2 || got.Options[0] != "allow" || got.Options[1] != "deny" {
		t.Errorf("options=%v", got.Options)
	}
	if !strings.Contains(got.Context, "bash") || !strings.Contains(got.Instructions, "allow or deny") {
		t.Errorf("question=%+v", got)
	}
}

func TestRiskyDeniesAtThreshold(t *testing.T) {
	var ran int
	g, _ := riskyGate(stubJudge(decision.Verdict{Option: "deny", Confidence: 0.7}, nil))
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "rm -rf /"})
	if err != nil {
		t.Fatalf("deny must not error: %v", err)
	}
	if !strings.Contains(res.Text, "declined by policy") || ran != 0 {
		t.Errorf("res=%v ran=%d", res, ran)
	}
}

func TestRiskyUnsureAsks(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantRan int
		wantRes string
	}{
		{"y\n", 1, "ran"},
		{"n\n", 0, "declined by user"},
	} {
		var ran int
		var out bytes.Buffer
		g := &Gate{Mode: config.AuthRisky, In: strings.NewReader(tc.in), Out: &out,
			Judge: stubJudge(decision.Verdict{Option: "allow", Confidence: 0.5}, nil), Threshold: 0.7}
		res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "x"})
		if err != nil || res.Text != tc.wantRes || ran != tc.wantRan {
			t.Errorf("in=%q res=%v err=%v ran=%d", tc.in, res, err, ran)
		}
		if !strings.Contains(out.String(), "unsure") || !strings.Contains(out.String(), "leans allow") {
			t.Errorf("in=%q prompt=%q", tc.in, out.String())
		}
	}
}

func TestRiskyJudgeErrorBlocks(t *testing.T) {
	var ran int
	g, _ := riskyGate(stubJudge(decision.Verdict{}, errTestDown))
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "x"})
	if err != nil {
		t.Fatalf("block must not error: %v", err)
	}
	if !strings.Contains(res.Text, "blocked") || !strings.Contains(res.Text, "sidecar") || ran != 0 {
		t.Errorf("res=%v ran=%d", res, ran)
	}
}

func TestRiskyNilJudgeBlocks(t *testing.T) {
	var ran int
	g := &Gate{Mode: config.AuthRisky, In: strings.NewReader(""), Out: &bytes.Buffer{}, Threshold: 0.7}
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "x"})
	if err != nil {
		t.Fatalf("block must not error: %v", err)
	}
	if !strings.Contains(res.Text, "blocked") || ran != 0 {
		t.Errorf("res=%v ran=%d", res, ran)
	}
}

func TestRiskyFreeSkipsJudge(t *testing.T) {
	var ran int
	g, _ := riskyGate(func(context.Context, decision.Request) (decision.Verdict, error) { panic("free tool must not judge") })
	res, err := g.Wrap(counting(&ran))(context.Background(), namedDef("search"), tool.Args{"q": "x"})
	if err != nil || res.Text != "ran" || ran != 1 {
		t.Errorf("res=%v err=%v ran=%d", res, err, ran)
	}
}

func TestAllTruncatesLongArgs(t *testing.T) {
	var ran int
	var out bytes.Buffer
	g := &Gate{Mode: config.AuthAll, In: strings.NewReader("n\n"), Out: &out, MaxArgChars: 10}
	if _, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	preview, body, complete := strings.Cut(out.String(), "\nComplete validated action (JSON):\n")
	if !complete || !strings.Contains(preview, `cmd="0123456789…"`) || strings.Contains(preview, "abcdef") || !strings.Contains(body, "0123456789abcdef") {
		t.Errorf("prompt=%q", out.String())
	}
}

func TestRiskyUnlistedIsJudged(t *testing.T) {
	var ran int
	g, _ := riskyGate(stubJudge(decision.Verdict{Option: "deny", Confidence: 0.9}, nil))
	res, err := g.Wrap(counting(&ran))(context.Background(), namedDef("frobnicator"), tool.Args{"q": "x"})
	if err != nil {
		t.Fatalf("deny must not error: %v", err)
	}
	if !strings.Contains(res.Text, "declined by policy") || ran != 0 {
		t.Errorf("res=%v ran=%d", res, ran)
	}
}

func TestRiskyLogsJudgmentWithResolution(t *testing.T) {
	cases := []struct {
		name       string
		verdict    decision.Verdict
		answer     string
		resolution string
	}{
		{"allowed", decision.Verdict{Option: "allow", Confidence: 0.9}, "", "allowed"},
		{"denied", decision.Verdict{Option: "deny", Confidence: 0.9}, "", "denied"},
		{"asked-allowed", decision.Verdict{Option: "allow", Confidence: 0.5}, "y\n", "asked-allowed"},
		{"asked-denied", decision.Verdict{Option: "deny", Confidence: 0.5}, "n\n", "asked-denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ran int
			var kinds, texts []string
			g, _ := riskyGate(stubJudge(tc.verdict, nil))
			g.In = strings.NewReader(tc.answer)
			g.Log = func(kind, text string) { kinds, texts = append(kinds, kind), append(texts, text) }
			if _, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "ls"}); err != nil {
				t.Fatal(err)
			}
			if len(kinds) != 1 || kinds[0] != decision.KindJudgment {
				t.Fatalf("kinds=%v", kinds)
			}
			j, ok := decision.ParseJudgment(texts[0])
			if !ok {
				t.Fatalf("unparseable event: %q", texts[0])
			}
			if j.Source != decision.SourceRiskyAuth || j.Pick != tc.verdict.Option || j.Resolution != tc.resolution || j.Confidence != tc.verdict.Confidence {
				t.Errorf("parsed=%+v", j)
			}
		})
	}
}

func TestRiskyJudgeErrorLogsNothing(t *testing.T) {
	logged := 0
	g, _ := riskyGate(stubJudge(decision.Verdict{}, errTestDown))
	g.Log = func(string, string) { logged++ }
	if _, err := g.Wrap(counting(new(int)))(context.Background(), fakeDef(), tool.Args{"cmd": "ls"}); err != nil {
		t.Fatal(err)
	}
	if logged != 0 {
		t.Errorf("logged=%d, want 0 (no verdict, no event)", logged)
	}
}
