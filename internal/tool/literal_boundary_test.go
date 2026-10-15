package tool

import "testing"

func TestLiteralBoundarySharedRules(t *testing.T) {
	for _, tc := range []struct {
		line, label string
		marked      bool
	}{
		{"cmd: <<END", "END", true}, {"CMD = <<END_2\r", "END_2", true},
		{"cmd: <<BAD LABEL", "", true}, {"cmd: <<", "", true},
		{"#cmd: <<END", "", false}, {"  # cmd = <<END", "", false}, {"\r", "", false},
		{"cmd: echo a:b=c", "", false}, {"not a slot: <<END", "", false},
	} {
		t.Run(tc.line, func(t *testing.T) {
			label, marked := LiteralStart(tc.line)
			if label != tc.label || marked != tc.marked {
				t.Errorf("label=%q marked=%v want=%q %v", label, marked, tc.label, tc.marked)
			}
		})
	}
	for _, tc := range []struct {
		line, label string
		end         bool
	}{
		{"END", "END", true}, {"END\r", "END", true}, {" END", "END", false},
		{"END ", "END", false}, {"END_OTHER", "END", false}, {"", "", false}, {"```", "END", false},
	} {
		if got := LiteralEnd(tc.line, tc.label); got != tc.end {
			t.Errorf("end(%q,%q)=%v want=%v", tc.line, tc.label, got, tc.end)
		}
	}
}
