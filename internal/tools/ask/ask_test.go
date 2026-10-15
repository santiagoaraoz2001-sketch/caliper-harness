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
	if res.Text != "1: blue" {
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
		return []Answer{{Picked: "x"}}, nil
	}}
	d := New(p)
	_, err := d.Execute(context.Background(), tool.Args{
		"question": "a?", "options": "x, y ,z",
		"question2": "b?", "options3": "orphan",
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

func TestAskStdinOptions(t *testing.T) {
	var out bytes.Buffer
	p := &Prompter{In: strings.NewReader("2\nship it\n"), Out: &out}
	res, err := New(p).Execute(context.Background(), tool.Args{"question": "which?", "options": "slow, fast"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "1: fast — ship it" {
		t.Errorf("answer=%q", res.Text)
	}
	for _, want := range []string{"1) slow", "2) fast", "3) None of the above"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, out.String())
		}
	}
}

func TestAskFormat(t *testing.T) {
	if got := format(nil); got != "unanswered" {
		t.Errorf("nil=%q", got)
	}
	got := format([]Answer{{Picked: "a", Comment: "why"}, {Picked: ""}})
	if got != "1: a — why\n2: no answer" {
		t.Errorf("format=%q", got)
	}
}
