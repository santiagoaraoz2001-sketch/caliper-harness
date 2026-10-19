package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/hw"
	"specific-harness/internal/serve"
)

// ServeRow is one status-screen row: a server, or one model on it.
// Model rows carry their server's URL in Ep and the model in Ep.Model.
type ServeRow struct {
	Ep      serve.Endpoint
	Health  serve.Health
	Have    bool
	Recipe  *serve.Recipe
	Ours    bool
	Worker  bool
	Sidecar bool
	IsModel bool
	// Resident marks a model row loaded in memory (x unloads it);
	// available-only rows load installed weights on l.
	Resident bool
	Asset    *serve.Asset
}

// ServeState is the status screen: the endpoint table, selection,
// processes we started, and one load/release in flight.
type ServeState struct {
	Rows      []ServeRow
	Sel       int
	Scroll    int
	Spawned   map[string]*serve.Proc
	Loading   string
	Focus     string
	Warnings  []string
	Installed []string
	cancel    context.CancelFunc
	// Mem is the hardware profile behind the MEMORY readout; MemErr
	// names a failed detection (readout shows unknown, not zero).
	Mem    hw.Profile
	MemErr string
}

// serveProbedMsg carries one finished probe round; serveLoadMsg ends
// a background resident action.
type serveProbedMsg struct {
	rows        []ServeRow
	mem         hw.Profile
	memErr      string
	warnings    []string
	installed   []string
	application *decisionApplied
}

// bytesPerToken estimates KV-cache footprint per context token. Rough
// by nature — architectures vary — so the readout labels it est.
const bytesPerToken = 2048

// memCalc is the max-load calculator: context tokens at max
// concurrency (worker window plus every subagent's), estimated bytes,
// and whether the estimate exceeds hardware. Non-positive subWindow
// inherits the worker window; non-positive agents autodetect.
func memCalc(window, maxAgents, subWindow int, mem hw.Profile) (tokens int, bytes uint64, warn bool) {
	if maxAgents <= 0 {
		maxAgents = hw.MaxSubagents(mem)
	}
	if subWindow <= 0 {
		subWindow = window
	}
	tokens = window + maxAgents*subWindow
	bytes = uint64(tokens) * bytesPerToken
	return tokens, bytes, mem.MemTotalBytes > 0 && bytes > mem.MemTotalBytes
}

// humanBytes renders GiB/MiB compactly for the MEMORY readout.
func humanBytes(b uint64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1fG", float64(b)/float64(1<<30))
	}
	if b >= 1<<20 {
		return fmt.Sprintf("%.0fM", float64(b)/float64(1<<20))
	}
	return fmt.Sprintf("%dB", b)
}

type serveLoadMsg struct {
	notice  string
	model   string
	url     string
	proc    *serve.Proc
	stopped bool
	err     error
}

func (m *Model) serveActive() bool {
	return m.Tabs[m.Active].at.view == ViewManage
}

// sameServeURL matches the root/API-base alternatives already accepted by Probe.
// Custom prefixes remain part of identity; only the optional /v1 suffix differs.
func sameServeURL(a, b string) bool {
	root := func(s string) string { return strings.TrimSuffix(strings.TrimRight(s, "/"), "/v1") }
	return root(a) == root(b)
}

// serveEndpoints includes only saved endpoints, selected legacy roles and owned children.
func (m *Model) serveEndpoints() []serve.Endpoint {
	var out []serve.Endpoint
	add := func(ep serve.Endpoint) {
		if ep.URL == "" {
			return
		}
		for _, e := range out {
			if sameServeURL(e.URL, ep.URL) {
				return
			}
		}
		out = append(out, ep)
	}
	for _, r := range m.Cfg.Serve.Recipes {
		add(serve.Endpoint{Kind: r.Kind, Label: r.Name, URL: r.URL})
	}
	for _, model := range m.Cfg.Serve.Models {
		add(serve.Endpoint{Kind: serve.KindOpenAI, Label: "saved server", URL: model.Server})
	}
	kind := serve.KindOpenAI
	if m.Cfg.Model.Runtime == "ollama" {
		kind = serve.KindOllama
	}
	if m.Cfg.Model.Runtime == "rulith" {
		kind = serve.KindRulith
	}
	if m.Cfg.Model.Name != "" {
		add(serve.Endpoint{Kind: kind, Label: m.Cfg.Model.Runtime, URL: m.Cfg.Model.Server, Model: m.Cfg.Model.Name})
	}
	if !m.Cfg.Decision.Disabled && (m.Cfg.Model.Name != "" || m.Cfg.Decision.Path != "" || m.Cfg.Decision.Model != config.Defaults().Decision.Model || m.Cfg.Decision.Server != config.Defaults().Decision.Server) {
		kind = serve.KindOpenAI
		if decision.NativeRuntime(m.Cfg.Decision.Runtime) {
			kind = serve.KindSidecar
		} else if m.Cfg.Decision.Runtime == "ollama" {
			kind = serve.KindOllama
		} else if m.Cfg.Decision.Runtime == "rulith" {
			kind = serve.KindRulith
		}
		add(serve.Endpoint{Kind: kind, Label: m.Cfg.Decision.Runtime, URL: m.Cfg.Decision.Server, Model: m.Cfg.Decision.Model})
	}
	for endpoint, proc := range m.Serve.Spawned {
		if proc.PID() == 0 {
			continue
		}
		kind, label := serve.KindAuto, "owned server"
		for _, row := range m.Serve.Rows {
			if sameServeURL(row.Ep.URL, endpoint) && !row.IsModel {
				kind, label = row.Ep.Kind, row.Ep.Label
				break
			}
		}
		add(serve.Endpoint{Kind: kind, Label: label, URL: endpoint})
	}
	return out
}

// Resolve launchers only for explicitly saved recipes or the selected legacy role.
func (m *Model) serveRecipes() []serve.Recipe {
	var out []serve.Recipe
	for _, r := range m.Cfg.Serve.Recipes {
		rec := serve.Recipe{Name: r.Name, Kind: r.Kind, Runtime: r.Runtime, URL: r.URL, Argv: r.Argv, Dir: r.Dir}
		if r.Launcher == "preset" {
			if known, ok := serve.LocalRecipe(serve.FindHarnessDir(), r.Runtime, r.URL, "{model}"); ok {
				rec = known
				rec.Name = r.Name
			}
		}
		if r.Launcher == "preset" && len(rec.Argv) == 0 && r.Kind == serve.KindSidecar && (decision.NativeRuntime(r.Runtime) || r.Runtime == "" && decision.NativeRuntime(m.Cfg.Decision.Runtime)) {
			selected := m.Cfg.Decision
			if r.Runtime != "" {
				selected.Runtime = r.Runtime
			}
			selected.Server = r.URL
			if known, ok := serve.SidecarRecipe(serve.FindHarnessDir(), r.URL, selected.Model, decision.SelectionFor(selected)); ok {
				rec = known
				rec.Runtime = selected.Runtime
				rec.Name = r.Name
			}
		}
		out = append(out, rec)
	}
	legacy := func(runtime, url, model string, native bool) {
		for _, r := range out {
			if sameServeURL(r.URL, url) {
				return
			}
		}
		var rec serve.Recipe
		var ok bool
		if native {
			rec, ok = serve.SidecarRecipe(serve.FindHarnessDir(), url, model, decision.SelectionFor(m.Cfg.Decision))
		} else {
			rec, ok = serve.LocalRecipe(serve.FindHarnessDir(), runtime, url, model)
		}
		if ok {
			out = append(out, rec)
		}
	}
	legacy(m.Cfg.Model.Runtime, m.Cfg.Model.Server, m.Cfg.Model.Name, false)
	if !m.Cfg.Decision.Disabled {
		legacy(m.Cfg.Decision.Runtime, m.Cfg.Decision.Server, m.Cfg.Decision.Model, decision.NativeRuntime(m.Cfg.Decision.Runtime))
	}
	return out
}

