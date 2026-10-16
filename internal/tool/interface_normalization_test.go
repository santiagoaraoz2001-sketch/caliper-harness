package tool

import (
	"strings"
	"testing"
)

func TestInterfaceExactScalarAndFieldCompatibility(t *testing.T) {
	d := Definition{Name: "bash", Params: []Param{{Name: "cmd", Type: TypeString, Required: true}, {Name: "timeout", Type: TypeInt}, {Name: "ratio", Type: TypeFloat}, {Name: "flag", Type: TypeBool}}}
	command := "  printf '%s' '%20 α🙂'\r\n\t  "
	for _, native := range []bool{false, true} {
		var args Args
		var repair string
		if native {
			args, repair = ValidateJSON(d, `{"command":"  printf '%s' '%20 α🙂'\r\n\t  ","TIMEOUT":"9007199254740993","ratio":"1.25","flag":"Yes"}`)
		} else {
			args, repair = Validate(d, map[string]string{"command": command, "TIMEOUT": "9007199254740993", "ratio": "1.25", "flag": "Yes"})
		}
		if repair != "" || args["cmd"] != command || args["timeout"] != int64(9007199254740993) || args["ratio"] != 1.25 || args["flag"] != true {
			t.Fatalf("native=%t args=%+v repair=%s", native, args, repair)
		}
	}
	for _, raw := range []string{`{"cmd":"x","timeout":"1.1"}`, `{"cmd":"x","timeout":"1e2"}`, `{"cmd":"x","timeout":"9223372036854775808"}`, `{"cmd":"x","ratio":"NaN"}`, `{"cmd":"x","flag":"maybe"}`, `{"cmd":"x","timeout":1.5}`, `{"cmd":"x","cmd":"y"}`, `{"cmd":"x"`} {
		if args, repair := ValidateJSON(d, raw); repair == "" || args != nil {
			t.Fatalf("ambiguous or inexact input admitted %s", raw)
		}
	}
	for _, raw := range []string{`{"cmd":"x","command":"y"}`, `{"cmd":"x","COMMAND":"x "}`} {
		if args, repair := ValidateJSON(d, raw); args != nil || !strings.Contains(strings.ToLower(repair), "conflict") {
			t.Fatalf("conflicting source bytes admitted: %s / %s", raw, repair)
		}
	}
	if args, repair := ValidateJSON(d, `{"cmd":"x","command":"x","timeout":2,"TIMEOUT":"2"}`); repair != "" || args["cmd"] != "x" || args["timeout"] != int64(2) || len(args) != 2 {
		t.Fatalf("equal aliases did not collapse once: %+v %s", args, repair)
	}
	unknown := Definition{Name: "probe", Params: []Param{{Name: "path", Type: TypeString}}}
	if _, repair := ValidateJSON(unknown, `{"file_path":"x"}`); repair == "" {
		t.Fatal("undeclared per-tool synonym acquired meaning")
	}
}

func TestInterfaceZeroArgumentCurrentCall(t *testing.T) {
	d := Definition{Name: "plan", Desc: "current question"}
	if args, repair := ValidateJSON(d, `{}`); repair != "" || args == nil || len(args) != 0 {
		t.Fatalf("valid empty call refused: %+v %s", args, repair)
	}
	for _, shown := range []string{d.Schema(), func() string { _, repair := ValidateJSON(d, `{"invoke":"BEGIN"}`); return repair }()} {
		if !strings.Contains(shown, "no arguments") || !strings.Contains(shown, "Call plan") || !strings.Contains(shown, "\n{}\n") || strings.Contains(shown, "slots:\n") || strings.Contains(shown, "Valid arguments: .") {
			t.Fatalf("zero-argument recovery is not explicit: %s", shown)
		}
	}
}
