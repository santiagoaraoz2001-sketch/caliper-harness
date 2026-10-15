package main

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIProcess(t *testing.T) {
	if os.Getenv("HARNESS_CLI_PROCESS") != "1" {
		return
	}
	os.Args = append([]string{os.Args[0]}, strings.Split(os.Getenv("HARNESS_CLI_ARGV"), "\x1f")...)
	main()
	os.Exit(0)
}

func cliProcess(t *testing.T, argv ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	// Old TUI/decision/help handlers must fail loading this before runtime actions.
	if err := os.WriteFile(filepath.Join(dir, "specific-harness.yaml"), []byte("unknown_fixture_key: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HARNESS_CLI_PROCESS=1", "HARNESS_CLI_ARGV="+strings.Join(argv, "\x1f"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := append(stdout.Bytes(), stderr.Bytes()...)
	if err == nil && (argv[0] == "help" || argv[0] == "-h" || argv[0] == "--help" || argv[len(argv)-1] == "--help") && stderr.Len() != 0 {
		t.Fatalf("successful help wrote stderr: %s", stderr.String())
	}
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"specific-harness.yaml"}) {
		t.Fatalf("parser/help created files: %v", names)
	}
	return string(out), code
}

func TestCLIHelpBeforeEffects(t *testing.T) {
	cases := [][]string{{"--help"}, {"-h"}, {"help"}}
	for _, name := range []string{"version", "config", "chat", "hw", "tool", "run", "graph", "sessions", "judgments", "template", "launch", "retire", "list", "decision", "tui"} {
		cases = append(cases, []string{name, "--help"}, []string{"help", name})
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, "_"), func(t *testing.T) {
			out, code := cliProcess(t, argv...)
			if code != 0 || !strings.Contains(out, "usage: caliper-harness") {
				t.Fatalf("help exit%d: %s", code, out)
			}
			if strings.Contains(out, "unknown_fixture_key") {
				t.Fatalf("help loaded config: %s", out)
			}
		})
	}
}

