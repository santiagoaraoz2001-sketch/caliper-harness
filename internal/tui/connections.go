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
	"specific-harness/internal/decision"
	"specific-harness/internal/serve"
	"specific-harness/internal/tui/thirdparty/textarea"
	"strings"
	"time"
)

// The ordinary path requires an endpoint and model. Launch argv is an optional
// advanced setting for servers whose installation Caliper does not recognize.
type connectionForm struct {
	URLPinned                                                    bool // A user-entered or saved endpoint survives preset changes.
	Field                                                        int
	Values                                                       [8]textarea.Model
	Mode, OriginalName, OriginalURL, OriginalModel, OriginalPath string
	Advanced                                                     bool
	Err, Receipt                                                 string
	Catalog                                                      []string
	ProbeSeq                                                     uint64
}

var connectionLabels = []string{"Name", "Preset (←/→ to choose)", "Server URL", "Model ID (leave blank for checkpoint)", "Checkpoint folder or first GGUF shard (optional)", "Launch argv as JSON array (optional, advanced)", "Launch directory (optional)", "Local launcher (blank: external; preset; or executable path)"}

type connectionTestMsg struct {
	Form        *connectionForm
	Receipt     string
	Catalog     []string
	URL, Preset string
	Seq         uint64
}

func (m *Model) openConnectionForm() {
	f := &connectionForm{}
	values := []string{"my-server", "openai", "http://127.0.0.1:8080/v1", "", "", "", "", ""}
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
	serverOnly := f.Mode == "server" || f.Mode == "edit" && f.OriginalModel == ""
	if vals[0] == "" || vals[2] == "" || (vals[3] == "" && !serverOnly) {
		return base, fmt.Errorf("Name and server URL are required; add a model ID or checkpoint for a model")
	}
	if serverOnly {
		for _, saved := range base.Serve.Recipes {
			if saved.Name == vals[0] && (f.Mode == "server" || saved.Name != f.OriginalName) {
				return base, fmt.Errorf("server name %q already exists; edit that connection or choose another name", vals[0])
			}
		}
	}
	runtime := strings.ToLower(vals[1])
	brand := runtime
	if runtime == "custom" || runtime == "gufo" || runtime == "llama.cpp" || runtime == "llama-server" {
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
	}
	if len(argv) == 0 && vals[7] != "" {
		if !serve.Loopback(vals[2]) {
			return base, fmt.Errorf("local launcher requires a loopback URL")
		}
		if vals[7] == "preset" {
			var rec serve.Recipe
			var ok bool
			if kind == serve.KindSidecar {
				selected := base.Decision
				selected.Runtime, selected.Server = runtime, vals[2]
				rec, ok = serve.SidecarRecipe(serve.FindHarnessDir(), vals[2], selected.Model, decision.SelectionFor(selected))
			} else {
				rec, ok = serve.LocalRecipe(serve.FindHarnessDir(), runtime, vals[2], "{model}")
			}
			if !ok {
				return base, fmt.Errorf("preset launcher unavailable; enter its executable path or use an existing server URL")
			}
			argv = rec.Argv
		} else {
			u, _ := url.Parse(vals[2])
			if u.Port() == "" {
				return base, fmt.Errorf("local launcher URL needs an explicit port")
			}
			switch brand {
			case "mlx":
				argv = []string{vals[7], "--host", u.Hostname(), "--port", u.Port(), "--model", "{model}"}
			case "ollama":
				return base, fmt.Errorf("Ollama launcher: enter preset (binds OLLAMA_HOST) or advanced argv")
			case "llama-server":
				argv = []string{vals[7], "--host", u.Hostname(), "--port", u.Port(), "--model", "{model}"}
			case "gufo":
				argv = []string{vals[7], "serve", "--host", u.Hostname(), "--port", u.Port(), "llm", "--model", "{model}"}
			default:
				argv = []string{vals[7]}
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
	if vals[7] == "preset" {
		argv = nil
	}
	recipe := config.RecipeConfig{Launcher: vals[7], Name: vals[0], Kind: kind, Runtime: runtime, URL: vals[2], Argv: argv, Dir: vals[6]}

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
	if vals[3] != "" {
		base.Model.Server, base.Model.Name, base.Model.Runtime = vals[2], vals[3], runtime
		if modelFromPath && path != "" && runtime != "ollama" && runtime != "rulith" {
			base.Model.Name = path
		}

	}
	if vals[3] != "" {
		name := vals[3]
		if modelFromPath && path != "" && runtime != "ollama" && runtime != "rulith" {
			name = path
		}
		base.Serve.Models = append([]config.SavedModel(nil), base.Serve.Models...)
		saved := config.SavedModel{Server: vals[2], Name: name, Path: path}
		foundModel := false
		for i, model := range base.Serve.Models {
			if sameServeURL(model.Server, saved.Server) && model.Name == saved.Name {
				base.Serve.Models[i] = saved
				foundModel = true
			}
		}
		if !foundModel {
			base.Serve.Models = append(base.Serve.Models, saved)
		}
	}
	// Native decision selections use their own config; do not put them through
	// the worker runtime validator.
	if kind == serve.KindSidecar && vals[3] != "" {
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
	if msg.Type == tea.KeyF2 {
		f.Advanced = !f.Advanced
		f.Field = f.visibleFields()[0]
		return m, nil, true
	}
	if f.Field == 1 && (msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight) {
		previous := f.Values[1].Value()
		presets := []string{"custom", "mlx", "ollama", "llama.cpp", "laya", "system_one", "rulith", "gufo"}
		idx := 0
		for i, p := range presets {
			if f.Values[1].Value() == p {
				idx = i
			}
		}
		if msg.Type == tea.KeyLeft {
			idx = (idx + len(presets) - 1) % len(presets)
		} else {
			idx = (idx + 1) % len(presets)
		}
		if next := presetEndpoint(presets[idx]); !f.URLPinned && next != "" && (f.Values[2].Value() == "" || f.Values[2].Value() == presetEndpoint(previous)) {
			f.Values[2].SetValue(next)
		}
		f.Values[1].SetValue(presets[idx])
		if f.Values[7].Value() == "preset" && presets[idx] != "mlx" && presets[idx] != "ollama" && presets[idx] != "laya" && presets[idx] != "system_one" {
			f.Values[7].SetValue("")
		}
		f.Catalog, f.Receipt = nil, ""
		f.ProbeSeq++
		return m, nil, true
	}
	if f.Field == 3 && len(f.Catalog) > 0 && (msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight) {
		index := -1
		for i, name := range f.Catalog {
			if name == f.Values[3].Value() {
				index = i
				break
			}
		}
		if msg.Type == tea.KeyLeft {
			if index < 0 {
				index = 0
			}
			index = (index + len(f.Catalog) - 1) % len(f.Catalog)
		} else {
			index = (index + 1) % len(f.Catalog)
		}
		f.Values[3].SetValue(f.Catalog[index])
		return m, nil, true
	}
	if msg.Type == tea.KeyTab {
		fields := f.visibleFields()
		i := 0
		for j, n := range fields {
			if n == f.Field {
				i = j
			}
		}
		f.Field = fields[(i+1)%len(fields)]
		return m, nil, true
	}
	if msg.Type == tea.KeyShiftTab {
		fields := f.visibleFields()
		i := 0
		for j, n := range fields {
			if n == f.Field {
				i = j
			}
		}
		f.Field = fields[(i+len(fields)-1)%len(fields)]
		return m, nil, true
	}
	if msg.Type == tea.KeyCtrlT || msg.Type == tea.KeyCtrlS {
		base := m.Cfg
		if f.Mode == "" {
			base.Serve = m.Setup.Serve
			base.Decision = m.Setup.Judge
		}
		validation := *f
		if msg.Type == tea.KeyCtrlT {
			// Catalog lookup needs an endpoint, not a model identity already known.
			validation.Mode, validation.OriginalModel = "edit", ""
			validation.OriginalName = strings.TrimSpace(f.Values[0].Value())
		}
		c, err := validation.candidate(base)
		if err != nil {
			f.Err = err.Error()
			return m, nil, true
		}
		runtime := strings.ToLower(strings.TrimSpace(f.Values[1].Value()))
		native := runtime == "laya" || runtime == "system_one"
		if native && f.Mode == "" && m.Setup.Stage == 0 {
			f.Err = "Native decision runtimes belong in the Judge step"
			return m, nil, true
		}
		if msg.Type == tea.KeyCtrlT {
			endpoint := serve.Endpoint{Kind: serve.KindOpenAI, URL: strings.TrimSpace(f.Values[2].Value()), Model: strings.TrimSpace(f.Values[3].Value())}
			if runtime == "ollama" {
				endpoint.Kind = serve.KindOllama
			} else if runtime == "rulith" {
				endpoint.Kind = serve.KindRulith
			}
			if native {
				endpoint.Kind = serve.KindSidecar
			}
			timeout := time.Duration(c.Serve.ProbeTimeoutSeconds) * time.Second
			f.Receipt = "Checking server catalog…"
			f.Catalog = nil
			f.ProbeSeq++
			seq := f.ProbeSeq
			serverOnly := f.Mode == "server" || f.Mode == "edit" && f.OriginalModel == ""
			return m, func() tea.Msg {
				h := serve.Probe(&http.Client{Timeout: timeout}, endpoint)
				receipt := "Connection failed: " + h.Err
				var catalog []string
				if h.Up {
					for _, name := range append(append([]string(nil), h.Loaded...), h.Available...) {
						if name != "" {
							catalog = appendUnique(catalog, name)
						}
					}
					receipt = fmt.Sprintf("Server responds · %d models · Model ID ←/→ selects · inference unchecked", len(catalog))
					if len(catalog) == 0 {
						receipt = "Server responds · no model advertised · enter an ID or checkpoint manually"
					}
					if serverOnly && len(catalog) > 0 {
						receipt = fmt.Sprintf("Server responds · %d models · save, then select a model in Manage", len(catalog))
					}
					if native {
						receipt += " · judgment not tested"
					}
					if h.InventoryErr != "" {
						receipt += " · catalog incomplete: " + h.InventoryErr
					}
				}
				return connectionTestMsg{Form: f, Receipt: receipt, Catalog: catalog, URL: endpoint.URL, Preset: runtime, Seq: seq}
			}, true
		}
		if f.Mode != "" {
			if f.Mode == "model" || f.OriginalModel != "" {
				c.Serve.Recipes = m.Cfg.Serve.Recipes
			}
			// Saving a connection never silently assigns a role.
			c.Model, c.Decision = m.Cfg.Model, m.Cfg.Decision
			c.Serve.Models = append([]config.SavedModel(nil), c.Serve.Models...)
			if f.OriginalURL != "" {
				if f.OriginalModel != "" {
					var models []config.SavedModel
					for _, model := range c.Serve.Models {
						if sameServeURL(model.Server, f.OriginalURL) && model.Name == f.OriginalModel {
							continue
						}
						models = append(models, model)
					}
					c.Serve.Models = models
					// Candidate may replace an identical model; retain it in that case.
					name := strings.TrimSpace(f.Values[3].Value())
					if name == "" {
						name = filepath.Base(strings.TrimSpace(f.Values[4].Value()))
					}
					path := strings.TrimSpace(f.Values[4].Value())
					if path != "" {
						path, _ = filepath.Abs(path)
						path, _ = filepath.EvalSymlinks(path)
						if strings.TrimSpace(f.Values[3].Value()) == "" {
							name = path
						}
					}
					nextURL := strings.TrimSpace(f.Values[2].Value())
					found := false
					for _, model := range c.Serve.Models {
						found = found || sameServeURL(model.Server, nextURL) && model.Name == name
					}
					if !found {
						c.Serve.Models = append(c.Serve.Models, config.SavedModel{Server: nextURL, Name: name, Path: path})
					}
					if sameServeURL(c.Model.Server, f.OriginalURL) && (c.Model.Name == f.OriginalModel || f.OriginalPath != "" && c.Model.Name == f.OriginalPath) {
						c.Model.Server = nextURL
						c.Model.Name = name
						if c.Model.Runtime == "mlx" && f.OriginalPath != "" && m.Cfg.Model.Name == f.OriginalPath && path != "" {
							c.Model.Name = path
						}
					}
					if sameServeURL(c.Decision.Server, f.OriginalURL) && (c.Decision.Model == f.OriginalModel || c.Decision.Path == f.OriginalModel || f.OriginalPath != "" && (c.Decision.Path == f.OriginalPath || c.Decision.Model == f.OriginalPath)) {
						c.Decision.Server = nextURL
						c.Decision.Model = name
						if c.Decision.Runtime == "mlx" && f.OriginalPath != "" && m.Cfg.Decision.Model == f.OriginalPath && path != "" {
							c.Decision.Model = path
						}
						if native {
							c.Decision.Path = path
						}
					}

				} else {
					var recs []config.RecipeConfig
					for _, r := range c.Serve.Recipes {
						if r.Name == f.OriginalName && r.Name != strings.TrimSpace(f.Values[0].Value()) {
							continue
						}
						recs = append(recs, r)
					}
					c.Serve.Recipes = recs
					nextURL := strings.TrimSpace(f.Values[2].Value())
					for i := range c.Serve.Models {
						if sameServeURL(c.Serve.Models[i].Server, f.OriginalURL) {
							c.Serve.Models[i].Server = nextURL
						}
					}
					if sameServeURL(c.Model.Server, f.OriginalURL) {
						c.Model.Server = nextURL
						c.Model.Runtime = candidateRuntime(runtime)
					}
					if sameServeURL(c.Decision.Server, f.OriginalURL) {
						c.Decision.Server = nextURL
						c.Decision.Runtime = candidateRuntime(runtime)
					}
				}
			}
			if err := m.saveManaged(c); err != nil {
				f.Err = err.Error()
				return m, nil, true
			}
			m.Setup.Add = nil
			m.alert("info", "Connection saved · roles unchanged · t Test availability")
			return m, m.probeServe(), true
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
	before := f.Values[f.Field].Value()
	f.Values[f.Field], cmd = f.Values[f.Field].Update(msg)
	if (f.Field == 1 || f.Field == 2) && f.Values[f.Field].Value() != before {
		if f.Field == 2 {
			f.URLPinned = true
		}
		f.Catalog, f.Receipt = nil, ""
		f.ProbeSeq++
	}
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
	if f.Mode == "model" || f.OriginalModel != "" {
		rows = append(rows, Guidance.Render("Server: "+f.Values[0].Value()+" · "+f.Values[2].Value()))
	}
	fields := f.visibleFields()
	selected := 0
	for _, i := range fields {
		label := connectionLabels[i]
		if i == f.Field {
			selected = len(rows)
		}
		if i == f.Field {
			rows = append(rows, Title.Render(label))
			field := f.Values[i]
			field.SetWidth(max(1, inner-2))
			rows = append(rows, strings.Split(field.View(), "\n")...)
		} else {
			rows = append(rows, Guidance.Render(truncate(label+": "+f.Values[i].Value(), inner)))
		}
	}
	rows = listWindow(rows, min(len(rows)-1, selected), max(1, h-5))
	if f.Err != "" {
		rows = append(rows, ErrSt.Render(truncate(f.Err, inner)))
	}
	if f.Receipt != "" {
		rows = append(rows, Guidance.Render(truncate(f.Receipt, inner)))
	}
	title := "Add server / model"
	if f.Mode != "" {
		title = "Models & Servers · " + f.Mode
	}
	return panel(title, rows, []string{"Tab fields · ←/→ preset/model · F2 Advanced · Ctrl+T Test · Ctrl+S Save · Esc"}, w, h)
}

func (f *connectionForm) visibleFields() []int {
	var fields []int
	switch {
	case f.Mode == "model" || f.OriginalModel != "":
		fields = []int{3, 4}
	case f.Mode == "server" || f.Mode == "edit":
		fields = []int{0, 1, 2}
	default:
		fields = []int{0, 1, 2, 3, 4}
	}
	if f.Advanced {
		if f.Mode != "model" && f.OriginalModel == "" {
			fields = append(fields, 7)
		}
		fields = append(fields, 5, 6)
	}
	return fields
}

func candidateRuntime(runtime string) string {
	switch runtime {
	case "custom", "llama.cpp", "llama-server", "gufo":
		return "openai"
	}
	return runtime
}

// Reuse the serving owner's conventional endpoints only for untouched presets.
func presetEndpoint(preset string) string {
	switch preset {
	case "ollama":
		return serve.OllamaURL
	case "laya", "system_one":
		return serve.SidecarURL
	case "openai", "custom", "mlx", "llama.cpp", "llama-server":
		return serve.MLXURL
	}
	return ""
}
