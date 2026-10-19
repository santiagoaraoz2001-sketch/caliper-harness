package slash

import (
	"strings"
	"testing"
)

func TestRegistryHas31Commands(t *testing.T) {
	if got := len(Commands()); got != 32 {
		t.Fatalf("registry holds %d commands, want 32", got)
	}
	seen := map[string]bool{}
	for _, c := range Commands() {
		if c.Name == "" || c.Desc == "" || c.Usage == "" {
			t.Errorf("command missing name/desc/usage: %+v", c)
		}
		if seen[c.Name] {
			t.Errorf("duplicate command %q", c.Name)
		}
		seen[c.Name] = true
	}
}

func TestParse(t *testing.T) {
	r := New()
	c, args, ok := r.Parse("/sessions abc123")
	if !ok || c.Name != "sessions" || len(args) != 1 || args[0] != "abc123" {
		t.Errorf("parse sessions: %+v %v %v", c, args, ok)
	}
	c, args, ok = r.Parse("/btw watch the loop")
	if !ok || c.Name != "btw" || len(args) != 3 {
		t.Errorf("parse btw: %+v %v %v", c, args, ok)
	}
	if _, _, ok := r.Parse("/Sessions"); ok {
		t.Error("names are case-sensitive: /Sessions must not parse")
	}
	for _, in := range []string{"", "chat", "/bogus", "/"} {
		if _, _, ok := r.Parse(in); ok {
			t.Errorf("Parse(%q) must fail", in)
		}
	}
}

func TestComplete(t *testing.T) {
	r := New()
	if got := r.Complete("/", Context{}); len(got) != 32 {
		t.Errorf("/ completes to %d, want 32", len(got))
	}
	got := r.Complete("/p", Context{})
	if len(got) != 1 || got[0] != "/plans" {
		t.Errorf("/p completes to %v, want [/plans]", got)
	}
	if got := r.Complete("/zzz", Context{}); len(got) != 0 {
		t.Errorf("/zzz completes to %v, want nothing", got)
	}
	if got := r.Complete("hello", Context{}); len(got) != 0 {
		t.Errorf("plain text completes to %v, want nothing", got)
	}
}

func TestCompleteCommentUsesContext(t *testing.T) {
	r := New()
	ctx := Context{View: "plans", RefPrefix: "approachX-T1"}
	if got := r.Complete("/comment ", ctx); len(got) != 1 || got[0] != "approachX-T1" {
		t.Errorf("context ref completes to %v", got)
	}
	if got := r.Complete("/comment approachX-", ctx); len(got) != 1 {
		t.Errorf("partial ref completes to %v", got)
	}
	if got := r.Complete("/comment ", Context{}); len(got) != 0 {
		t.Errorf("empty context completes to %v, want nothing", got)
	}
	if got := r.Complete("/recall foo", Context{}); len(got) != 0 {
		t.Errorf("non-comment args complete to %v, want nothing", got)
	}
}

func TestCompleteArgs(t *testing.T) {
	r := New()
	if got := r.Complete("/auth ", Context{}); len(got) != 3 {
		t.Errorf("/auth completes to %v", got)
	}
	if got := r.Complete("/auth r", Context{}); len(got) != 1 || got[0] != "risky" {
		t.Errorf("/auth r completes to %v", got)
	}
	if got := r.Complete("/auth yolo", Context{}); got != nil {
		t.Errorf("complete arg must send, got %v", got)
	}
	if got := r.Complete("/auth yolo x", Context{}); got != nil {
		t.Errorf("extra tokens must send, got %v", got)
	}
}

func TestHelpListsAll(t *testing.T) {
	h := New().Help()
	for _, c := range Commands() {
		if !strings.Contains(h, "/"+c.Name) {
			t.Errorf("help omits /%s", c.Name)
		}
	}
}

func TestModelsRawArgumentAndLiveView(t *testing.T) {
	r := New()
	for _, raw := range []string{"/absolute/name with  spaces  ", "installed:exact"} {
		c, args, ok := r.Parse("/models " + raw)
		if !ok || c.View != "manage" || c.Lands != "" || len(args) != 1 || args[0] != raw {
			t.Fatalf("%+v %q", c, args)
		}
	}
}