func TestCaliperConfigNamingPreservesExistingProjects(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	legacy := []byte("model:\n  name: retained-legacy-model\n")
	if err := os.WriteFile(config.LegacyPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	_, args, err := parseCLI([]string{"tui"})
	if err != nil || args.string("config") != config.LegacyPath {
		t.Fatalf("existing project config lost: %+v %v", args, err)
	}
	cfg, used, err := config.Load(args.string("config"))
	if err != nil || !used || cfg.Model.Name != "retained-legacy-model" {
		t.Fatalf("legacy config not loaded: %v %v", used, err)
	}
	if err := os.WriteFile(config.DefaultPath, []byte("model:\n  name: caliper-model\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, args, err = parseCLI([]string{"tui"})
	if err != nil || args.string("config") != config.DefaultPath {
		t.Fatal("new public config did not take precedence")
	}
	_, args, err = parseCLI([]string{"tui", "--config", config.LegacyPath})
	if err != nil || args.string("config") != config.LegacyPath {
		t.Fatal("explicit legacy config was overridden")
	}
	_, args, err = parseCLI([]string{"config", "--config", "missing-explicit.yaml"})
	if err != nil || args.string("config") != "missing-explicit.yaml" {
		t.Fatal("explicit missing path was silently replaced")
	}
	_, used, err = config.Load(args.string("config"))
	if err != nil || used {
		t.Fatal("explicit missing path loaded a different config")
	}
	var help bytes.Buffer
	printCLIHelp(&help, "tui")
	if !strings.Contains(help.String(), "default caliper-harness.yaml") || strings.Contains(help.String(), "specific-harness") {
		t.Fatal("help branding/default depends on legacy working directory")
	}
	if err := os.WriteFile(config.DefaultPath, []byte("unknown_caliper_key: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, args, _ = parseCLI([]string{"tui"})
	if _, _, err := config.Load(args.string("config")); err == nil {
		t.Fatal("invalid new config silently fell back to valid legacy config")
	}
	got, err := os.ReadFile(config.LegacyPath)
	if err != nil || !bytes.Equal(got, legacy) {
		t.Fatal("branding migrated or rewrote existing config")
	}
}

func TestCLISyntaxBeforeConfig(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "--bogus"}, "--bogus"},
		{[]string{"config", "--config"}, "--config"},
		{[]string{"run", "--max-steps", "hot"}, "--max-steps"},
		{[]string{"run", "--max-steps", "3junk"}, "--max-steps"},
		{[]string{"run", "--max-steps", "999999999999999999999999999"}, "--max-steps"},
		{[]string{"run", "--max-steps=-1"}, "--max-steps"},
		{[]string{"version", "extra"}, "arguments"},
		{[]string{"version", "extra", "--help"}, "arguments"},
		{[]string{"tool", "bash", "extra"}, "arguments"},
		{[]string{"list", "plans", "extra"}, "arguments"},
		{[]string{"list", "approaches"}, "arguments"},
		{[]string{"list", "unknown"}, "arguments"},
		{[]string{"run", "--tools", "unknown"}, "--tools"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, "_"), func(t *testing.T) {
			out, code := cliProcess(t, c.args...)
			if code != 2 || !strings.Contains(out, c.want) || strings.Contains(out, "unknown_fixture_key") {
				t.Fatalf("syntax must refuse before config: exit%d %s", code, out)
			}
		})
	}
}

func TestCLIInterspersedExactValues(t *testing.T) {
	text := " \t α\n\n```\n--help\n```  "
	for _, argv := range [][]string{
		{"template", "task", "--title", text, "--config", "first", "--config=last"},
		{"template", "--title=" + text, "--config=first", "task", "--config", "last"},
	} {
		name, a, err := parseCLI(argv)
		if err != nil || name != "template" || a.string("title") != text || a.string("config") != "last" || !reflect.DeepEqual(a.positionals, []string{"task"}) {
			t.Fatalf("changed exact/interspersed values: %q %v %v", name, a, err)
		}
	}
	_, a, err := parseCLI([]string{"run", "--prompt=--help", "--max-steps", "4", "--max-steps=7"})
	if err != nil || a.help || a.string("prompt") != "--help" || a.integer("max-steps") != 7 {
		t.Fatalf("literal/help/last-wins: %v %v", a, err)
	}
	_, a, err = parseCLI([]string{"sessions", "--", "--help"})
	if err != nil || a.help || !reflect.DeepEqual(a.positionals, []string{"--help"}) {
		t.Fatalf("option terminator: %v %v", a, err)
	}
}

func TestCLIRealPreflightAndExactChatWire(t *testing.T) {
	var calls atomic.Int32
	prompt := " \t exact α\n\nsource: \"quoted\"\n--help  "
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Model    string
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "r37-fixture" || len(body.Messages) != 1 || body.Messages[0].Content != prompt {
			t.Errorf("changed request: %#v %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"r37-fixture","choices":[{"message":{"content":"FIXTURE"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(good, []byte("model:\n  server: "+srv.URL+"\n  name: r37-fixture\n  runtime: openai\ndecision:\n  server: "+srv.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("unknown_fixture_key: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"run", "--config", good, "--bogus"}, {"run", "--config", good, "--max-steps", "hot"}, {"tui", "--config", good, "--help"}, {"decision", "--config", good, "--help"}} {
		out, code := cliProcess(t, argv...)
		want := 2
		if argv[len(argv)-1] == "--help" {
			want = 0
		}
		if code != want {
			t.Fatalf("preflight exit%d: %s", code, out)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("preflight reached model fixture")
	}
	out, code := cliProcess(t, "chat", "--config", bad, "--no-stream", "--prompt", prompt, "--config", good)
	if code != 0 || !strings.Contains(out, "FIXTURE") || calls.Load() != 1 {
		t.Fatalf("valid exact chat/last config exit%d calls%d: %s", code, calls.Load(), out)
	}
}

func TestCLIValidUtilitySmoke(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "valid.yaml")
	templates := filepath.Join(dir, "templates")
	sessions := filepath.Join(dir, "sessions")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{templates, sessions} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	doc := map[string]map[string]string{}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	doc["texts"]["templates.task"] = "TITLE={{.Title}}"
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(templates, "prompts.yaml")
	if err := os.WriteFile(catalogPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(dir, "configured-prompts.yaml")
	raw, err = yaml.Marshal(map[string]any{"prompts": map[string]any{"path": catalogPath}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configured, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"version"}, version},
		{[]string{"config", "--config", cfg}, "max_output_tokens: 16384"},
		{[]string{"tool", "bash", "--config", cfg}, "cmd (string, required)"},
		{[]string{"sessions", "--sessions-dir", sessions, "--config", cfg}, "no sessions yet"},
		{[]string{"template", "task", "--title", "α title", "--templates-dir", templates, "--config", cfg}, "TITLE=α title"},
		{[]string{"template", "task", "--title", "α title", "--config", configured}, "TITLE=α title"},
	}
	for _, c := range cases {
		out, code := cliProcess(t, c.args...)
		if code != 0 || !strings.Contains(out, c.want) {
			t.Fatalf("valid invocation exit%d: %s", code, out)
		}
	}
	standalone := filepath.Join(dir, "standalone")
	if err := os.Mkdir(standalone, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(standalone, "task.tmpl"), []byte("TITLE={{.Title}}"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, code := cliProcess(t, "template", "task", "--title", "α title", "--templates-dir", standalone, "--config", cfg); code == 0 || !strings.Contains(out, "standalone .tmpl files are no longer prompt sources") {
		t.Fatalf("standalone prompt silently admitted exit%d: %s", code, out)
	}
}

func TestCLIRepeatedMalformedStepsBeforeConfig(t *testing.T) {
	cases := [][]string{
		{"run", "--max-steps", "hot", "--max-steps", "7"},
		{"run", "--max-steps=3junk", "--max-steps=7"},
		{"run", "--max-steps=999999999999999999999999999", "--max-steps", "7"},
		{"run", "--max-steps=-1", "--max-steps=7"},
		{"launch", "approach-fixture", "--max-steps", "hot", "--max-steps=7"},
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, "_"), func(t *testing.T) {
			out, code := cliProcess(t, argv...)
			if code != 2 || !strings.Contains(out, "--max-steps") || strings.Contains(out, "unknown_fixture_key") {
				t.Fatalf("each explicit numeric value must refuse before config: exit%d %s", code, out)
			}
		})
	}
}

func TestCLIExplicitZeroSteps(t *testing.T) {
	for _, command := range []string{"run", "launch"} {
		base := []string{command}
		if command == "launch" {
			base = append(base, "approach-fixture")
		}
		for _, flags := range [][]string{{"--max-steps", "0"}, {"--max-steps=7", "--max-steps=0"}, {"--max-steps=0", "--max-steps=7"}} {
			_, args, e := parseCLI(append(append([]string{}, base...), flags...))
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if flags[len(flags)-1] == "--max-steps=7" {
				want = 7
			}
			if args.integer("max-steps") != want || args.string("max-steps") == "" {
				t.Fatalf("explicit value lost: %+v", args)
			}
		}
		_, absent, e := parseCLI(base)
		if e != nil || absent.string("max-steps") != "" {
			t.Fatalf("absent flag is not distinct: %+v %v", absent, e)
		}
	}
}

// Labelled HTTP model double, real CLI/config/store/check. No model inference.
func TestCLIExplicitZeroOverridesPositiveConfig(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int
		flags      []string
		calls      int
	}{
		{"absent retains configured one", 1, nil, 1},
		{"explicit zero exceeds configured one", 1, []string{"--max-steps=0"}, 2},
		{"explicit positive overrides configured three", 3, []string{"--max-steps=1"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				reply := "CLI_ZERO_COMPONENT_FINAL"
				if n == 1 {
					reply = "```tool\ntool: help\ntopic: read\n```"
				}
				writeTextToolFixture(w, reply)
			}))
			defer server.Close()
			dir := t.TempDir()
			cfg := config.Defaults()
			cfg.Agent.MaxSteps = tc.configured
			cfg.Model.ToolProtocol = "text"
			cfg.Model.Name = "labelled-component-only"
			cfg.Model.Server = server.URL + "/v1"
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Coding.CheckCommand = "true"
			cfg.Store.Path = filepath.Join(dir, "db")
			cfg.Sessions.Path = filepath.Join(dir, "sessions")
			cfg.Behavior.Path = filepath.Join(dir, "absent-behavior")
			cfg.Templates.Path = "../../templates"
			cfg.Recall.TopN = 0
			outcomeDecisionFixture(t, &cfg)
			path := filepath.Join(dir, "config.yaml")
			if e := config.Save(path, cfg); e != nil {
				t.Fatal(e)
			}
			args := append([]string{"run", "--config", path, "--prompt", "Inspect the read tool and run the configured check."}, tc.flags...)
			cmd := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$")
			cmd.Env = append(os.Environ(), "HARNESS_CLI_PROCESS=1", "HARNESS_CLI_ARGV="+strings.Join(args, "\x1f"))
			out, e := cmd.CombinedOutput()
			if int(calls.Load()) != tc.calls {
				t.Fatalf("actual requests %d want %d err=%v output=%s", calls.Load(), tc.calls, e, out)
			}
			if tc.calls == 2 && (e != nil || !strings.Contains(string(out), "CLI_ZERO_COMPONENT_FINAL")) {
				t.Fatalf("zero failed actual final/check: %v %s", e, out)
			}
		})
	}
}
