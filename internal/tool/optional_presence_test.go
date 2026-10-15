package tool

import (
	"reflect"
	"testing"
)

func TestOptionalInterfacePresenceAndDefaults(t *testing.T) {
	d := Definition{Name: "fixture", Params: []Param{{Name: "range", Type: TypeInt}, {Name: "text", Type: TypeString, AllowEmpty: true}, {Name: "limit", Type: TypeInt, Default: "7"}}}
	for _, native := range []bool{false, true} {
		var a Args
		var q string
		if native {
			a, q = ValidateJSON(d, `{}`)
		} else {
			a, q = Validate(d, map[string]string{})
		}
		if q != "" || !reflect.DeepEqual(a, Args{"limit": int64(7)}) {
			t.Fatalf("omitted=%v %s", a, q)
		}
		for _, raw := range []string{`{"range":0,"text":""}`, `{"range":-1,"text":""}`} {
			a, q = ValidateJSON(d, raw)
			if q != "" || a["range"] == nil || a["text"] != "" || a["limit"] != int64(7) {
				t.Fatalf("presence %v %s", a, q)
			}
		}
		a, q = Validate(d, map[string]string{"range": "0", "text": ""})
		if q != "" || a["range"] != int64(0) || a["text"] != "" {
			t.Fatal("text presence", a, q)
		}
	}
	for _, raw := range []string{`{"range":"1.1"}`, `{"range":1.5}`, `{"range":9223372036854775808}`} {
		if _, q := ValidateJSON(d, raw); q == "" {
			t.Fatal("wrong number accepted")
		}
	}
	r := NewRegistry()
	r.Register(d)
	defs, e := r.NativeDefinitions()
	if e != nil {
		t.Fatal(e)
	}
	p := defs[0].Function.Parameters["properties"].(map[string]any)
	if _, ok := p["range"].(map[string]any)["default"]; ok {
		t.Fatal("invented default")
	}
	if p["limit"].(map[string]any)["default"] != int64(7) {
		t.Fatal("declared default lost")
	}
}
