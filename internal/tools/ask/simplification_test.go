package ask

import (
	"context"
	"specific-harness/internal/tool"
	"testing"
)

func TestSimplificationAskRejectsDroppedOptions(t *testing.T) {
	called := false
	d := New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) { called = true; return nil, nil }})
	a, q := tool.ValidateJSON(d, `{"question":"Choose","options":"a,b,c,d"}`)
	if q != "" {
		t.Fatal(q)
	}
	if _, e := d.Execute(context.Background(), a); e == nil || called {
		t.Fatal("excess option silently discarded or question asked")
	}
	a["options"] = "a,b,c"
	if _, e := d.Execute(context.Background(), a); e != nil || !called {
		t.Fatal("valid options refused", e)
	}
}
