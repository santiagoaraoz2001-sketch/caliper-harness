package tool

import (
	"testing"
)

func TestNativeJSONPrecisionAndStringBytes(t *testing.T) {
	d := Definition{Name: "probe", Params: []Param{{Name: "n", Type: TypeInt, Required: true}, {Name: "s", Type: TypeString, Required: true}, {Name: "optional", Type: TypeString, Default: "default"}}}
	a, q := ValidateJSON(d, `{"n":9007199254740993,"s":"  a\r\nb\t  ","optional":""}`)
	if q != "" || a["n"] != int64(9007199254740993) || a["s"] != "  a\r\nb\t  " || a["optional"] != "" {
		t.Fatalf("args=%+v %s", a, q)
	}
	for _, raw := range []string{`{"n":1,"n":2,"s":"x"}`, `{"n":1,"\u006e":2,"s":"x"}`, `{"n":"1.1","s":"x"}`, `{"n":1.1,"s":"x"}`, `{"n":9223372036854775808,"s":"x"}`, `{"n":1,"s":null}`, `{"n":1,"s":{},"unknown":true}`, `{"n":1,"s":"x"} {}`, `{}`} {
		if _, q := ValidateJSON(d, raw); q == "" {
			t.Fatalf("invalid admitted %s", raw)
		}
	}
}
func TestNativeDefinitionsRefuseInternalSchemaOverwrite(t *testing.T) {
	for _, params := range [][]Param{{{Name: "", Type: TypeString}}, {{Name: "x", Type: TypeString}, {Name: "x", Type: TypeString}}, {{Name: "n", Type: TypeInt, Default: "bad"}}} {
		r := NewRegistry()
		r.Register(Definition{Name: "probe", Params: params})
		if _, e := r.NativeDefinitions(); e == nil {
			t.Fatalf("invalid schema admitted %+v", params)
		}
	}
}
