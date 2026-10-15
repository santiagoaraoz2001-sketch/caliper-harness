package tool

import (
	"strings"
	"testing"
)

func TestExtractRefusesMultipleCalls(t *testing.T) {
	block := "```tool\ntool: bash\ncmd: echo first\n```\n"
	for _, n := range []int{2, 9} {
		call, found, repair := Extract(strings.Repeat(block, n))
		if !found || !strings.Contains(repair, "one tool block") || call.Name != "" || len(call.Slots) != 0 {
			t.Errorf("%d blocks: call=%+v found=%v repair=%q", n, call, found, repair)
		}
	}
	_, _, repair := Extract(block + "```tool\ntool: bash\ncmd: echo second")
	if !strings.Contains(repair, "one tool block") {
		t.Errorf("unclosed second block repair=%q", repair)
	}
}

func TestExtractLiteralBytes(t *testing.T) {
	cases := []struct{ name, value, ending string }{
		{"indent comments assignments fences", "# keep this comment\n\nvalue = 2\nif value:\n    print('yes')  \nelse:\n\tprint('no')\n```tool\ntool: this is data\n```\nEND_OTHER\nEND \n\n", "\n"},
		{"CRLF", "# comment\r\n\r\n  key: value \t\r\nx=y\r\n", "\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := "```tool" + tc.ending + "tool: bash" + tc.ending + "cmd: <<END" + tc.ending + tc.value + "END" + tc.ending + "timeout: 3" + tc.ending + "```"
			call, found, repair := Extract(msg)
			if !found || repair != "" || call.Name != "bash" || call.Slots["cmd"] != tc.value || call.Slots["timeout"] != "3" {
				t.Fatalf("call=%+v found=%v repair=%q\nvalue=%q want=%q", call, found, repair, call.Slots["cmd"], tc.value)
			}
		})
	}
}

func TestExtractRefusesAmbiguousShapes(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"implicit continuation", "tool: bash\ncmd: echo one\necho two", "<<END"},
		{"stray before tool", "execute this\ntool: bash\ncmd: true", "tool: NAME"},
		{"duplicate tool", "tool: bash\ntool: bash\ncmd: true", "duplicate"},
		{"different duplicate tool", "tool: bash\ntool: find\ncmd: true", "duplicate"},
		{"empty duplicate tool", "tool: \ntool: bash\ncmd: true", "duplicate"},
		{"duplicate slot", "tool: bash\ncmd: first\ncmd: second", "duplicate"},
		{"case insensitive duplicate slot", "tool: bash\ncmd: first\nCMD = second", "duplicate"},
		{"bad delimiter", "tool: bash\ncmd: <<END extra\ntrue\nEND", "delimiter"},
		{"missing delimiter", "tool: bash\ncmd: <<END\ntrue", "END"},
		{"indented delimiter", "tool: bash\ncmd: <<END\ntrue\n END", "END"},
		{"YAML literal", "tool: bash\ncmd: |\n  true", "<<END"},
		{"literal tool name", "tool: <<END\nbash\nEND", "tool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call, found, repair := Extract("```tool\n" + tc.body + "\n```")
			if !found || !strings.Contains(repair, tc.want) || call.Name != "" || len(call.Slots) != 0 {
				t.Fatalf("call=%+v found=%v repair=%q", call, found, repair)
			}
		})
	}
}

func TestExtractScalarSeparatorsAndSchema(t *testing.T) {
	for _, msg := range []string{"```tool\ntool: bash\ncmd=echo a:b=c\n```", "```tool\nTOOL = bash\nCMD: echo a:b=c\n```"} {
		c, _, q := Extract(msg)
		if q != "" || c.Slots["cmd"] != "echo a:b=c" {
			t.Fatalf("call=%+v repair=%q", c, q)
		}
	}
	for _, scalar := range []string{"|", ">", "a:b=c"} {
		c, _, q := Extract("```tool\ntool: bash\ncmd: " + scalar + "\n```")
		if q != "" || c.Slots["cmd"] != scalar {
			t.Fatalf("scalar %q changed: call=%+v repair=%q", scalar, c, q)
		}
	}
	c, found, q := Extract("```tool\ntool: bash\n```")
	if !found || q != "" || c.Name != "bash" || len(c.Slots) != 0 {
		t.Fatalf("schema call=%+v found=%v repair=%q", c, found, q)
	}
	c, _, q = Extract("```tool\ntool: bash\nunknown: x\n```")
	if q != "" {
		t.Fatal(q)
	}
	if _, q = Validate(fixtureTool(), c.Slots); !strings.Contains(q, `unsupported argument "unknown"`) {
		t.Fatal(q)
	}
}

func TestExtractOuterCommentsCannotArmLiteral(t *testing.T) {
	for _, comment := range []string{"#cmd: <<END", "  # cmd = <<END", "#tool: <<END"} {
		for _, position := range []string{"before tool", "between fields", "after scalar"} {
			t.Run(position+" "+comment, func(t *testing.T) {
				body := "tool: bash\ncmd: printf OK\n"
				switch position {
				case "before tool":
					body = comment + "\n" + body
				case "between fields":
					body = "tool: bash\n" + comment + "\ncmd: printf OK\n"
				case "after scalar":
					body += comment + "\n"
				}
				c, found, repair := Extract("```tool\n" + body + "```")
				if !found || repair != "" || c.Name != "bash" || c.Slots["cmd"] != "printf OK" || len(c.Slots) != 1 {
					t.Fatalf("outer comment affected scalar call: found=%v repair=%q call=%+v", found, repair, c)
				}
			})
		}
	}
}

func TestExtractLiteralKeepsCommentDeclarationBytes(t *testing.T) {
	value := "#cmd: <<INNER\n#tool: <<OTHER\n  # key = <<LABEL  \n\n"
	c, found, repair := Extract("```tool\ntool: bash\ncmd: <<END\n" + value + "END\n```")
	if !found || repair != "" || c.Slots["cmd"] != value {
		t.Fatalf("literal comment bytes changed: found=%v repair=%q got=%q want=%q", found, repair, c.Slots["cmd"], value)
	}
}
