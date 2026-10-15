package tui

import (
	"strings"
	"testing"
)

func TestShortAuth(t *testing.T) {
	for mode, want := range map[string]string{"yolo": "YOLO", "authorize-risky": "RISKY", "authorize-all": "ALL", "zzz": "ALL"} {
		if got := ShortAuth(mode); got != want {
			t.Errorf("%s → %q, want %q", mode, got, want)
		}
	}
}

func TestKfmt(t *testing.T) {
	for n, want := range map[int]string{0: "0", 950: "950", 1024: "1k", 12400: "12.1k", 65536: "64k"} {
		if got := kfmt(n); got != want {
			t.Errorf("%d → %q, want %q", n, got, want)
		}
	}
}

func TestCtxBar(t *testing.T) {
	f, e := ctxBar(0, 100)
	if f != "" || e != "░░░░░░░░" {
		t.Errorf("empty=%q%q", f, e)
	}
	f, e = ctxBar(50, 100)
	if f != "▓▓▓▓" || e != "░░░░" {
		t.Errorf("half=%q%q", f, e)
	}
	f, e = ctxBar(200, 100)
	if f != "▓▓▓▓▓▓▓▓" || e != "" {
		t.Errorf("over=%q%q", f, e)
	}
	f, e = ctxBar(10, 0)
	if f != "" || e != "░░░░░░░░" {
		t.Errorf("nocap=%q%q", f, e)
	}
}

func TestStatusRender(t *testing.T) {
	s := Status{Auth: "RISKY", CtxUsed: 12400, CtxKnown: true, CtxWindow: 65536, Think: true,
		Model: "qwen3:8b", Branch: "main", Dir: "harness",
		PlanTitle: "loginbug", PlanApproaches: []string{"poems2 2/2"}}
	got := s.Render()
	for _, want := range []string{"RISKY", "12.1k/64k", "▓", "THINK:", "ON", "qwen3:8b",
		"main", "harness", "loginbug", "poems2 2/2", "esc back", "/help"} {
		if !strings.Contains(got, want) {
			t.Errorf("footer omits %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"shift+↑↓", "shift+←→", "tab accept", "ctrl+t"} {
		if strings.Contains(got, gone) {
			t.Errorf("footer keeps tactical hint %q:\n%s", gone, got)
		}
	}
	if lines := strings.Count(got, "\n"); lines != 2 {
		t.Errorf("footer has %d breaks, want 2", lines)
	}
}

func TestStatusRenderEmpty(t *testing.T) {
	got := Status{Auth: "ALL"}.Render()
	for _, want := range []string{"ALL", "unknown/—", "OFF", "—", "no git", "no plan"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty footer omits %q:\n%s", want, got)
		}
	}
}
