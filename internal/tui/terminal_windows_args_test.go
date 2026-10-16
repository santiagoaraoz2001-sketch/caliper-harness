package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestWindowsTerminalArgumentsRemainRaw(t *testing.T) {
	t.Setenv("WT_SESSION", "fixture")
	argv := []string{`C:\Program Files\Caliper\caliper.exe`, "tui", "--resume", "sesión ü", `literal"quote`, `C:\trailing\`, ""}
	before := append([]string(nil), argv...)
	bin, args, err := windowsTerminalCommand(argv)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{"-w", "0", "nt", "-d", cwd(), "--"}, argv...)
	if bin != "wt.exe" || !reflect.DeepEqual(args, want) || !reflect.DeepEqual(argv, before) {
		t.Fatalf("%s %#v %v", bin, args, err)
	}
}
func TestWindowsTerminalRefusesCommandSeparators(t *testing.T) {
	t.Setenv("WT_SESSION", "fixture")
	for _, arg := range []string{";", "session;split-pane", "line\nnext", "bad\x00arg"} {
		if _, _, err := windowsTerminalCommand([]string{"caliper.exe", arg}); err == nil {
			t.Fatalf("accepted %q", arg)
		}
	}
}
func TestWindowsTerminalRequiresSessionAndExecutable(t *testing.T) {
	t.Setenv("WT_SESSION", "")
	if _, _, err := windowsTerminalCommand([]string{"caliper.exe"}); err == nil || !strings.Contains(err.Error(), "resume with:") {
		t.Fatal(err)
	}
	t.Setenv("WT_SESSION", "fixture")
	if _, _, err := windowsTerminalCommand(nil); err == nil {
		t.Fatal("empty argv admitted")
	}
}