func (m *Model) probeServe() tea.Cmd {
	return m.probeServeWith(&http.Client{Timeout: time.Duration(m.Cfg.Serve.ProbeTimeoutSeconds) * time.Second})
}
func (m *Model) probeServeWith(client *http.Client) tea.Cmd {
	return m.probeServeWithDiscovery(client, nil)
}

// The obsolete discovery hook remains test-injectable to prove it is never called.
func (m *Model) probeServeWithDiscovery(client *http.Client, _ func(context.Context, *http.Client) ([]serve.DiscoveredServer, error)) tea.Cmd {
	endpoints, recipes := m.serveEndpoints(), m.serveRecipes()
	spawned := make(map[string]*serve.Proc, len(m.Serve.Spawned))
	for u, p := range m.Serve.Spawned {
		spawned[u] = p
	}
	coding, judge := m.Cfg.Model, m.Cfg.Decision
	models := append([]config.SavedModel(nil), m.Cfg.Serve.Models...)
	if coding.Name != "" {
		found := false
		for _, model := range models {
			if sameServeURL(model.Server, coding.Server) && (model.Name == coding.Name || model.Path == coding.Name) {
				found = true
			}
		}
		if !found {
			path := ""
			if _, err := os.Stat(coding.Name); err == nil {
				path = coding.Name
			}
			models = append(models, config.SavedModel{Server: coding.Server, Name: coding.Name, Path: path})
		}
	}
	if !judge.Disabled {
		found := false
		for _, model := range models {
			if sameServeURL(model.Server, judge.Server) && (model.Name == judge.Model || judge.Path != "" && model.Path == judge.Path) {
				found = true
			}
		}
		if !found {
			models = append(models, config.SavedModel{Server: judge.Server, Name: judge.Model, Path: judge.Path})
		}
	}
	return func() tea.Msg {
		health := serve.ProbeAll(client, endpoints)
		var rows []ServeRow
		var warnings []string
		for i, e := range endpoints {
			r := ServeRow{Ep: e, Health: health[i], Have: true, Worker: sameServeURL(e.URL, coding.Server), Sidecar: e.Kind == serve.KindSidecar}
			for j := range recipes {
				if sameServeURL(recipes[j].URL, e.URL) {
					r.Recipe = &recipes[j]
					break
				}
			}
			if p := spawned[e.URL]; p != nil {
				r.Ours = p.PID() != 0 && (!r.Sidecar || health[i].Decision != nil && health[i].Decision.PID == p.PID())
			}
			if r.Ours && spawned[e.URL].Runtime() == "mlx" {
				h, err := serve.OwnedMLXReady(context.Background(), client, spawned[e.URL])
				if err != nil {
					h.Err = err.Error()
				}
				r.Health = h
				r.Ours = err == nil
			}
			rows = append(rows, r)
			saved := append([]config.SavedModel(nil), models...)
			for _, name := range append(append([]string(nil), r.Health.Loaded...), r.Health.Available...) {
				saved = append(saved, config.SavedModel{Server: e.URL, Name: name})
			}
			seen := map[string]bool{}
			for _, model := range saved {
				if !sameServeURL(model.Server, e.URL) || model.Name == "" || seen[model.Name] {
					continue
				}
				seen[model.Name] = true
				mr := r
				mr.IsModel = true
				mr.Ep.Model = model.Name
				path := model.Path
				if path != "" {
					assets, notes := serve.Discover(nil, []string{path})
					warnings = append(warnings, notes...)
					if len(assets) == 1 {
						a := assets[0]
						if r.Recipe != nil {
							a.Runtime = r.Recipe.Runtime
						}
						if r.Sidecar {
							a.Runtime = judge.Runtime
							if r.Recipe != nil && decision.NativeRuntime(r.Recipe.Runtime) {
								a.Runtime = r.Recipe.Runtime
							}
						}
						mr.Asset = &a
					}
				}
				for _, name := range r.Health.Loaded {
					if name == model.Name || mr.Asset != nil && name == mr.Asset.Path {
						mr.Resident = r.Health.Kind == serve.KindOllama || r.Ours
					}
				}
				rows = append(rows, mr)
			}
		}
		mem, err := hw.Detect()
		msg := serveProbedMsg{rows: groupServeRows(rows), mem: mem, warnings: warnings}
		if err != nil {
			msg.memErr = err.Error()
		}
		return msg
	}
}

// groupServeRows keeps every model directly below its actual server/recipe.
// Discovery appends local assets after probing; their position is not ownership.
func groupServeRows(rows []ServeRow) []ServeRow {
	out := make([]ServeRow, 0, len(rows))
	used := make([]bool, len(rows))
	for i, server := range rows {
		if server.IsModel {
			continue
		}
		out = append(out, server)
		used[i] = true
		for j, model := range rows {
			if used[j] || !model.IsModel || !sameServeURL(model.Ep.URL, server.Ep.URL) {
				continue
			}
			if model.Recipe != nil && (server.Recipe == nil || model.Recipe.Name != server.Recipe.Name) {
				continue
			}
			out = append(out, model)
			used[j] = true
		}
	}
	for i, row := range rows {
		if !used[i] {
			out = append(out, row)
		}
	}
	return out
}

// sameServerPort compares complete endpoint identity, not just its port.
func sameServerPort(a, b string) bool { return serve.SameEndpoint(a, b) }

// serveKeyOf identifies a row across probes: server URL plus model
// (server rows carry a blank model).
func serveKeyOf(r ServeRow) string {
	kind := "server"
	if r.IsModel {
		kind = "model"
	}
	if r.Asset != nil {
		kind = "asset"
	}
	key := kind + "\x00" + r.Ep.URL + "\x00" + r.Ep.Model
	if r.Recipe != nil {
		key += "\x00" + r.Recipe.Name
	}
	return key
}

