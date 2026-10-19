// Command caliper-harness is the Caliper Harness agent runtime.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"specific-harness/internal/prompts"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/hw"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/run"
	"specific-harness/internal/serve"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tools/files"
	"specific-harness/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.1"

func main() {
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "tui")
	}
	command, args, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		usage()
		os.Exit(2)
	}
	if args.help {
		printCLIHelp(os.Stdout, command)
		return
	}
	switch command {
	case "version":
		fmt.Println(version)
	case "config":
		runConfig(args)
	case "chat":
		runChatParsed(args)
	case "hw":
		runHW()
	case "tool":
		runTool(args)
	case "run":
		runAgentParsed(args)
	case "graph":
		runGraph(args)
	case "sessions":
		runSessions(args)
	case "judgments":
		runJudgments(args)
	case "template":
		runTemplate(args)
	case "launch":
		runLaunch(args)
	case "retire":
		runRetire(args)
	case "list":
		runList(args)
	case "decision":
		runDecision(args)
	case "tui":
		runTUI(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// runConfig prints the effective configuration: file values over defaults.
func runConfig(args cliArgs) {
	path := args.string("config")
	cfg, usedFile, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !usedFile {
		fmt.Fprintf(os.Stderr, "note: no config file at %s, using built-in defaults\n", path)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot render config: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(out))
}

// runChat sends one prompt to the model server and prints the reply.
// Flags: --config path, --server URL, --model ID, --prompt TEXT, --no-stream.
func runChatParsed(args cliArgs) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfgPath := args.string("config")
	server, model, prompt := args.string("server"), args.string("model"), args.string("prompt")
	stream := !args.bool("no-stream")
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
	if server == "" {
		server = cfg.Model.Server
	}
	if model == "" {
		model = cfg.Model.Name
	}
	if model == "" {
		fmt.Fprintln(os.Stderr, "error: no model set — pass --model ID or set model.name in config")
		if ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: no prompt — pass --prompt TEXT")
		if ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
	msgs := []llm.Message{{Role: "user", Content: prompt}}
	params := run.ModelParams(cfg.Params)
	if notice := llm.RequestedControlNotice(cfg.Model.Runtime, params); notice != "" {
		fmt.Fprintln(os.Stderr, "note: "+notice)
	}
	if stream {
		reply, served, _, err := llm.New(server, model).WithPlainTimeout(time.Duration(cfg.Model.PlainRequestTimeoutSeconds)*time.Second).WithRuntime(cfg.Model.Runtime).WithParams(params).ChatStream(ctx, msgs,
			func(d string) { fmt.Print(d) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			if ctx.Err() != nil {
				os.Exit(130)
			}
			os.Exit(1)
		}
		fmt.Printf("\n[served by %s]\n", served)
		_ = reply
		return
	}
	reply, served, _, err := llm.New(server, model).WithPlainTimeout(time.Duration(cfg.Model.PlainRequestTimeoutSeconds)*time.Second).WithRuntime(cfg.Model.Runtime).WithParams(params).Chat(ctx, msgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
	fmt.Printf("%s\n[served by %s]\n", reply, served)
}

// runHW prints the detected hardware profile and derived limits.
func runHW() {
	p, err := hw.Detect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("profile: %s\nlimits:  %s\n", p, hw.LimitsFor(p))
}

// runTUI launches the interactive terminal app.
func runTUI(args cliArgs) {
	startupCtx, stopStartup := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopStartup()
	boot := newStartup(version, args.string("config"))
	boot.begin("Configuration", "Reading project settings")
	cfg, used, err := config.Load(args.string("config"))
	if err != nil {
		boot.finish("error", err.Error())
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if used {
		boot.finish("ok", args.string("config"))
	} else {
		boot.finish("info", "First use · review settings in Setup")
	}
	boot.begin("Instructions", "Validating the prompt catalog")
	pc, err := cfg.LoadPrompts()
	if err != nil {
		boot.finish("error", err.Error())
		fatal(err)
	}
	boot.finish("ok", "Prompt catalog validated")
	if !interactiveTerminal() {
		fatal(fmt.Errorf("Caliper Harness needs an interactive terminal; use caliper-harness help for headless commands"))
	}
	if !boot.dependencies() {
		os.Exit(1)
	}
	m := tui.New(cfg, version)
	m.CfgPath = args.string("config")
	if id := args.string("resume"); id != "" {
		continuation, notice, err := tui.BaselineContinuation(cfg.Sessions.Path, id, m.St.CtxWindow, cfg.Model.Server+"|"+cfg.Model.Name+"|"+cfg.Model.Runtime+"|"+llm.ToolProtocol(cfg.Model.ToolProtocol), pc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		m.Chat.Context = continuation
		m.Chat.SessionIDs = []string{id}
		m.AddSysLine(fmt.Sprintf("resumed %s — %s; prompt size unknown", id, notice))
		m.SyncTabChat()
	}
	sweepSessions(cfg)
	var sidecar *serve.Proc
	if cfg.Model.Name != "" {
		boot.begin("Decision service", "Checking the configured service")
		sidecar = ensureSidecarContext(startupCtx, cfg, boot.detail)
		state, message := "info", "Preparation complete · inspect readiness in Status"
		if strings.Contains(boot.message, "unavailable") || strings.Contains(boot.message, "not confirmed") {
			state, message = "warn", boot.message
		}
		boot.finish(state, message)
	} else {
		boot.line("info", "Model selection", "Choose an existing model in Setup")
	}
	if sidecar != nil {
		if startupCtx.Err() == nil {
			m.TrackSidecar(sidecar, serve.ProbeContext(startupCtx, &http.Client{Timeout: time.Duration(cfg.Serve.ProbeTimeoutSeconds) * time.Second}, serve.Endpoint{Kind: serve.KindSidecar, URL: cfg.Decision.Server}))
		}
	}
	if startupCtx.Err() != nil {
		if sidecar != nil {
			_ = sidecar.Stop()
		}
		return
	}
	stopStartup()
	if cfg.Model.Name == "" {
		m.OpenSetup()
	}
	boot.line("ok", "Workspace", "Opening Caliper Harness")
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.Shared.Prog = p
	final, runErr := p.Run()
	cleanup := m
	if state, ok := final.(tui.Model); ok {
		cleanup = state
	}
	if err := cleanup.CloseOwnedServing(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		os.Exit(1)
	}
}

// sidecarWait caps the open-time health wait; a slow loader keeps
// coming up under the status screen's watch.
const sidecarWait = 15 * time.Second

// ensureSidecar auto-starts the decision sidecar when it is ours to
// manage (loopback, down, recipe available). Anything else — remote,
// already up, no recipe — is left alone. The caller stops the return.
func ensureSidecar(cfg config.Config) *serve.Proc {
	return ensureSidecarWith(cfg, func(text string) { fmt.Fprintln(os.Stderr, text) })
}

func ensureSidecarWith(cfg config.Config, note func(string)) *serve.Proc {
	return ensureSidecarContext(context.Background(), cfg, note)
}

func ensureSidecarContext(ctx context.Context, cfg config.Config, note func(string)) *serve.Proc {
	if cfg.Decision.Runtime != "" && cfg.Decision.Runtime != "laya" && cfg.Decision.Runtime != "system_one" {
		note("unsupported decision runtime: select laya or system_one")
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	srv := cfg.Decision.Server
	if !serve.Loopback(srv) {
		return nil
	}
	client := &http.Client{Timeout: time.Duration(cfg.Serve.ProbeTimeoutSeconds) * time.Second}
	if serve.ProbeContext(ctx, client, serve.Endpoint{Kind: serve.KindSidecar, URL: srv}).Reachable {
		return nil
	}
	recipe, ok := serve.SidecarRecipe(serve.FindHarnessDir(), srv, cfg.Decision.Model, decision.SelectionFor(cfg.Decision))
	custom := false
	for _, r := range cfg.Serve.Recipes {
		if r.Kind == serve.KindSidecar && r.URL == srv {
			recipe = serve.Recipe{Name: r.Name, Kind: r.Kind, URL: r.URL, Argv: r.Argv, Dir: r.Dir}
			ok, custom = true, true
		}
	}
	if !ok {
		note("Decision service unavailable · no start recipe found")
		return nil
	}
	if !custom {
		pc, err := cfg.LoadPrompts()
		if err == nil {
			recipe, err = serve.SidecarPrompts(recipe, pc)
		}
		if err != nil {
			note(fmt.Sprintf("Decision service unavailable: %v", err))
			return nil
		}
	}
	proc, err := serve.StartContext(ctx, recipe, os.TempDir()+"/caliper-harness-sidecar.log", time.Duration(cfg.Serve.ProbeTimeoutSeconds)*time.Second)
	if err != nil {
		note(fmt.Sprintf("Decision service unavailable: %v", err))
		return nil
	}
	note("Starting the configured decision service")
	deadline := time.Now().Add(sidecarWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			_ = proc.Stop()
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		h := serve.ProbeContext(ctx, client, serve.Endpoint{Kind: serve.KindSidecar, URL: srv})
		if h.Decision != nil && h.Decision.PID == proc.PID() && h.Decision.Matches(decision.SelectionFor(cfg.Decision)) {
			note("Decision service confirmed the selected model")
			return proc
		}
	}
	note("Decision selection not confirmed · inspect Status")
	return proc
}

// runTool implements schema-on-demand: `tool` lists, `tool <name>`
// prints purpose plus fillable slots.
func runTool(args cliArgs) {
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	pc, err := cfg.LoadPrompts()
	if err != nil {
		fatal(err)
	}
	cfg.PromptCatalog = pc
	reg := tool.NewRegistry(pc)
	tools.RegisterAll(reg, cfg, os.Stdin, os.Stderr, nil)
	rest := args.positionals
	if len(rest) == 0 {
		names := reg.Names()
		if len(names) == 0 {
			fmt.Println("no tools registered yet")
			return
		}
		fmt.Println(strings.Join(names, "\n"))
		return
	}
	d, ok := reg.Lookup(rest[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown tool %q", rest[0])
		if names := reg.Names(); len(names) > 0 {
			fmt.Fprintf(os.Stderr, " — available: %s", strings.Join(names, ", "))
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	fmt.Print(d.Schema())
}

// runAgent executes one headless task: --prompt TEXT [--server URL]
// [--model ID] [--max-steps N] [--config path].
func runAgentParsed(args cliArgs) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, model, prompt, toolView := args.string("server"), args.string("model"), args.string("prompt"), args.string("tools")
	maxSteps := args.integer("max-steps")
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if server == "" {
		server = cfg.Model.Server
	}
	if model == "" {
		model = cfg.Model.Name
	}
	if model == "" {
		fmt.Fprintln(os.Stderr, "error: no model set — pass --model ID or set model.name in config")
		os.Exit(1)
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: no prompt — pass --prompt TEXT")
		os.Exit(1)
	}
	if args.string("max-steps") == "" {
		maxSteps = cfg.Agent.MaxSteps
	}
	sweepSessions(cfg)
	// The store opens even though session events don't touch Kuzu: the
	// mutation path runs through the GraphQL executor, whose resolvers
	// need the store. One open per run.
	s, err := store.OpenWithBufferPool(cfg.Store.Path, uint64(cfg.Store.BufferPoolBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	input, err := ask.NewFileInput(os.Stdin)
	if err != nil {
		s.Close()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer input.Close()
	id := session.NewID()
	fmt.Fprintf(os.Stderr, "session: %s\n", id)
	window := resolveWindow(cfg)
	sessLog := graph.NewSessionLog(graph.New(s, version, cfg.Sessions.Path))
	r, notes, err := run.New(run.Deps{
		Tools: toolView, Cfg: cfg, Server: server, Model: model, MaxSteps: maxSteps, Window: window,
		Store: s, Session: sessLog, SessionID: id,
		AskYesNo: func(ctx context.Context, prompt string) bool { return auth.Ask(ctx, input, os.Stderr, prompt) },
		In:       input, Out: os.Stderr, ReplyOut: os.Stdout,
	}, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		s.Close()
		input.Close()
		stop()
		os.Exit(1)
	}
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, n)
	}
	_, _, runErr := r.Run(ctx, prompt)
	if r.UsageUnknown {
		fmt.Fprintf(os.Stderr, "warning: model server does not report token usage — context features disabled\n")
	} else {
		fmt.Fprintf(os.Stderr, "tokens in=%d out=%d\n", r.UsedIn, r.UsedOut)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
	}
	s.Close()
	input.Close()
	stop()
	if code := r.Outcome.ExitCode(); code != 0 {
		fmt.Fprintf(os.Stderr, "run %s: %s\n", r.Outcome.Status, r.Outcome.Reason)
		os.Exit(code)
	}
}

// runGraph executes one GraphQL operation in-process against the store:
// --query TEXT [--db path] [--sessions-dir path] [--config path].
func runGraph(args cliArgs) {
	db, sessionsDir, query := args.string("db"), args.string("sessions-dir"), args.string("query")
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if db == "" {
		db = cfg.Store.Path
	}
	if sessionsDir == "" {
		sessionsDir = cfg.Sessions.Path
	}
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: no query — pass --query TEXT")
		os.Exit(1)
	}
	pc, err := cfg.LoadPrompts()
	if err != nil {
		fatal(err)
	}
	s, err := store.OpenWithBufferPool(db, uint64(cfg.Store.BufferPoolBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dj := decision.New(cfg.Decision.Server, cfg.Decision.Model, cfg.Decision.Device, time.Duration(cfg.Decision.TimeoutSeconds)*time.Second).WithSelection(decision.SelectionFor(cfg.Decision)).WithPrompts(pc).WithInputLimit(cfg.Decision.MaxInputTokens)
	api := graph.NewWithReady(s, version, sessionsDir, func(ctx context.Context, id string) (*plan.Approach, error) {
		input, err := ask.NewFileInput(os.Stdin)
		if err != nil {
			return nil, err
		}
		defer input.Close()
		d := launch.Deps{Prompts: pc, Store: s, Wf: cfg.Workflows, Judge: dj.Judge, Threshold: cfg.Decision.RiskThreshold, CoherenceMaxChars: cfg.Decision.CoherenceMaxChars, In: input, Out: os.Stderr}
		return launch.Ready(ctx, d, id)
	})
	w, err := files.Capture(cfg.Tools.Bash, pc)
	if err == nil {
		api, err = api.Scoped(w.Path, "", false)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		s.Close()
		os.Exit(1)
	}
	out, err := api.ExecBounded(ctx, query, "", nil, cfg.Tools.GraphQL)
	if err == nil {
		var result graph.Result
		if e := json.Unmarshal(out, &result); e != nil {
			err = e
		} else {
			out = result.Data
		}
	} else if len(out) > 0 {
		fmt.Fprintln(os.Stdout, string(out))
	}
	s.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func sweepSessions(cfg config.Config) {
	deleted, err := session.Sweep(cfg.Sessions.Path, cfg.Retention.SessionMonths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: session sweep failed: %v\n", err)
	} else if len(deleted) > 0 {
		fmt.Fprintf(os.Stderr, "note: swept %d old session(s)\n", len(deleted))
	}
}

// runSessions lists past runs: `sessions` prints ids, `sessions <id>`
// prints that run's events. Flags: --sessions-dir path, --config path.
func runSessions(args cliArgs) {
	dir, rest := args.string("sessions-dir"), args.positionals
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if dir == "" {
		dir = cfg.Sessions.Path
	}
	if len(rest) == 0 {
		ids, err := session.List(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(ids) == 0 {
			fmt.Println("no sessions yet")
			return
		}
		fmt.Println(strings.Join(ids, "\n"))
		return
	}
	events, err := session.ReadUsed(dir, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, e := range events {
		fmt.Printf("%s %s %s\n", e.Time.Format(time.RFC3339), e.Kind, e.Text)
	}
}

// runJudgments summarizes one run's verdicts: `judgments <id>` prints
// counts by source and resolution plus a confidence histogram over the
// configured buckets. Flags: --sessions-dir path, --config path.
func runJudgments(args cliArgs) {
	dir, rest := args.string("sessions-dir"), args.positionals
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if dir == "" {
		dir = cfg.Sessions.Path
	}
	events, err := session.ReadUsed(dir, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	var js []decision.Judgment
	skipped := 0
	for _, e := range events {
		if e.Kind != decision.KindJudgment {
			continue
		}
		j, ok := decision.ParseJudgment(e.Text)
		if !ok {
			skipped++
			continue
		}
		js = append(js, j)
	}
	if len(js) == 0 {
		fmt.Printf("no judgments in session %s\n", rest[0])
		return
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "warning: skipped %d malformed judgment line(s)\n", skipped)
	}
	fmt.Print(decision.Summarize(js, cfg.Decision.ConfidenceBuckets))
}

// runTemplate renders one scaffold: `template <name> [--title TEXT]`
// [--templates-dir path] [--config path]. Name is plan, approach, or task.
func runTemplate(args cliArgs) {
	dir, title, rest := args.string("templates-dir"), args.string("title"), args.positionals
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	var pc *prompts.Catalog
	if dir == "" {
		pc, err = cfg.LoadPrompts()
	} else {
		pc, err = prompts.Legacy(dir)
	}
	if err != nil {
		fatal(err)
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "error: no template — pass plan, approach, or task")
		os.Exit(1)
	}
	if title == "" {
		title = "untitled"
	}
	out, err := pc.Render("templates."+rest[0], map[string]string{"Title": title})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// resolveWindow returns the effective context window: config when set,
// hardware autodetect otherwise (unlimited with a warning if that fails).
func resolveWindow(cfg config.Config) int {
	w, err := hw.ResolveWindow(cfg.Context.Window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: hardware detect failed (%v) — running with no context cap\n", err)
	}
	return w
}

// runLaunch fans out one approach deterministically: `launch <approach-id>`
// [--server URL] [--model ID] [--max-steps N] [--config path].
func runLaunch(args cliArgs) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, model, rest := args.string("server"), args.string("model"), args.positionals
	maxSteps := args.integer("max-steps")
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "error: no approach — pass `launch <approach-id>`")
		os.Exit(1)
	}
	if server == "" {
		server = cfg.Model.Server
	}
	if model == "" {
		model = cfg.Model.Name
	}
	if model == "" {
		fmt.Fprintln(os.Stderr, "error: no model set — pass --model ID or set model.name in config")
		os.Exit(1)
	}
	if args.string("max-steps") == "" {
		maxSteps = cfg.Agent.MaxSteps
	}
	s, err := store.OpenWithBufferPool(cfg.Store.Path, uint64(cfg.Store.BufferPoolBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	input, err := ask.NewFileInput(os.Stdin)
	if err != nil {
		s.Close()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer input.Close()
	if notice := llm.RequestedControlNotice(cfg.Model.Runtime, run.ModelParams(cfg.Params)); notice != "" {
		fmt.Fprintln(os.Stderr, "note: "+notice)
	}
	deps, setupErr := run.LaunchDeps(cfg, s, server, model, maxSteps, resolveWindow(cfg), input, os.Stderr)
	if setupErr != nil {
		s.Close()
		input.Close()
		fmt.Fprintln(os.Stderr, setupErr)
		os.Exit(1)
	}
	if deps.BehaviorNote != "" {
		fmt.Fprintln(os.Stderr, deps.BehaviorNote)
	}
	def := launch.New(deps)
	res, err := def.Execute(ctx, tool.Args{"approach": rest[0]})
	if res.Text != "" {
		fmt.Println(res.Text)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	s.Close()
	input.Close()
	cancelled := ctx.Err() != nil
	stop()
	if cancelled {
		os.Exit(130)
	}
	if res.Workflow != nil {
		if code := res.Workflow.Outcome.ExitCode(); code != 0 {
			os.Exit(code)
		}
	}
	if err != nil || res.Failed {
		os.Exit(1)
	}
}

func usage() { printCLIHelp(os.Stderr, "") }

// runDecision sends one judgment to the decision sidecar and prints the
// verdict. Flags fall back to the decision: config section.
func runDecision(args cliArgs) {
	ctxText, instructions, server, device, options := args.string("context"), args.string("instructions"), args.string("server"), args.string("device"), args.string("options")
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fatal(err)
	}
	if server == "" {
		server = cfg.Decision.Server
	}
	if device == "" {
		device = cfg.Decision.Device
	}
	opts := strings.Split(options, ",")
	for i := range opts {
		opts[i] = strings.TrimSpace(opts[i])
	}
	selection := decision.SelectionFor(cfg.Decision)
	selection.Device = device
	pc, err := cfg.LoadPrompts()
	if err != nil {
		fatal(err)
	}
	c := decision.New(server, cfg.Decision.Model, device, time.Duration(cfg.Decision.TimeoutSeconds)*time.Second).WithSelection(selection).WithPrompts(pc).WithInputLimit(cfg.Decision.MaxInputTokens)
	v, err := c.Judge(context.Background(), decision.Request{Context: ctxText, Instructions: instructions, Options: opts})
	if err != nil {
		fatal(err)
	}
	source := ""
	if v.ConfidenceSource != "" {
		source = "; " + v.ConfidenceSource
	}
	fmt.Printf("pick: %s (confidence %.2f%s, model %s, device %s)\n", v.Option, v.Confidence, source, v.Model, v.Device)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// runRetire flips one plan or approach to retired. The id resolves
// plan-first, then approach; retirement is legal only from complete.
func runRetire(args cliArgs) {
	id := args.positionals[0]
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fatal(err)
	}
	if id == "" {
		fatal(errors.New("no id — pass `retire <id>`"))
	}
	s, err := store.OpenWithBufferPool(cfg.Store.Path, uint64(cfg.Store.BufferPoolBytes))
	if err != nil {
		fatal(err)
	}
	defer s.Close()
	if p, err := plan.TransitionPlan(s, id, plan.PlanRetired); err == nil {
		fmt.Printf("retired plan %s\n", p.ID)
		return
	} else if !errors.Is(err, plan.ErrNotFound) {
		fatal(err)
	}
	if a, err := plan.TransitionApproach(s, id, plan.ApproachRetired); err == nil {
		fmt.Printf("retired approach %s\n", a.ID)
		return
	} else if !errors.Is(err, plan.ErrNotFound) {
		fatal(err)
	}
	fatal(fmt.Errorf("no plan or approach %q", id))
}

// runList prints plans or one plan's approaches. Retired nodes stay
// hidden unless --retired asks for the archive bucket.
func runList(args cliArgs) {
	retired, rest := args.bool("retired"), args.positionals
	cfg, _, err := config.Load(args.string("config"))
	if err != nil {
		fatal(err)
	}
	s, err := store.OpenWithBufferPool(cfg.Store.Path, uint64(cfg.Store.BufferPoolBytes))
	if err != nil {
		fatal(err)
	}
	defer s.Close()
	type row struct{ id, state, title string }
	var rows []row
	empty := ""
	switch rest[0] {
	case "plans":
		if retired {
			ps, err := plan.RetiredPlans(s)
			if err != nil {
				fatal(err)
			}
			for _, p := range ps {
				rows = append(rows, row{p.ID, p.State, p.Title})
			}
			empty = "no retired plans"
		} else {
			ps, err := plan.ActivePlans(s)
			if err != nil {
				fatal(err)
			}
			for _, p := range ps {
				rows = append(rows, row{p.ID, p.State, p.Title})
			}
			empty = "no active plans"
		}
	case "approaches":
		if len(rest) < 2 {
			fatal(errors.New("no plan — pass `list approaches <plan-id>`"))
		}
		if _, err := plan.GetPlan(s, rest[1]); err != nil {
			fatal(err)
		}
		if retired {
			as, err := plan.RetiredPlanApproaches(s, rest[1])
			if err != nil {
				fatal(err)
			}
			for _, a := range as {
				rows = append(rows, row{a.ID, a.State, a.Title})
			}
			empty = "no retired approaches"
		} else {
			as, err := plan.ActivePlanApproaches(s, rest[1])
			if err != nil {
				fatal(err)
			}
			for _, a := range as {
				rows = append(rows, row{a.ID, a.State, a.Title})
			}
			empty = "no active approaches"
		}
	default:
		fatal(fmt.Errorf("cannot list %q — pass `list plans` or `list approaches <plan-id>`", rest[0]))
	}
	if len(rows) == 0 {
		fmt.Println(empty)
		return
	}
	for _, r := range rows {
		fmt.Printf("%s %s %s\n", r.id, r.state, r.title)
	}
}

func cliArgsOrExit(command string, args []string) cliArgs {
	name, parsed, err := parseCLI(append([]string{command}, args...))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if parsed.help {
		printCLIHelp(os.Stdout, name)
		os.Exit(0)
	}
	return parsed
}

func runAgent(args []string) { runAgentParsed(cliArgsOrExit("run", args)) }
func runChat(args []string)  { runChatParsed(cliArgsOrExit("chat", args)) }
