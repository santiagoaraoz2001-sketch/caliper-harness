package ask

import (
	"context"
	"errors"
	"testing"

	"specific-harness/internal/tool"
)

func TestAskTransport(t *testing.T) {
	d := New(&Prompter{Ask: func(ctx context.Context, qs []Question) ([]Answer, error) { return []Answer{{Picked: " 42 "}}, nil }})
	res, err := d.Execute(context.Background(), tool.Args{"question": "q"})
	if err != nil || res.Text != `[{"question":"q","picked":" 42 ","comment":""}]` {
		t.Errorf("res=%+v err=%v", res, err)
	}
	d2 := New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) { return nil, errors.New("nope") }})
	if _, err := d2.Execute(context.Background(), tool.Args{"question": "q"}); err == nil {
		t.Error("ask error must surface")
	}
	d3 := New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) { return nil, nil }})
	res, err = d3.Execute(context.Background(), tool.Args{"question": "q"})
	if err != nil || res.Text != "unanswered" {
		t.Errorf("cancel=%+v err=%v", res, err)
	}
}