// onServeProbed swaps in fresh results, keeping the selection on its row.
func (m *Model) onServeProbed(msg serveProbedMsg) {
	if a := msg.application; a != nil {
		m.Settings.Applying = false
		if a.err != nil {
			m.Settings.DecisionNotice = "activation not confirmed: " + a.err.Error()
			m.alert("err", m.Settings.DecisionNotice)
			return
		}
		if a.seq != m.Settings.DecisionSeq || m.Serve.Spawned[a.url] != a.proc || a.proc.PID() != a.health.PID {
			m.Settings.DecisionNotice = "activation receipt stale — refresh status before applying again"
			m.alert("err", m.Settings.DecisionNotice)
			return
		}
		m.Cfg.Decision = a.cfg
		m.Settings.DecisionDraft = nil
		m.Settings.DecisionNotice = "decision active on " + a.health.Active.NativeDevice + " — s saves"
		for i := range m.Serve.Rows {
			if m.Serve.Rows[i].Ep.URL == a.url && m.Serve.Rows[i].Sidecar {
				m.Serve.Rows[i].Health.Decision = &a.health
			}
		}
		m.alert("info", m.Settings.DecisionNotice)
		return
	}
	keep := ""
	previous := m.Serve.Sel
	if m.Serve.Sel < len(m.Serve.Rows) {
		keep = serveKeyOf(m.Serve.Rows[m.Serve.Sel])
	}
	m.Serve.Rows = msg.rows
	m.Serve.Installed = msg.installed
	for _, r := range msg.rows {
		if !r.Sidecar && r.Have && (sameServeURL(r.Ep.URL, m.Cfg.Model.Server) || r.Health.APIBase == m.Cfg.Model.Server) {
			state := "offline"
			if r.Health.Up {
				state = "online"
			} else if r.Health.Reachable {
				state = "issue"
			}
			m.WorkerObservation = workerObservation{Server: m.Cfg.Model.Server, Model: m.Cfg.Model.Name, State: state}
			break
		}
	}
	m.Serve.Warnings = msg.warnings
	m.Serve.Mem, m.Serve.MemErr = msg.mem, msg.memErr
	m.Serve.Sel = 0
	for i, r := range msg.rows {
		if serveKeyOf(r) == keep {
			m.Serve.Sel = i
		}
	}
	if wanted := m.Serve.Focus; wanted != "" {
		canonical, _ := filepath.EvalSymlinks(wanted)
		var matches, active []int
		for i, r := range msg.rows {
			if r.IsModel && (r.Ep.Model == wanted || canonical != "" && r.Ep.Model == canonical || r.Asset != nil && r.Asset.ID == wanted) {
				matches = append(matches, i)
				if sameServeURL(r.Ep.URL, m.Cfg.Model.Server) || r.Health.APIBase == m.Cfg.Model.Server {
					active = append(active, i)
				}
			}
		}
		if len(active) > 0 {
			matches = active
		}
		if len(matches) == 1 {
			m.Serve.Sel = matches[0]
		} else if len(matches) == 0 {
			m.alert("err", "model/path not saved or in the selected server catalog: "+wanted)
		} else {
			m.alert("err", "model identity is ambiguous — select an exact server/path row")
		}
	}
	m.Serve.Focus = ""
	if m.Serve.Sel != previous || m.serveCur() == nil || serveKeyOf(*m.serveCur()) != keep {
		m.followServe()
	}
}

// renderMemory draws the max-load calculator: hardware total vs the
// estimated footprint at max concurrency vs currently resident VRAM.
// MLX and sidecar residency is unknowable from their APIs — unknown,
// never zero.
func (m Model) renderMemory() string {
	lines := []string{SectionSt.Render("MEMORY")}
	if m.Serve.MemErr != "" {
		lines = append(lines, Muted.Render("hardware unknown: "+truncate(m.Serve.MemErr, 60)))
		return strings.Join(lines, "\n")
	}
	hwLine := humanBytes(m.Serve.Mem.MemTotalBytes)
	if m.Serve.Mem.UnifiedMemory {
		hwLine += " unified"
	}
	tokens, est, warn := memCalc(m.St.CtxWindow,
		m.Cfg.Subagents.MaxSubagents, m.Cfg.Subagents.ContextWindow, m.Serve.Mem)
	var resident uint64
	unknown := false
	for _, r := range m.Serve.Rows {
		if r.IsModel || !r.Health.Up {
			continue
		}
		for _, b := range r.Health.Vram {
			resident += b
		}
		if len(r.Health.Vram) == 0 {
			unknown = true
		}
	}
	line := fmt.Sprintf("hardware %s · max-at-max-use ≈%s est (%dk tokens) · resident %s",
		hwLine, humanBytes(est), tokens/1000, humanBytes(resident))
	if unknown {
		line += " + other servers unknown"
	}
	lines = append(lines, Body.Render(line))
	if warn {
		lines = append(lines, ErrSt.Render("estimate exceeds hardware — lower max_subagents or context windows"))
	}
	return strings.Join(lines, "\n")
}

// serveCur is the selected row, or nil on an empty table.
func (m *Model) serveCur() *ServeRow {
	if m.Serve.Sel < 0 || m.Serve.Sel >= len(m.Serve.Rows) {
		return nil
	}
	return &m.Serve.Rows[m.Serve.Sel]
}

// persist writes the config back so runtime/device switches survive
// reopening. Empty path means no file backs this session.
func (m *Model) persist() error {
	if m.CfgPath == "" {
		return fmt.Errorf("no config file in use — relaunch with --config to persist")
	}
	if err := config.SaveEffective(m.CfgPath, m.Cfg); err != nil {
		return err
	}
	m.syncSetupAfterSave()
	return nil
}

// serveUse switches the worker runtime to the selected row. A model
// row takes both its server and its model.
func (m *Model) serveUse() {
	r := m.serveCur()
	if r == nil {
		return
	}
	if r.Sidecar {
		m.alert("err", "the sidecar is not a worker runtime")
		return
	}
	if !r.IsModel && (!r.Have || !r.Health.Up) {
		m.alert("err", "select a saved model row to assign Coding while offline")
		return
	}
	runtime := r.Health.Runtime
	if runtime == "" && r.Ours {
		runtime = m.Serve.Spawned[r.Ep.URL].Runtime()
	}
	if runtime == "" && r.Recipe != nil {
		runtime = r.Recipe.Runtime
	}
	if runtime == "" && sameServeURL(r.Ep.URL, m.Cfg.Model.Server) {
		runtime = m.Cfg.Model.Runtime
	}
	if r.Ep.Kind == serve.KindOllama {
		runtime = "ollama"
	}
	if r.Ep.Kind == serve.KindRulith {
		runtime = "rulith"
	}
	if runtime == "" {
		runtime = "openai"
	}
	old := m.Cfg.Model
	base := r.Health.APIBase
	if base == "" {
		base = r.Ep.URL
	}
	if r.Asset != nil && r.Asset.Runtime != "" {
		runtime = r.Asset.Runtime
	}
	m.Cfg.Model.Runtime = runtime
	m.Cfg.Model.Server = base
	if r.IsModel {
		m.Cfg.Model.Name = r.Ep.Model
		if runtime == "mlx" && r.Asset != nil && r.Recipe != nil && r.Recipe.HasModelPlaceholder() {
			m.Cfg.Model.Name = r.Asset.Path
		}
	}
	if err := m.persist(); err != nil {
		m.Cfg.Model = old
		m.alert("err", err.Error())
		return
	}
	m.Server = base
	if r.IsModel {
		m.ModelName = m.Cfg.Model.Name
		m.St.Model = m.Cfg.Model.Name
		m.alert("info", fmt.Sprintf("runtime: %s on %s (next run)", r.Ep.Model, r.Ep.Label))
		return
	}
	m.alert("info", fmt.Sprintf("runtime: %s (next run)", r.Ep.Label))
}

