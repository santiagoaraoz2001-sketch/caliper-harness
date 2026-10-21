package tui

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConnectionRemotePresetKeepsExternalOwnership(t *testing.T) {
	m := formModel(t)
	f := m.Setup.Add
	f.Mode = "server"
	f.Field = 1
	f.Values[1].SetValue("mlx")
	f.Values[2].SetValue("http://server.example:11434")
	m = viewportKey(m, tea.KeyRight)
	if f.Values[7].Value() != "" {
		t.Fatal("preset silently selected a local launcher")
	}
	if _, err := f.candidate(m.Cfg); err != nil {
		t.Fatal(err)
	}
	f.Values[7].SetValue("/custom/launcher")
	m = viewportKey(m, tea.KeyLeft)
	if f.Values[7].Value() != "/custom/launcher" {
		t.Fatal("explicit launcher lost")
	}
}

func TestConnectionLauncherIsAdvanced(t *testing.T) {
	m := formModel(t)
	f := m.Setup.Add
	f.Mode = "server"
	for _, field := range f.visibleFields() {
		if field == 7 {
			t.Fatal("ordinary form exposes local launcher")
		}
	}
	m = viewportKey(m, tea.KeyF2)
	found := false
	for _, field := range f.visibleFields() {
		found = found || field == 7
	}
	if !found {
		t.Fatal("advanced form lost local launcher")
	}
}

func TestConnectionCatalogBeforeModelIDAndNoSelectionEffects(t *testing.T) {
	requests := 0
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = nativeHealthRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"first-model"},{"id":"second-model"}]}`)), Header: make(http.Header)}, nil
	})
	m := formModel(t)
	f := m.Setup.Add
	f.Values[2].SetValue("http://catalog.fixture/v1")
	before := m.Cfg
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("empty model prevented catalog lookup: %s", f.Err)
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if requests != 1 || !strings.Contains(f.Receipt, "2") {
		t.Fatalf("catalog receipt: %s requests=%d", f.Receipt, requests)
	}
	f.Field = 3
	m = viewportKey(m, tea.KeyRight)
	if f.Values[3].Value() != "first-model" {
		t.Fatalf("catalog not selectable: %s", f.Values[3].Value())
	}
	m = viewportKey(m, tea.KeyRight)
	if f.Values[3].Value() != "second-model" {
		t.Fatal("second catalog model unavailable")
	}
	if !reflect.DeepEqual(before, m.Cfg) || len(m.Serve.Spawned) != 0 {
		t.Fatal("catalog selected a live role or started a process")
	}
	f.Field = 2
	m = viewportKey(m, tea.KeyEnd)
	m = key(m, "/other")
	f.Field = 3
	f.Values[3].SetValue("")
	m = viewportKey(m, tea.KeyRight)
	if f.Values[3].Value() != "" {
		t.Fatal("old endpoint catalog remained selectable")
	}
}

func TestConnectionCatalogRejectsStaleResponses(t *testing.T) {
	for _, change := range []string{"url", "preset", "newer-probe", "closed-form"} {
		t.Run(change, func(t *testing.T) {
			m := formModel(t)
			f := m.Setup.Add
			f.ProbeSeq = 1
			msg := connectionTestMsg{Form: f, Receipt: "old catalog", Catalog: []string{"old-model"}, URL: f.Values[2].Value(), Preset: f.Values[1].Value(), Seq: 1}
			switch change {
			case "url":
				f.Values[2].SetValue("http://different/v1")
			case "preset":
				f.Values[1].SetValue("ollama")
			case "newer-probe":
				f.ProbeSeq++
			case "closed-form":
				m.Setup.Add = nil
			}
			m.Update(msg)
			if len(f.Catalog) != 0 || f.Receipt != "" {
				t.Fatal("stale response applied")
			}
		})
	}
}

func TestConnectionCatalogIncludesResidentOllamaModels(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	requests := []string{}
	http.DefaultTransport = nativeHealthRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		body := `{"models":[{"name":"resident"}]}`
		if r.URL.Path == "/api/tags" {
			body = `{"models":[{"name":"resident"},{"name":"installed"}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	m := formModel(t)
	f := m.Setup.Add
	f.Values[1].SetValue("ollama")
	f.Values[2].SetValue("http://catalog.fixture:11434")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal(f.Err)
	}
	m.Update(cmd())
	if !reflect.DeepEqual(requests, []string{"GET /api/ps", "GET /api/tags"}) || !reflect.DeepEqual(f.Catalog, []string{"resident", "installed"}) {
		t.Fatalf("catalog=%v requests=%v", f.Catalog, requests)
	}
}
