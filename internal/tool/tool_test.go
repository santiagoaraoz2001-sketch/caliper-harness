package tool

import (
	"strings"
	"testing"
)

func fixtureTool() Definition {
	return Definition{
		Name: "bash", Label: "bash", Desc: "run a shell command",
		Purpose: "Runs one shell command and returns output.",
		Params: []Param{
			{Name: "cmd", Desc: "the command", Type: TypeString, Required: true},
			{Name: "timeout", Desc: "seconds", Type: TypeInt, Default: "10"},
			{Name: "shell", Desc: "which shell", Type: TypeString, Enum: []string{"sh", "bash"}, Default: "bash"},
		},
	}
}

func TestExtract(t *testing.T) {
	msg := "Sure thing.\n```tool\ntool: bash\ncmd: ls -la\ntimeout = 5\n```\nDone?"
	call, found, repair := Extract(msg)
	if !found || repair != "" {
		t.Fatalf("found=%v repair=%q", found, repair)
	}
	if call.Name != "bash" || call.Slots["cmd"] != "ls -la" || call.Slots["timeout"] != "5" {
		t.Errorf("call=%+v", call)
	}
}

func TestExtractChatterAroundBlock(t *testing.T) {
	_, found, _ := Extract("no block here, just chat")
	if found {
		t.Error("plain chat must not extract a call")
	}
}

func TestExtractUnclosed(t *testing.T) {
	_, found, repair := Extract("```tool\ntool: bash\ncmd: ls")
	if !found || !strings.Contains(repair, "never closed") {
		t.Errorf("found=%v repair=%q", found, repair)
	}
}

func TestExtractMissingToolLine(t *testing.T) {
	_, found, repair := Extract("```tool\ncmd: ls\n```")
	if !found || !strings.Contains(repair, "which tool") {
		t.Errorf("found=%v repair=%q", found, repair)
	}
}

func TestParseMultilineAndComments(t *testing.T) {
	call, _, repair := Extract("```tool\n# outer comment\ntool: bash\ncmd: <<END\necho one\necho two\n# a comment\nEND\ntimeout: 3\n```")
	if repair != "" {
		t.Fatal(repair)
	}
	if call.Slots["cmd"] != "echo one\necho two\n# a comment\n" {
		t.Errorf("multiline cmd=%q", call.Slots["cmd"])
	}
	if call.Slots["timeout"] != "3" {
		t.Errorf("timeout=%q", call.Slots["timeout"])
	}
}

func TestParseCaseInsensitiveKeys(t *testing.T) {
	call, _, _ := Extract("```tool\nTOOL: Bash\nCMD: ls\n```")
	if call.Name != "Bash" || call.Slots["cmd"] != "ls" {
		t.Errorf("call=%+v (name keeps case, keys lowercased)", call)
	}
}

func TestValidateHappy(t *testing.T) {
	args, repair := Validate(fixtureTool(), map[string]string{"cmd": "ls"})
	if repair != "" {
		t.Fatalf("repair=%q", repair)
	}
	if args["cmd"] != "ls" || args["timeout"] != int64(10) || args["shell"] != "bash" {
		t.Errorf("defaults/coercion wrong: %+v", args)
	}
}

func TestValidateGapsAskOneTargetedQuestion(t *testing.T) {
	d := fixtureTool()
	cases := []struct {
		name  string
		slots map[string]string
		want  []string
	}{
		{"missing required", map[string]string{}, []string{"bash needs `cmd`", "what should it be"}},
		{"bad int", map[string]string{"cmd": "ls", "timeout": "soon"}, []string{"timeout", "whole number", `"soon"`}},
		{"unknown slot", map[string]string{"cmd": "ls", "cmdd": "x"}, []string{`unsupported argument "cmdd"`, "cmd, timeout, shell"}},
		{"bad enum", map[string]string{"cmd": "ls", "shell": "zsh"}, []string{"one of", "zsh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, repair := Validate(d, tc.slots)
			if repair == "" {
				t.Fatal("expected a repair question, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(repair, w) {
					t.Errorf("repair %q missing %q", repair, w)
				}
			}
			if !strings.Contains(repair, `"name": "bash"`) {
				t.Errorf("repair must show the current native call: %q", repair)
			}
		})
	}
}

func TestValidateBoolVariants(t *testing.T) {
	d := Definition{Name: "t", Params: []Param{{Name: "flag", Type: TypeBool, Required: true}}}
	for _, s := range []string{"true", "yes", "Y", "1"} {
		args, repair := Validate(d, map[string]string{"flag": s})
		if repair != "" || args["flag"] != true {
			t.Errorf("%q: repair=%q args=%v", s, repair, args)
		}
	}
	if _, repair := Validate(d, map[string]string{"flag": "maybe"}); !strings.Contains(repair, "true or false") {
		t.Errorf("repair=%q", repair)
	}
}

func TestRegistryAndSchema(t *testing.T) {
	r := NewRegistry()
	r.Register(fixtureTool())
	d, ok := r.Lookup("BASH")
	if !ok {
		t.Fatal("lookup must be case-insensitive")
	}
	s := d.Schema()
	for _, w := range []string{"bash", "run a shell command", "cmd", "required", "timeout", "default: 10", "one of: sh, bash"} {
		if !strings.Contains(s, w) {
			t.Errorf("schema missing %q:\n%s", w, s)
		}
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Error("unknown tool must not be found")
	}
}
