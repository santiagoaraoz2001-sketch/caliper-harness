package tool

import (
	"strings"
	"testing"
)

func TestSimplificationAllIndependentValidationErrors(t *testing.T) {
	d := Definition{Name: "edit", Params: []Param{{Name: "path", Type: TypeString, Required: true}, {Name: "new", Type: TypeString, Required: true}, {Name: "count", Type: TypeInt}}}
	input := `{"zzz":true,"aaa":false,"count":"bad"}`
	var previous string
	for i := 0; i < 20; i++ {
		args, repair := ValidateJSON(d, input)
		for _, part := range []string{`argument "aaa"`, `argument "zzz"`, "edit needs path", "edit needs new", "edit needs a JSON int for count"} {
			if !strings.Contains(repair, part) {
				t.Fatalf("missing %s from %q", part, repair)
			}
		}
		if args != nil || repair == "" {
			t.Fatal("invalid call admitted")
		}
		if i > 0 && repair != previous {
			t.Fatal("nondeterministic repair")
		}
		previous = repair
	}
	if a, q := ValidateJSON(d, `{"path":"a","new":"b","count":2}`); q != "" || a["count"] != int64(2) {
		t.Fatal(q)
	}
	for _, s := range []string{`{"path":"a","path":"b"}`, `{"path":{"nested":true}}`} {
		if a, q := ValidateJSON(d, s); q == "" || a != nil {
			t.Fatal("ambiguous/non scalar admitted")
		}
	}
}
