package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/serve"
)

func TestServeLoadingLabelFollowsExactRowIdentity(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width, m.Height = 200, 60
	server := func(url string) ServeRow {
		return ServeRow{Ep: serve.Endpoint{Kind: serve.KindOllama, Label: "ollama", URL: url}, Health: serve.Health{Kind: serve.KindOllama, Up: true}}
	}
	model := func(url, name string) ServeRow {
		r := server(url)
		r.Ep.Model, r.IsModel = name, true
		return r
	}
	m.Serve.Rows = []ServeRow{
		server("http://fixture-owned/v1"), model("http://fixture-owned/v1", "qwen3:8b"), model("http://fixture-owned/v1", "other"),
		server("http://fixture-external/v1"), model("http://fixture-external/v1", "qwen3:8b"),
		{Ep: serve.Endpoint{Kind: serve.KindOpenAI, Label: "mlx", URL: "http://fixture-mlx/v1", Model: "local-model"}, IsModel: true, Asset: &serve.Asset{ID: "local-model", Reason: "metadata candidate"}},
	}
	keys := make([]string, len(m.Serve.Rows))
	for i, r := range m.Serve.Rows {
		keys[i] = serveKeyOf(r)
	}
	for _, owner := range []int{0, 1, 5} {
		t.Run(keys[owner], func(t *testing.T) {
			m.Serve.Sel, m.Serve.Loading = owner, keys[owner]
			lines, _ := m.serveContent(m.Width)
			body := ansi.Strip(strings.Join(lines, "\n"))
			if n := strings.Count(body, "[loading/releasing]"); n != 1 {
				t.Errorf("busy row count=%d, want exactly one\n%s", n, body)
			}
			if !strings.Contains(ansi.Strip(m.renderServe(m.Width, 50)), "loading/releasing — awaiting native receipt") {
				t.Error("global loading feedback lost")
			}
			for i, r := range m.Serve.Rows {
				if r.IsModel {
					got := strings.Contains(ansi.Strip(m.serveModelItem(r).Value), "[loading/releasing]")
					if got != (i == owner) {
						t.Errorf("model row %d busy=%t, owner=%d", i, got, owner)
					}
				} else {
					line := m.serveServerItem(r).Value
					if strings.Contains(line, "[loading/releasing]") != (i == owner) {
						t.Errorf("server row %d has wrong busy tag: %s", i, line)
					}
				}
				if serveKeyOf(r) != keys[i] || m.Serve.Sel != owner || m.Serve.Loading != keys[owner] {
					t.Error("render mutated selection or action identity")
				}
			}
		})
	}
}