// serveUseJudge selects an existing catalog entry without loading or downloading.
func (m *Model) serveUseJudge() {
	r := m.serveCur()
	if r == nil || m.servingBusy() {
		return
	}
	if r.Sidecar && r.Asset != nil {
		next := m.Cfg
		if m.Settings.DecisionDraft != nil {
			next.Decision = *m.Settings.DecisionDraft
		}
		a := r.Asset
		if r.Recipe != nil && decision.NativeRuntime(r.Recipe.Runtime) {
			next.Decision.Runtime = r.Recipe.Runtime
		}
		if !a.Complete || a.Format != "safetensors" || a.Runtime != next.Decision.Runtime || (a.Runtime != "laya" && a.Runtime != "system_one") {
			m.alert("err", "select a native decision runtime in Settings, then refresh its local candidates")
			return
		}
		path, err := filepath.EvalSymlinks(a.Path)
		if err != nil || !filepath.IsAbs(path) || path != a.Path {
			m.alert("err", "judge candidate path changed or unavailable — refresh inventory")
			return
		}
		next.Decision.Disabled = false
		if r.Ep.URL != "" {
			next.Decision.Server = r.Ep.URL
		}
		next.Decision.Model, next.Decision.Path, next.Decision.Revision = a.ID, path, a.Revision
		if err := next.Validate(); err != nil {
			m.alert("err", err.Error())
			return
		}
		m.Settings.DecisionDraft = &next.Decision
		m.Settings.DecisionSeq++
		m.Settings.DecisionNotice = fmt.Sprintf("judge candidate staged for %s; compatibility unconfirmed; trust_remote_code=%t retained — a applies; Settings s saves", a.Runtime, next.Decision.TrustRemoteCode)
		m.alert("info", m.Settings.DecisionNotice)
		return
	}
	if r.IsModel && r.Sidecar {
		next := m.Cfg
		next.Decision.Disabled = false
		next.Decision.Server = r.Ep.URL
		next.Decision.Model = r.Ep.Model
		next.Decision.Path = ""
		next.Decision.Revision = ""
		if r.Recipe != nil && decision.NativeRuntime(r.Recipe.Runtime) {
			next.Decision.Runtime = r.Recipe.Runtime
		}
		if err := next.Validate(); err != nil {
			m.alert("err", err.Error())
			return
		}
		m.Settings.DecisionDraft = &next.Decision
		m.Settings.DecisionSeq++
		m.Settings.DecisionNotice = "Native Judge selection staged · availability unconfirmed · a apply · Settings s save"
		m.alert("info", m.Settings.DecisionNotice)
		return
	}
	if r.IsModel && !r.Sidecar {
		next := m.Cfg
		next.Decision.Disabled = false
		next.Decision.Server = r.Health.APIBase
		if next.Decision.Server == "" {
			next.Decision.Server = r.Ep.URL
		}
		next.Decision.Model = r.Ep.Model
		next.Decision.Path = ""
		next.Decision.Revision = ""
		next.Decision.Device = "auto"
		next.Decision.Runtime = r.Health.Runtime
		if r.Health.Kind == serve.KindOllama || r.Ep.Kind == serve.KindOllama {
			next.Decision.Runtime = "ollama"
		}
		if r.Health.Kind == serve.KindRulith || r.Ep.Kind == serve.KindRulith {
			next.Decision.Runtime = "rulith"
		}
		if r.Asset != nil {
			next.Decision.Runtime = r.Asset.Runtime
		}
		if next.Decision.Runtime == "" && r.Recipe != nil {
			next.Decision.Runtime = r.Recipe.Runtime
		}
		if next.Decision.Runtime == "" {
			next.Decision.Runtime = "openai"
		}
		if next.Decision.Runtime == "mlx" && r.Asset != nil && r.Recipe != nil && r.Recipe.HasModelPlaceholder() {
			next.Decision.Model = r.Asset.Path
		}
		if err := next.Validate(); err != nil {
			m.alert("err", err.Error())
			return
		}
		m.Settings.DecisionDraft = &next.Decision
		m.Settings.DecisionSeq++
		m.Settings.DecisionNotice = "Chat judge staged; decision-trained models recommended; confidence is self-reported · a applies · Settings s saves"
		m.alert("info", m.Settings.DecisionNotice)
		return
	}
	m.alert("err", "select a model row for the judge")
}

// labelOf names a row for messages: the model on model rows.
func labelOf(r *ServeRow) string {
	if r.IsModel {
		return r.Ep.Model
	}
	return r.Ep.Label
}

// recipeFor resolves the selected row's recipe. Sidecar rows rebuild
// fresh every time so a just-selected decision model is honored;
// other rows use the probe-time recipe.
func (m *Model) recipeFor(r *ServeRow) *serve.Recipe {
	if m.Cfg.Decision.Disabled && r.Sidecar {
		return nil
	}
	if r.Sidecar {
		for _, configured := range m.Cfg.Serve.Recipes {
			if configured.Kind == serve.KindSidecar && configured.URL == r.Ep.URL && len(configured.Argv) > 0 && (r.Recipe == nil || r.Recipe.Name == configured.Name) {
				return &serve.Recipe{Name: configured.Name, Kind: configured.Kind, URL: configured.URL, Argv: configured.Argv, Dir: configured.Dir}
			}
		}
		for _, rec := range m.serveRecipes() {
			if rec.Kind == serve.KindSidecar && rec.URL == r.Ep.URL {
				return &rec
			}
		}
		if rec, ok := serve.SidecarRecipe(serve.FindHarnessDir(), r.Ep.URL, m.Cfg.Decision.Model, decision.SelectionFor(m.Cfg.Decision)); ok {
			return &rec
		}
		return nil
	}
	return r.Recipe
}

// serveStart launches the selected row's recipe in the background;
// the tick confirms it came up.
func (m *Model) serveStart() {
	r := m.serveCur()
	if r == nil {
		return
	}
	if r.IsModel {
		m.alert("err", "s starts servers — select the server row")
		return
	}
	if m.servingBusy() {
		return
	}
	if m.Serve.Spawned[r.Ep.URL].PID() != 0 {
		m.alert("info", "live owned process already uses this endpoint — release its server row before starting another")
		return
	}
	if !r.Sidecar && (r.Health.Reachable || !serve.Loopback(r.Ep.URL)) {
		m.alert("err", "endpoint responds or is external — no second server started")
		return
	}
	if r.Sidecar && (!serve.Loopback(r.Ep.URL) || r.Health.Reachable || m.Serve.Spawned[r.Ep.URL].PID() != 0) {
		m.alert("info", "sidecar endpoint responds, is external, or has a live owned process — no second process started; inspect status")
		return
	}
	if r.Have && r.Health.Up {
		m.alert("info", fmt.Sprintf("%s is already up", r.Ep.Label))
		return
	}
	rec := m.recipeFor(r)
	if rec == nil || len(rec.Argv) == 0 {
		m.alert("err", fmt.Sprintf("no start recipe for %s", r.Ep.URL))
		return
	}
	if rec.Runtime == "mlx" || rec.Kind == serve.KindRulith || rec.Discovered {
		m.alert("err", "select an existing local model row and l load")
		return
	}
	if r.Sidecar {
		custom := false
		for _, configured := range m.Cfg.Serve.Recipes {
			custom = custom || configured.Kind == serve.KindSidecar && configured.URL == r.Ep.URL && len(configured.Argv) > 0
		}
		if !custom {
			pc, err := m.Cfg.LoadPrompts()
			if err != nil {
				m.alert("err", err.Error())
				return
			}
			bound, err := serve.SidecarPrompts(*rec, pc)
			if err != nil {
				m.alert("err", err.Error())
				return
			}
			rec = &bound
		}
	}
	selectedModel := m.Cfg.Model.Name
	if r.Sidecar {
		selectedModel = m.Cfg.Decision.Model
	}
	bound, err := serve.BindModel(*rec, selectedModel)
	if err != nil {
		m.alert("err", err.Error())
		return
	}
	rec = &bound
	log := filepath.Join(os.TempDir(), "caliper-harness-"+rec.Name+".log")
	p, err := serve.Start(*rec, log, time.Duration(m.Cfg.Serve.ProbeTimeoutSeconds)*time.Second)
	if err != nil {
		m.alert("err", err.Error())
		return
	}
	if m.Serve.Spawned == nil {
		m.Serve.Spawned = map[string]*serve.Proc{}
	}
	m.Serve.Spawned[r.Ep.URL] = p
	m.alert("info", fmt.Sprintf("started %s — confirming (log %s)", rec.Name, log))
}

