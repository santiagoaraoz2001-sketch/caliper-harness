package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
	"specific-harness/internal/tui/thirdparty/textarea"
	"strings"
	"time"
)

// The ordinary path requires an endpoint and model. Launch argv is an optional
// advanced setting for servers whose installation Caliper does not recognize.
type connectionForm struct {
	Field        int
	Values       [7]textarea.Model
	Err, Receipt string
}

var connectionLabels = []string{"Name", "Server type (openai, mlx, ollama, gufo, llama.cpp, rulith, laya, system_one)", "Server URL", "Model ID", "Checkpoint folder or GGUF file (optional)", "Launch argv as JSON array (optional, advanced)", "Launch directory (optional)"}

type connectionTestMsg struct {
	Form    *connectionForm
	Receipt string
}

func (m *Model) openConnectionForm() {
	f := &connectionForm{}
	values := []string{"my-server", "openai", "http://127.0.0.1:8080/v1", "", "", "", ""}
	if m.Setup.Stage == 1 {
		values[0] = "my-judge"
	}
	for i := range f.Values {
		f.Values[i] = newComposer()
		f.Values[i].SetHeight(1)
		f.Values[i].SetWidth(max(1, m.Width-8))
		f.Values[i].SetValue(values[i])
	}
	m.Setup.Add = f
}
func (f *connectionForm) candidate(base config.Config) (config.Config, error) {
	vals := make([]string, len(f.Values))
	for i := range vals {
		vals[i] = strings.TrimSpace(f.Values[i].Value())
	}
	modelFromPath := vals[3] == "" && vals[4] != ""
	if vals[3] == "" && vals[4] != "" {
		vals[3] = filepath.Base(vals[4])
	}
	if vals[0] == "" || vals[2] == "" || vals[3] == "" {
		return base, fmt.Errorf("Name, server URL and model ID are required")
	}
	runtime := strings.ToLower(vals[1])
	brand := runtime
	if runtime == "gufo" || runtime == "llama.cpp" || runtime == "llama-server" {
		runtime = "openai"
	}
	if brand == "llama.cpp" {
		brand = "llama-server"
	}
	kind := serve.KindOpenAI
	switch runtime {
	case "openai", "mlx":
	case "ollama":
		kind = serve.KindOllama
	case "rulith":
		kind = serve.KindRulith
	case "laya", "system_one":
		kind = serve.KindSidecar
	default:
		return base, fmt.Errorf("choose a listed runtime; server brand names use openai when compatible")
	}
	u, err := url.Parse(vals[2])
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return base, fmt.Errorf("server URL must be http:// or https:// with a host")
	}
	var argv []string
	if vals[5] != "" {
		if err = json.Unmarshal([]byte(vals[5]), &argv); err != nil || len(argv) == 0 {
			return base, fmt.Errorf("launch argv must be a nonempty JSON string array, for example [\"my-server\",\"--model\",\"{model}\"]")
		}
	}
	path := vals[4]
	if path != "" {
		path, err = filepath.Abs(path)
		if err != nil {
			return base, err
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return base, err
		}
		assets, warnings := serve.Discover(nil, []string{path})
		if len(warnings) > 0 {
			return base, fmt.Errorf("checkpoint: %s", strings.Join(warnings, "; "))
		}
		if len(assets) != 1 {
			return base, fmt.Errorf("choose one checkpoint directory or the first GGUF shard; found %d candidates", len(assets))
		}
		if !assets[0].LoadCandidate() {
			return base, fmt.Errorf("checkpoint: %s", assets[0].Reason)
		}
		base.Serve.ModelDirs = appendUnique(base.Serve.ModelDirs, path)
	}
	if len(argv) == 0 && serve.Loopback(vals[2]) {

		if runtime == "openai" {
			for _, r := range serve.InstalledRecipes(vals[2]) {
				if r.Name == brand || r.Name == vals[0] {
					argv = r.Argv
					break
				}
			}
		}
	}
	if vals[6] != "" {
		vals[6], err = filepath.Abs(vals[6])
		if err != nil {
			return base, err
		}
		st, statErr := os.Stat(vals[6])
		if statErr != nil {
			return base, statErr
		}
		if !st.IsDir() {
			return base, fmt.Errorf("launch directory is not a directory: %s", vals[6])
		}
	}
	recipe := config.RecipeConfig{Name: vals[0], Kind: kind, Runtime: runtime, URL: vals[2], Argv: argv, Dir: vals[6]}
	if kind == serve.KindSidecar {
		recipe.Runtime = ""
	}
	found := false
	base.Serve.Recipes = append([]config.RecipeConfig(nil), base.Serve.Recipes...)
	for i := range base.Serve.Recipes {
		if base.Serve.Recipes[i].Name == recipe.Name {
			base.Serve.Recipes[i] = recipe
			found = true
		}
	}
	if !found {
		base.Serve.Recipes = append(base.Serve.Recipes, recipe)
	}
	base.Model.Server, base.Model.Name, base.Model.Runtime = vals[2], vals[3], runtime
	if modelFromPath && path != "" && runtime != "ollama" && runtime != "rulith" {
		base.Model.Name = path
	}
	// Native decision selections use their own config; do not put them through
	// the worker runtime validator.
	if kind == serve.KindSidecar {
		base.Model = config.Defaults().Model
		base.Decision.Server = vals[2]
		base.Decision.Model = vals[3]
		base.Decision.Runtime = runtime
		base.Decision.Path = path
		base.Decision.Revision = ""
	}
	if err = base.Validate(); err != nil {
		return base, err
	}
	return base, nil
}
func appendUnique(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(append([]string(nil), xs...), s)
}
func (m Model) connectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	f := m.Setup.Add
	if msg.Type == tea.KeyTab {
		f.Field = (f.Field + 1) % len(f.Values)
		return m, nil, true
	}
	if msg.Type == tea.KeyShiftTab {
		f.Field = (f.Field + len(f.Values) - 1) % len(f.Values)
		return m, nil, true
	}
	if msg.Type == tea.KeyCtrlT || msg.Type == tea.KeyCtrlS {
		base := m.Cfg
		base.Serve = m.Setup.Serve
		base.Decision = m.Setup.Judge
		c, err := f.candidate(base)
		if err != nil {
			f.Err = err.Error()
			return m, nil, true
		}
		runtime := strings.ToLower(strings.TrimSpace(f.Values[1].Value()))
		native := runtime == "laya" || runtime == "system_one"
		if native && m.Setup.Stage == 0 {
			f.Err = "Native decision runtimes belong in the Judge step"
			return m, nil, true
		}
		if msg.Type == tea.KeyCtrlT {
			endpoint := serve.Endpoint{Kind: serve.KindOpenAI, URL: c.Model.Server, Model: c.Model.Name}
			if c.Model.Runtime == "ollama" {
				endpoint.Kind = serve.KindOllama
			}
			if native {
				endpoint = serve.Endpoint{Kind: serve.KindSidecar, URL: c.Decision.Server, Model: c.Decision.Model}
			}
			timeout := time.Duration(c.Serve.ProbeTimeoutSeconds) * time.Second
			f.Receipt = "Checking server catalog…"
			return m, func() tea.Msg {
				h := serve.Probe(&http.Client{Timeout: timeout}, endpoint)
				receipt := "Connection failed: " + h.Err
				if h.Up {
					receipt = "Server responds; selected model not in catalog"
					for _, model := range h.Available {
						if model == endpoint.Model {
							receipt = "Model found in server catalog · inference and judgment quality unchecked"
						}
					}
					if native {
						receipt = "Native decision health responds · judgment not tested"
					}
				}
				return connectionTestMsg{f, receipt}
			}, true
		}
		m.Setup.Serve = c.Serve
		if m.Setup.Stage == 0 {
			m.Setup.Model = c.Model
			m.Setup.Pick = "added:" + c.Model.Server + ":" + c.Model.Name
			m.Setup.Added = append(m.Setup.Added, setupChoice{key: m.Setup.Pick, label: c.Model.Name, state: "Added connection · inference unchecked", model: c.Model})
			m.Setup.Stage = 1
		} else {
			if native {
				m.Setup.Judge = c.Decision
			} else {
				m.Setup.Judge.Server = c.Model.Server
				m.Setup.Judge.Model = c.Model.Name
				m.Setup.Judge.Runtime = c.Model.Runtime
				m.Setup.Judge.Path = ""
				m.Setup.Judge.Revision = ""
				m.Setup.Judge.Device = "auto"
			}
			m.Setup.Stage = 2
			m.Setup.CheckFocused = true
		}
		m.Setup.Add = nil
		m.Setup.Err = ""
		return m, nil, true
	}
	var cmd tea.Cmd
	f.Values[f.Field], cmd = f.Values[f.Field].Update(msg)
	f.Err = ""
	return m, cmd, true
}
func (m Model) renderConnection(w, h int) string {
	f := m.Setup.Add
	inner := BoxInner(w)
	var rows []string
	if h <= 6 {
		field := f.Values[f.Field]
		field.SetWidth(max(1, inner-2))
		field.SetHeight(1)
		rows = append(rows, strings.Split(field.View(), "\n")...)
		if f.Err != "" {
			rows = []string{ErrSt.Render(truncate(f.Err, inner))}
		}
		return panel(truncate(connectionLabels[f.Field], inner), rows, []string{"Tab · Ctrl+T test · Ctrl+S use · Esc"}, w, h)
	}
	for i, label := range connectionLabels {
		if i == f.Field {
			rows = append(rows, Title.Render(label))
			field := f.Values[i]
			field.SetWidth(max(1, inner-2))
			rows = append(rows, strings.Split(field.View(), "\n")...)
		} else {
			rows = append(rows, Guidance.Render(truncate(label+": "+f.Values[i].Value(), inner)))
		}
	}
	rows = listWindow(rows, min(len(rows)-1, f.Field), max(1, h-5))
	if f.Err != "" {
		rows = append(rows, ErrSt.Render(truncate(f.Err, inner)))
	}
	if f.Receipt != "" {
		rows = append(rows, Guidance.Render(truncate(f.Receipt, inner)))
	}
	return panel("Add server / model", rows, []string{"Tab fields · Ctrl+T test catalog · Ctrl+S use · Esc cancel"}, w, h)
}
