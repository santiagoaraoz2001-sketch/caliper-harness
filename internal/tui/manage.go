package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/serve"
	"strings"
	"time"
)

// All connection writes use the existing machine/project config owners.
func (m *Model) saveManaged(candidate config.Config) error {
	if m.setupBusy() {
		return fmt.Errorf("wait for active work or staged decision changes before saving connections")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	if m.CfgPath != "" {
		if err := config.SaveEffective(m.CfgPath, candidate); err != nil {
			return err
		}
	}
	if err := config.SaveConnections(candidate); err != nil {
		if m.CfgPath != "" {
			return fmt.Errorf("project saved; reusable connections failed: %w", err)
		}
		return err
	}
	m.Cfg = candidate
	m.syncSetupAfterSave()
	m.syncDerived()
	return nil
}
func (m *Model) openManagedForm(mode string) {
	if m.setupBusy() {
		m.alert("info", "wait for active work before editing connections")
		return
	}
	m.openConnectionForm()
	f := m.Setup.Add
	f.Mode = mode
	f.Values[0].SetValue("my-server")
	f.Values[1].SetValue("custom")
	r := m.serveCur()
	if mode == "server" {
		return
	}
	if r == nil {
		f.Mode = "server"
		f.Err = "Add a server first, then + Add model"
		return
	}
	f.URLPinned = true
	f.Values[0].SetValue(r.Ep.Label)
	f.Values[2].SetValue(r.Ep.URL)
	runtime := "custom"
	for _, rec := range m.Cfg.Serve.Recipes {
		if sameServeURL(rec.URL, r.Ep.URL) {
			f.Values[7].SetValue(rec.Launcher)
			if rec.Launcher == "preset" {
				f.Values[5].SetValue("")
			}
		}
	}
	if r.Recipe != nil {
		if r.Recipe.Runtime != "" {
			runtime = r.Recipe.Runtime
		}
		if len(r.Recipe.Argv) > 0 {
			raw, _ := jsonMarshal(r.Recipe.Argv)
			f.Values[5].SetValue(raw)
		}
		f.Values[6].SetValue(r.Recipe.Dir)
	}
	if r.Ep.Kind == serve.KindOllama {
		runtime = "ollama"
	} else if r.Ep.Kind == serve.KindRulith {
		runtime = "rulith"
	}
	for _, rec := range m.Cfg.Serve.Recipes {
		if sameServeURL(rec.URL, r.Ep.URL) && rec.Launcher == "preset" {
			f.Values[5].SetValue("")
		}
	}
	if r.Sidecar {
		if !decision.NativeRuntime(runtime) {
			runtime = m.Cfg.Decision.Runtime
		}
	}
	f.Values[1].SetValue(runtime)
	if mode == "edit" {
		f.OriginalName, f.OriginalURL = r.Ep.Label, r.Ep.URL
		if r.IsModel {
			f.OriginalModel = r.Ep.Model
			f.Field = 3
			f.Values[3].SetValue(r.Ep.Model)
			if r.Asset != nil {
				f.Values[4].SetValue(r.Asset.Path)
				f.OriginalPath = r.Asset.Path
			}
		}
	}
	if mode == "model" {
		f.Field = 3
	}
}
func jsonMarshal(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }
func (m *Model) removeManaged() {
	r := m.serveCur()
	if r == nil {
		return
	}
	c := m.Cfg
	if r.IsModel {
		c.Serve.Models = nil
		for _, model := range m.Cfg.Serve.Models {
			if sameServeURL(model.Server, r.Ep.URL) && model.Name == r.Ep.Model {
				continue
			}
			c.Serve.Models = append(c.Serve.Models, model)
		}
		if sameServeURL(c.Model.Server, r.Ep.URL) && (c.Model.Name == r.Ep.Model || r.Asset != nil && c.Model.Name == r.Asset.Path) {
			c.Model.Name = ""
		}
		if sameServeURL(c.Decision.Server, r.Ep.URL) && (c.Decision.Model == r.Ep.Model || r.Asset != nil && (c.Decision.Path == r.Asset.Path || c.Decision.Model == r.Asset.Path)) {
			c.Decision.Disabled = true
		}
	} else {
		c.Serve.Recipes = nil
		for _, rec := range m.Cfg.Serve.Recipes {
			if sameServeURL(rec.URL, r.Ep.URL) {
				continue
			}
			c.Serve.Recipes = append(c.Serve.Recipes, rec)
		}
		c.Serve.Models = nil
		for _, model := range m.Cfg.Serve.Models {
			if !sameServeURL(model.Server, r.Ep.URL) {
				c.Serve.Models = append(c.Serve.Models, model)
			}
		}
		if sameServeURL(c.Model.Server, r.Ep.URL) {
			c.Model.Name = ""
		}
		if sameServeURL(c.Decision.Server, r.Ep.URL) {
			c.Decision.Disabled = true
		}
	}
	if err := m.saveManaged(c); err != nil {
		m.alert("err", err.Error())
		return
	}
	m.alert("info", "Saved connection removed · owned running processes stay available until explicitly stopped")
}
func (m *Model) testManaged() tea.Cmd {
	r := m.serveCur()
	if r == nil {
		return nil
	}
	endpoint := r.Ep
	client := &http.Client{Timeout: time.Duration(m.Cfg.Serve.ProbeTimeoutSeconds) * time.Second}
	return func() tea.Msg { h := serve.Probe(client, endpoint); return managedTestMsg{endpoint, h} }
}

type managedTestMsg struct {
	Endpoint serve.Endpoint
	Health   serve.Health
}

func (m *Model) onManagedTest(msg managedTestMsg) {
	for i := range m.Serve.Rows {
		if sameServeURL(m.Serve.Rows[i].Ep.URL, msg.Endpoint.URL) {
			m.Serve.Rows[i].Health = msg.Health
			m.Serve.Rows[i].Have = true
		}
	}
	if msg.Health.Up {
		var server *ServeRow
		for i := range m.Serve.Rows {
			if !m.Serve.Rows[i].IsModel && sameServeURL(m.Serve.Rows[i].Ep.URL, msg.Endpoint.URL) {
				copy := m.Serve.Rows[i]
				server = &copy
				break
			}
		}
		if server != nil {
			for _, name := range append(append([]string(nil), msg.Health.Loaded...), msg.Health.Available...) {
				found := false
				for _, r := range m.Serve.Rows {
					found = found || r.IsModel && sameServeURL(r.Ep.URL, msg.Endpoint.URL) && r.Ep.Model == name
				}
				if !found {
					row := *server
					row.Ep.Model = name
					row.IsModel = true
					for _, loaded := range msg.Health.Loaded {
						row.Resident = row.Resident || loaded == name && msg.Health.Kind == serve.KindOllama
					}
					m.Serve.Rows = append(m.Serve.Rows, row)
				}
			}
			m.onServeProbed(serveProbedMsg{rows: groupServeRows(m.Serve.Rows), mem: m.Serve.Mem, memErr: m.Serve.MemErr, warnings: m.Serve.Warnings})
		}
		m.alert("info", "Server responds · catalog: "+strings.Join(msg.Health.Available, ", ")+" · inference unchecked")
	} else {
		m.alert("err", "Connection failed: "+msg.Health.Err)
	}
}