func (m *Model) servingPending() bool {
	if m.Serve.Loading != "" {
		m.alert("info", "serving action owns the worker slot — wait or quit to cancel")
		return true
	}
	return false
}

// servingBusy protects active worker use and one unsettled serving operation.
func (m *Model) servingBusy() bool {
	if m.Chat.Running || m.Wf.Busy != "" || m.compactBusy(false) || m.Serve.Loading != "" {
		m.alert("info", "worker use or serving action still unsettled")
		return true
	}
	return false
}
func (m *Model) serveUnload() tea.Cmd { return m.serveAction(false) }
func (m *Model) serveLoad() tea.Cmd   { return m.serveAction(true) }
func (m *Model) serveAction(load bool) tea.Cmd {
	r := m.serveCur()
	if r == nil || m.servingBusy() {
		return nil
	}
	row := *r
	if row.Recipe != nil {
		rec := *row.Recipe
		rec.Argv = append([]string(nil), rec.Argv...)
		rec.Env = append([]string(nil), rec.Env...)
		row.Recipe = &rec
	}
	if row.Asset != nil {
		asset := *row.Asset
		row.Asset = &asset
	}
	if row.Sidecar {
		if row.Asset != nil {
			m.alert("info", "j stages this judge candidate; a applies; select the sidecar server row to stop it")
			return nil
		}
		if load {
			m.alert("err", "no load API for sidecar: s start, a apply")
			return nil
		}
		if !row.Health.Up && m.Serve.Spawned[row.Ep.URL].PID() == 0 {
			m.alert("info", "sidecar is already down")
			return nil
		}
		p := m.Serve.Spawned[row.Ep.URL]
		// Stopping our exact child does not require a healthy HTTP service.
		// Activation still requires validated native identity in decisionOwner.
		if p == nil || p.PID() == 0 {
			m.alert("err", "that sidecar is not ours — stop it where you started it")
			return nil
		}
		if err := p.Stop(); err != nil {
			m.alert("err", err.Error())
		} else {
			delete(m.Serve.Spawned, row.Ep.URL)
			m.alert("info", "sidecar stopped")
		}
		return nil
	}
	p := m.Serve.Spawned[row.Ep.URL]
	if !load && row.IsModel && !row.Resident {
		m.alert("info", "model is not resident (unconfirmed) — l load; select owned server to release an unready child")
		return nil
	}
	if row.Asset != nil && (!row.Asset.LoadCandidate() || row.Asset.Runtime != "mlx" && row.Asset.Runtime != "rulith" && (row.Recipe == nil || !row.Recipe.HasModelPlaceholder())) {
		m.alert("err", row.Asset.Reason)
		return nil
	}
	nativeRulith := row.Ep.Kind == serve.KindRulith && row.IsModel && row.Asset != nil && row.Asset.Runtime == "rulith"
	nativeOllama := row.IsModel && row.Asset == nil && (row.Health.Kind == serve.KindOllama || row.Ep.Kind == serve.KindOllama)
	if !nativeOllama && !nativeRulith && !load && p.PID() == 0 {
		m.alert("err", "no unload API or owned runtime to release; external residency stays unchanged")
		return nil
	}
	if load && !nativeOllama && !nativeRulith && row.Asset == nil {
		m.alert("err", "select an installed Ollama model or complete local MLX candidate")
		return nil
	}
	if load && row.Asset != nil && p.PID() != 0 {
		m.alert("err", "release the existing owned worker before loading another model")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.Serve.cancel = cancel
	m.Serve.Loading = serveKeyOf(row)
	action := "loading"
	if !load {
		action = "releasing"
	}
	m.alert("info", action+" "+row.Ep.Model)
	probeTimeout := time.Duration(m.Cfg.Serve.ProbeTimeoutSeconds) * time.Second
	return func() tea.Msg {
		defer cancel()
		msg := serveLoadMsg{model: row.Ep.Model, url: row.Ep.URL}
		if nativeRulith {
			install, ok := serve.LocateRulith()
			if !ok {
				msg.err = fmt.Errorf("Rulith installation is unavailable")
				return msg
			}
			_, msg.err = serve.SetRulithResident(ctx, &http.Client{Timeout: probeTimeout}, install, row.Ep.URL, row.Asset.ID, load)
			return msg
		}
		if nativeOllama {
			_, msg.err = serve.SetOllamaResident(ctx, &http.Client{}, row.Ep.URL, row.Ep.Model, load)
			return msg
		}
		if !load {
			msg.proc = p
			msg.err = p.Stop()
			msg.stopped = msg.err == nil
			return msg
		}
		if row.Recipe == nil {
			msg.err = fmt.Errorf("no native MLX recipe")
			return msg
		}
		if row.Asset.Runtime != "mlx" {
			if row.Health.Reachable || !serve.Loopback(row.Ep.URL) {
				msg.err = fmt.Errorf("endpoint responds or is external; no duplicate server started")
				return msg
			}
			rec, err := serve.BindModel(*row.Recipe, row.Asset.Path)
			if err != nil {
				msg.err = err
				return msg
			}
			msg.proc, msg.err = serve.StartContext(ctx, rec, filepath.Join(os.TempDir(), "caliper-harness-"+rec.Name+".log"), probeTimeout)
			msg.notice = "server process started; refresh for catalog, residency unconfirmed"
			return msg
		}
		rec, err := serve.PrepareMLX(*row.Recipe, filepath.Join(serve.FindHarnessDir(), "python", "worker", "server.py"), row.Asset.Path)
		if err != nil {
			msg.err = err
			return msg
		}
		if ctx.Err() != nil {
			msg.err = ctx.Err()
			return msg
		}
		msg.proc, msg.err = serve.StartContext(ctx, rec, filepath.Join(os.TempDir(), "caliper-harness-"+rec.Name+".log"), probeTimeout)
		if msg.err != nil {
			return msg
		}
		for {
			_, err = serve.OwnedMLXReady(ctx, &http.Client{Timeout: probeTimeout}, msg.proc)
			if err == nil {
				return msg
			}
			if msg.proc.PID() == 0 {
				msg.err = fmt.Errorf("native MLX exited before readiness: %w", err)
				return msg
			}
			select {
			case <-ctx.Done():
				msg.err = fmt.Errorf("native MLX not ready: %w; last receipt: %v", ctx.Err(), err)
				if stopErr := msg.proc.Stop(); stopErr != nil {
					msg.err = fmt.Errorf("%w; owned cleanup failed: %v", msg.err, stopErr)
				} else {
					msg.stopped = true
				}
				return msg
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
}
func (m *Model) onServeLoad(msg serveLoadMsg) {
	m.Serve.Loading = ""
	m.Serve.cancel = nil
	if msg.proc != nil {
		if m.Serve.Spawned == nil {
			m.Serve.Spawned = map[string]*serve.Proc{}
		}
		if msg.stopped {
			if m.Serve.Spawned[msg.url] == msg.proc {
				delete(m.Serve.Spawned, msg.url)
			}
		} else {
			m.Serve.Spawned[msg.url] = msg.proc
		}
	}
	if msg.err != nil {
		m.alert("err", msg.err.Error())
		return
	}
	if msg.notice != "" {
		m.alert("info", msg.notice)
		return
	}
	m.alert("info", "resident action confirmed for "+msg.model)
}
func recipeModels(recipes []serve.Recipe) []string {
	var out []string
	for _, rec := range recipes {
		for i, a := range rec.Argv {
			if a == "--model" && i+1 < len(rec.Argv) {
				out = append(out, rec.Argv[i+1])
			}
		}
	}
	return out
}

// serveDevice cycles the sidecar backend: auto, cpu, gpu.
func (m *Model) serveDevice() {
	r := m.serveCur()
	if r == nil {
		return
	}
	if !r.Sidecar {
		m.alert("err", "device belongs to the sidecar row")
		return
	}
	f := SettingField{"decision", "device", "decision device", "device"}
	m.cycleField(f)
}

// Model selection is open text; compatible checkpoints and local paths are admitted by the sidecar.
func (m *Model) serveSidecarModel() {
	r := m.serveCur()
	if r == nil || !r.Sidecar {
		m.alert("err", "decision model belongs to the sidecar row")
		return
	}
	m.navigate(ViewSettings, "")
	m.armEdit(SettingField{"decision", "model", "decision model", "text"})
}

// TrackSidecar supplies startup health before the first Settings visit.
func (m *Model) TrackSidecar(p *serve.Proc, h serve.Health) {
	if m.Serve.Spawned == nil {
		m.Serve.Spawned = map[string]*serve.Proc{}
	}
	url := m.Cfg.Decision.Server
	m.Serve.Spawned[url] = p
	m.Serve.Rows = append(m.Serve.Rows, ServeRow{Ep: serve.Endpoint{Kind: serve.KindSidecar, Label: "sidecar", URL: url, Model: m.Cfg.Decision.Model}, Sidecar: true, Have: true, Health: h})
	m.Serve.Rows[len(m.Serve.Rows)-1].Ours = m.decisionOwner() != nil
}

// Ownership needs both a live child and its validated HTTP identity.
func (m *Model) decisionOwner() *serve.Proc { return m.decisionOwnerAt(m.Cfg.Decision.Server) }
func (m *Model) decisionOwnerAt(url string) *serve.Proc {
	p := m.Serve.Spawned[url]
	if !serve.Loopback(url) || p.PID() == 0 {
		return nil
	}
	for _, r := range m.Serve.Rows {
		if r.Sidecar && r.Ep.URL == url && r.Health.Decision != nil && r.Health.Decision.PID == p.PID() {
			return p
		}
	}
	return nil
}
func (m *Model) commitExternalDecision() {
	m.Cfg.Decision = *m.Settings.DecisionDraft
	m.Settings.DecisionDraft = nil
	m.Settings.DecisionNotice = "requested selection saved in memory — pending external activation (ownership unconfirmed)"
	if m.Cfg.Decision.Disabled {
		m.Settings.DecisionNotice = "decision model disabled; selection retained without activation"
		m.alert("info", m.Settings.DecisionNotice)
		return
	}
	if !decision.NativeRuntime(m.Cfg.Decision.Runtime) {
		m.Settings.DecisionNotice = "Chat judge selected for next request; server controls residency; confidence is self-reported"
	}
	m.alert("info", m.Settings.DecisionNotice)
}

type decisionApplied struct {
	seq    int
	url    string
	proc   *serve.Proc
	cfg    config.DecisionConfig
	health decision.Health
	err    error
}

func (m *Model) applyDecisionSelection() tea.Cmd {
	if m.Settings.Applying {
		m.alert("info", "decision activation still unsettled")
		return nil
	}
	if m.Settings.DecisionDraft == nil {
		m.alert("info", "no staged decision selection")
		return nil
	}
	if m.Settings.DecisionDraft.Disabled {
		m.Cfg.Decision = *m.Settings.DecisionDraft
		m.Settings.DecisionDraft = nil
		m.Settings.DecisionNotice = "decision model disabled; selection retained without activation"
		m.alert("info", m.Settings.DecisionNotice)
		return nil
	}
	if !decision.NativeRuntime(m.Settings.DecisionDraft.Runtime) {
		m.Cfg.Decision = *m.Settings.DecisionDraft
		m.Settings.DecisionDraft = nil
		m.syncDerived()
		m.Settings.DecisionNotice = "Chat judge selected; load/unload through its model row · confidence is self-reported · inference unchecked"
		m.alert("info", m.Settings.DecisionNotice)
		return nil
	}
	p := m.decisionOwnerAt(m.Settings.DecisionDraft.Server)
	if p == nil {
		m.commitExternalDecision()
		return nil
	}
	cfg, seq, pid := *m.Settings.DecisionDraft, m.Settings.DecisionSeq, p.PID()
	m.Settings.Applying = true
	m.alert("info", "applying owned decision selection — previous model remains active until admitted")
	return func() tea.Msg {
		a := &decisionApplied{seq: seq, url: cfg.Server, proc: p, cfg: cfg}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
		defer cancel()
		client := decision.New(cfg.Server, cfg.Model, cfg.Device, time.Duration(cfg.TimeoutSeconds)*time.Second).WithInputLimit(cfg.MaxInputTokens)
		h, err := client.Health(ctx)
		if err == nil && (p.PID() != pid || h.PID != pid) {
			err = fmt.Errorf("owned process identity changed — no activation sent")
		}
		if err == nil {
			a.health, err = client.Activate(ctx, decision.SelectionFor(cfg))
		}
		if err == nil && a.health.PID != pid {
			err = fmt.Errorf("activation process identity changed")
		}
		a.err = err
		return serveProbedMsg{application: a}
	}
}

// serveKey drives the status screen: arrows move, letters act on the
// selected row, r refreshes, esc walks back to chat.
func (m Model) serveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.servePageHeight() < 1 {
		m.alert("err", "enlarge terminal to select or act")
		return m, nil, true
	}

	switch msg.Type {
	case tea.KeyUp:
		if len(m.Serve.Rows) > 0 {
			m.Serve.Sel = (m.Serve.Sel - 1 + len(m.Serve.Rows)) % len(m.Serve.Rows)
			m.followServe()
		}
		return m, nil, true
	case tea.KeyDown:
		if len(m.Serve.Rows) > 0 {
			m.Serve.Sel = (m.Serve.Sel + 1) % len(m.Serve.Rows)
			m.followServe()
		}
		return m, nil, true
	case tea.KeyBackspace, tea.KeyDelete:
		if m.Input.Value() != "" {
			return m, nil, false
		}
		m.removeManaged()
		return m, m.probeServe(), true
	case tea.KeyHome, tea.KeyEnd:
		m.Serve.Sel = 0
		if msg.Type == tea.KeyEnd {
			m.Serve.Sel = max(0, len(m.Serve.Rows)-1)
		}
		m.followServe()
		return m, nil, true
	case tea.KeyEnter:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		return m, m.serveEnter(), true
	case tea.KeyRunes:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		switch s := msg.String(); s {
		case "n", "N":
			m.openManagedForm("server")
			return m, nil, true
		case "+":
			m.openManagedForm("model")
			return m, nil, true
		case "e", "E":
			m.openManagedForm("edit")
			return m, nil, true
		case "t", "T":
			return m, m.testManaged(), true
		case "j", "J":
			m.serveUseJudge()
		case "u", "U":
			m.serveUse()
		case "s", "S":
			m.serveStart()
		case "x", "X":
			return m, m.serveUnload(), true
		case "l", "L":
			return m, m.serveLoad(), true
		case "d", "D":
			m.serveDevice()
		case "m", "M":
			m.serveSidecarModel()
		case "a", "A":
			cmd := m.applyDecisionSelection()
			return m, cmd, true
		case "r", "R":
			return m, m.probeServe(), true
		default:
			return m, nil, false
		}
		return m, nil, true
	}
	return m, nil, false
}

// serveEnter runs the row's obvious action: start what's down, use
// what's up; a model row unloads when resident, loads when not;
// the sidecar spells it out (s/d/x).
func (m *Model) serveEnter() tea.Cmd {
	r := m.serveCur()
	if r == nil {
		return nil
	}
	if r.Sidecar && r.Asset != nil {
		m.serveUseJudge()
		return nil
	}
	if r.Sidecar {
		m.alert("info", "sidecar: m edit model · d stage device · a apply · s start · x stop")
		return nil
	}
	if r.IsModel && r.Asset == nil && r.Have && r.Health.Up && r.Ep.Kind != serve.KindOllama && r.Health.Kind != serve.KindOllama {
		m.serveUse()
		return nil
	}
	if r.IsModel && r.Asset == nil && !r.Health.Up {
		m.serveUse()
		return nil
	}
	if r.IsModel {
		if r.Resident {
			return m.serveUnload()
		}
		return m.serveLoad()
	}
	if r.Have && !r.Health.Up {
		m.serveStart()
		return nil
	}
	m.serveUse()
	return nil
}

// This label mirrors serveEnter; it describes a request, never a promised result.
func (m *Model) serveEnterLabel() string {
	r := m.serveCur()
	if r == nil {
		return "act"
	}
	if r.Sidecar && r.Asset != nil {
		return "stage judge"
	}
	if r.Sidecar {
		return "sidecar guidance"
	}
	if r.IsModel && r.Asset == nil && r.Have && r.Health.Up && r.Ep.Kind != serve.KindOllama && r.Health.Kind != serve.KindOllama {
		return "use worker"
	}
	if r.IsModel {
		if r.Resident {
			return "unload"
		}
		return "load"
	}
	if r.Have && !r.Health.Up {
		return "start"
	}
	return "use"
}

// shortURL drops the scheme for dense rows.
func shortURL(raw string) string {
	return strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
}

// serveDot is the health lamp: lit when up, hollow when down.
func serveDot(up bool) string {
	if up {
		return InfoSt.Render("●")
	}
	return ErrSt.Render("○")
}

// serveShown lists a server's models: resident first, else known.
func serveShown(h serve.Health) []string {
	if len(h.Loaded) > 0 {
		return h.Loaded
	}
	return h.Available
}

// serveModelRow renders one model under its server: residency lamp,
// guides, and the in-use marker for the worker model.
func serveModelRow(m Model, i int, r ServeRow, inner int) string {
	mark := "  "
	if i == m.Serve.Sel {
		mark = "❯ "
	}
	branch := "├─"
	if i+1 >= len(m.Serve.Rows) || !m.Serve.Rows[i+1].IsModel {
		branch = "└─"
	}
	state := "saved · availability/residency unknown"
	if r.Health.Up && (r.Health.Kind == serve.KindOllama || r.Ep.Kind == serve.KindOllama) {
		state = "available — l load"
	}
	if r.Asset != nil {
		state = "candidate — " + r.Asset.Reason
		if r.Sidecar {
			state = "j stage judge (" + r.Asset.Runtime + ") — " + r.Asset.Reason
		}
	}
	if r.Resident {
		state = "resident — x unload"
	}
	tags := ""
	if m.Serve.Loading == serveKeyOf(r) {
		tags += " " + WarnSt.Render("[loading/releasing]")
	}
	if sameServeURL(r.Ep.URL, m.Cfg.Model.Server) && (r.Ep.Model == m.Cfg.Model.Name || r.Asset != nil && r.Asset.Path == m.Cfg.Model.Name) && m.Cfg.Model.Name != "" {
		tags += " " + InfoSt.Render("[requested next]")
	}
	display := r.Ep.Model
	if r.Asset != nil {
		display = r.Asset.ID
		if r.Asset.Revision != "" {
			display += "@" + r.Asset.Revision
		}
	}
	dot := Muted.Render("○")
	tone := Muted
	if r.Resident {
		dot = InfoSt.Render("●")
		tone = InfoSt
	}
	if m.Serve.Loading == serveKeyOf(r) {
		dot = WarnSt.Render("◌")
		tone = WarnSt.Bold(false)
	}
	row := fmt.Sprintf("  %s%s %s %s · %s%s", mark, dot, branch, PathSt.Render(truncate(display, 30)), tone.Render(state), tags)
	if i == m.Serve.Sel {
		return selectionRow(row, inner)
	}
	return Body.Render(ansi.Truncate(row, inner, "…"))
}

// serveHints is the full key list, wrapped to fit any frame —
// discoverability never depends on terminal width.
func (m Model) serveHints(inner int) []string {
	text := "n Add server · + Add model · e Edit · ⌫ Remove · t Test · ↑↓ select\nu Coding · j Judge · s start · l load · x unload/stop · r refresh · " + m.escapeHint()
	if r := m.serveCur(); r != nil && r.Sidecar {
		text += "\nd device · m model · a apply Judge"
	}
	var out []string
	for _, line := range Wrap(m.ownedHint(text), inner) {
		out = append(out, Guidance.Render(line))
	}
	return out
}

// renderServe draws mission control: runtime rows, the sidecar row,
// and always-visible wrapped hints.
func (m Model) serveContent(w int) ([]string, int) {
	inner := BoxInner(w)
	var lines []string
	selected := 0
	lines = append(lines, Title.Render("Models & Servers"))
	if !decision.NativeRuntime(m.Cfg.Decision.Runtime) {
		for _, line := range Wrap("Judge: "+m.Cfg.Decision.Model+" · "+m.Cfg.Decision.Runtime+" · self-reported confidence; native input-token cap unconfirmed; server context admission", inner) {
			lines = append(lines, Guidance.Render(line))
		}
	}
	if m.Serve.Loading != "" {
		lines = append(lines, WarnSt.Render("loading/releasing — awaiting native receipt"))
	}
	if len(m.Serve.Rows) == 0 {
		lines = append(lines, Muted.Render("No saved connections · n Add server"))
	}
	section := ""
	for i, r := range m.Serve.Rows {
		want := "RUNTIME"
		if r.Sidecar {
			want = "DECISION SIDECAR"
		}
		if want != section {
			section = want
			lines = append(lines, SectionSt.Render(section))
		}
		if i == m.Serve.Sel {
			selected = len(lines)
		}
		if r.IsModel {
			lines = append(lines, serveModelRow(m, i, r, inner))
			continue
		}
		mark := "  "
		if i == m.Serve.Sel {
			mark = "❯ "
		}
		dot := Muted.Render("○")
		if r.Have {
			dot = serveDot(r.Health.Up)
		}
		url := truncate(shortURL(r.Ep.URL), 34)
		model := r.Ep.Model
		if model == "" {
			model = "—"
		}
		state := "probing…"
		if r.Have {
			if r.Health.Up {
				state = fmt.Sprintf("up %dms", r.Health.LatencyMs)
				if shown := serveShown(r.Health); len(shown) > 0 {
					state += " · " + truncate(strings.Join(shown, ", "), 40)
				}
				if r.Sidecar {
					h := r.Health.Decision
					if h == nil || !h.IsReady() {
						state += " · not ready"
					} else {
						model = h.Active.Model
						state += " · " + h.Active.Runtime + " " + h.Active.NativeDevice
						if !h.Matches(decision.SelectionFor(m.Cfg.Decision)) {
							state += " · requested selection pending"
						}
					}
				}
			} else {
				state = "down"
				if r.Health.Err != "" {
					state += ": " + truncate(r.Health.Err, 60)
				}
			}
		}
		if r.Recipe != nil && !r.Health.Up {
			executable, err := r.Recipe.Executable()
			if err != nil {
				state += " · launcher unavailable: " + err.Error()
			} else {
				state += " · installed: " + executable
			}
		}
		if r.Recipe != nil && r.Ep.Label == r.Recipe.Name {
			state = "launch recipe; endpoint " + state
		}
		tags := ""
		if r.Worker {
			tags += " " + InfoSt.Render("[Coding]")
		}
		if !m.Cfg.Decision.Disabled && sameServeURL(r.Ep.URL, m.Cfg.Decision.Server) {
			tags += " " + InfoSt.Render("[Judge]")
		}
		if r.Ours {
			tags += " " + WarnSt.Render("[ours]")
		}
		if m.Serve.Loading == serveKeyOf(r) {
			tags += " " + WarnSt.Render("[loading/releasing]")
		}
		if r.Have && !r.Health.Up && r.Recipe != nil && !(r.Sidecar && r.Health.Reachable) {
			tags += " " + Muted.Render("[s start]")
		}
		row := fmt.Sprintf("%s%s %-7s %-18s %s · %s%s", mark, dot, r.Ep.Label, truncate(model, 18), state, url, tags)
		if inner < 95 {
			row = fmt.Sprintf("%s%s %-7s %-20s %s%s", mark, dot, r.Ep.Label, truncate(model, 20), state, tags)
		}
		tone := Muted
		if r.Have {
			if r.Health.Up {
				tone = InfoSt
			} else {
				tone = ErrSt.Bold(false)
			}
		}
		if strings.Contains(state, "not ready") || strings.Contains(state, "pending") {
			tone = WarnSt.Bold(false)
		}
		row = strings.Replace(row, state, tone.Render(state), 1)
		if i == m.Serve.Sel {
			lines = append(lines, selectionRow(row, inner))
		} else {
			lines = append(lines, Body.Render(ansi.Truncate(row, inner, "…")))
		}
	}
	if r := m.serveCur(); r != nil {
		identity := Body.Render("Selected runtime: "+r.Ep.Label) + PathSt.Render(" · "+r.Ep.URL+" · "+r.Ep.Model)
		if r.Health.Err != "" {
			identity += ErrSt.Bold(false).Render(" · " + r.Health.Err)
		}
		lines = append(lines, identity)
	}
	if len(m.Serve.Installed) > 0 {
		lines = append(lines, "Installed server launchers (not running proof): "+strings.Join(m.Serve.Installed, "; "), "Configure endpoint/launch command in serve.recipes; installation does not imply a listening URL.")
	}
	if len(m.Serve.Warnings) > 0 {
		lines = append(lines, WarnSt.Render(fmt.Sprintf("inventory warnings (%d): %s", len(m.Serve.Warnings), strings.Join(m.Serve.Warnings, "; "))))
	}
	if r := m.serveCur(); r != nil && r.Asset != nil {
		reason := r.Asset.Reason
		if r.Resident {
			reason = "native resident/generation readiness confirmed"
		}
		for _, line := range Wrap(r.Asset.Path+" · "+reason, inner) {
			lines = append(lines, Body.Render(line))
		}
		lines = append(lines, "Asset identity: "+r.Asset.ID+"@"+r.Asset.Revision)
	}
	if r := m.serveCur(); r != nil && r.Sidecar && r.Health.Decision != nil {
		h := r.Health.Decision
		if h.Active != nil {
			a := h.Active
			revision := "unreported"
			if a.Revision != nil {
				revision = *a.Revision
			}
			for _, l := range Wrap(fmt.Sprintf("active: %s · %s/%s · %d parameters · %s · source %s · path %s · revision %s · trust %t", a.Model, a.Runtime, a.NativeDevice, a.Parameters, a.Architecture, a.Source, a.Path, revision, a.Selection.TrustRemoteCode), inner) {
				lines = append(lines, Body.Render(l))
			}
		}
		if h.Error != nil {
			lines = append(lines, ErrSt.Render("last activation: "+*h.Error))
		}
		for _, l := range Wrap(fmt.Sprintf("requested: %s · %s/%s · path %s · revision %s · trust %t", m.Cfg.Decision.Model, m.Cfg.Decision.Runtime, m.Cfg.Decision.Device, m.Cfg.Decision.Path, m.Cfg.Decision.Revision, m.Cfg.Decision.TrustRemoteCode), inner) {
			lines = append(lines, Muted.Render(l))
		}
	}
	lines = append(lines, m.renderMemory())
	if m.Serve.MemErr != "" {
		lines = append(lines, ErrSt.Bold(false).Render("Memory error: "+m.Serve.MemErr))
	}
	lines = append(lines, SectionSt.Render("SESSION"), "Session identity: "+m.St.Model+" · "+m.St.Dir+" · "+m.St.Branch+" · "+m.St.PlanTitle+" · "+strings.Join(m.St.PlanApproaches, " · "))
	for i, tab := range m.Tabs {
		lines = append(lines, fmt.Sprintf("Tab %d/%d (active %t): ", i+1, len(m.Tabs), i == m.Active)+tab.Title+" · "+string(tab.at.view))
	}
	for _, a := range m.Alerts {
		lines = append(lines, "alert "+a.Kind+": "+a.Text)
	}
	return Wrap(strings.Join(lines, "\n"), inner), selected
}

func (m Model) servePageHeight() int { return viewHeight(m) - 3 - len(m.serveHints(BoxInner(m.Width))) }
func (m *Model) followServe()        { _, row := m.serveContent(m.Width); m.Serve.Scroll = row }

func (m Model) renderServe(w, vh int) string {
	if m.Setup.Add != nil {
		return m.renderConnection(w, vh)
	}
	inner := BoxInner(w)
	lines, _ := m.serveContent(w)
	hints := m.serveHints(inner)
	page := vh - 3 - len(hints)
	if page < 1 {
		return Box("Models & Servers", ansi.Truncate("enlarge terminal to select or act", inner, "…"), w)
	}
	start := min(max(0, m.Serve.Scroll), max(0, len(lines)-page))
	end := min(len(lines), start+page)
	pinned := fmt.Sprintf("selected %d/%d · no rows", 0, len(m.Serve.Rows))
	if r := m.serveCur(); r != nil {
		name := r.Ep.Model
		if name == "" {
			name = r.Ep.Label
		}
		pinned = fmt.Sprintf("selected %d/%d · ", m.Serve.Sel+1, len(m.Serve.Rows)) + PathSt.Render(name) + " · Enter " + m.serveEnterLabel()
	}
	body := []string{ansi.Truncate(pinned, inner, "…")}
	body = append(body, lines[start:end]...)
	for len(body) < page+1 {
		body = append(body, "")
	}
	body = append(body, hints...)
	return Box(fmt.Sprintf("Models & Servers · %d-%d/%d", start+1, end, len(lines)), strings.Join(body, "\n"), w)
}
