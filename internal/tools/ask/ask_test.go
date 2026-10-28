package ask

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"specific-harness/internal/tool"
)

func TestAskRoundTrip(t *testing.T) {
	var out bytes.Buffer
	p := &Prompter{In: strings.NewReader("blue\n"), Out: &out}
	d := New(p)
	args, repair := tool.Validate(d, map[string]string{"question": "favorite color?"})
	if repair != "" {
		t.Fatalf("unexpected repair: %s", repair)
	}
	res, err := d.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("ask failed: %v", err)
	}
	if res.Text != `[{"question":"favorite color?","picked":"blue","comment":""}]` {
		t.Errorf("answer=%q", res.Text)
	}
	if !strings.Contains(out.String(), "Q1/1: favorite color?") {
		t.Errorf("prompt=%q", out.String())
	}
}

func TestAskNeedsQuestion(t *testing.T) {
	p := &Prompter{In: strings.NewReader(""), Out: &bytes.Buffer{}}
	_, repair := tool.Validate(New(p), map[string]string{})
	if !strings.Contains(repair, "question") {
		t.Errorf("repair=%q", repair)
	}
}

func TestAskOptionsParse(t *testing.T) {
	var got []Question
	p := &Prompter{Ask: func(ctx context.Context, qs []Question) ([]Answer, error) {
		got = qs
		return []Answer{{Picked: "x"}, {Picked: "free"}}, nil
	}}
	d := New(p)
	_, err := d.Execute(context.Background(), tool.Args{
		"question": "a?", "options": "x\n y \nz",
		"question2": "b?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("questions=%+v", got)
	}
	want := []string{"x", "y", "z", NoneOfAbove}
	if strings.Join(got[0].Options, "|") != strings.Join(want, "|") {
		t.Errorf("options=%v", got[0].Options)
	}
	if len(got[1].Options) != 0 {
		t.Errorf("q2 options=%v", got[1].Options)
	}
}

func TestAskNaturalCommaIsNotAnOptionDelimiter(t *testing.T) {
	got := parseOptions("Industrial, restrained UI\r\nWarm, handcrafted cockpit\n Minimal, quiet")
	want := []string{"Industrial, restrained UI", "Warm, handcrafted cockpit", "Minimal, quiet", NoneOfAbove}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("natural labels split: %q", got)
	}
	if got := parseOptions("A single label, with a comma"); len(got) != 2 || got[0] != "A single label, with a comma" {
		t.Fatalf("single choice split: %q", got)
	}
}

func TestAskStdinOptions(t *testing.T) {
	var out bytes.Buffer
	p := &Prompter{In: strings.NewReader("2\nship it\n"), Out: &out}
	res, err := New(p).Execute(context.Background(), tool.Args{"question": "which?", "options": "slow\n fast"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != `[{"question":"which?","picked":"fast","comment":"ship it"}]` {
		t.Errorf("answer=%q", res.Text)
	}
	for _, want := range []string{"1) slow", "2) fast", "3) None of the above"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, out.String())
		}
	}
}

func TestAskFormat(t *testing.T) {
	if got, err := format(nil, nil); err != nil || got != "unanswered" {
		t.Errorf("nil=%q", got)
	}
	got, err := format([]Question{{Text: "first?"}, {Text: "second?"}}, []Answer{{Picked: "a", Comment: "why"}, {Picked: ""}})
	if err != nil || got != `[{"question":"first?","picked":"a","comment":"why"},{"question":"second?","picked":"","comment":""}]` {
		t.Errorf("format=%q", got)
	}
}
