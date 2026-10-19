package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsMatchOwnerPolicy(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	cases := map[string]float64{
		"trigger": 90, "keep": 40, "summary": 10, "step": 1,
		"max": 20, "inflight outputs": 0, "schema cap": 0,
		"words per token": 0.75, "chars per token": 4,
	}
	if d.Context.OutputPruneAfterTokens != 50000 {
		t.Errorf("output_prune_after_tokens=%d, want 50000", d.Context.OutputPruneAfterTokens)
	}
	got := map[string]float64{
		"trigger": d.Context.Trigger, "keep": d.Context.Keep,
		"summary": d.Context.Summary, "step": d.Context.SummaryStep,
		"max": d.Context.SummaryMax, "inflight outputs": d.Context.InflightOutputs,
		"schema cap":      d.Schemas.InflightCap,
		"words per token": d.Context.SummaryWordsPerToken,
		"chars per token": d.Behavior.CharsPerToken,
	}
	for name, want := range cases {
		if got[name] != want {
			t.Errorf("%s: got %v, want %v", name, got[name], want)
		}
	}
	if d.Schemas.KeepLatest != 3 {
		t.Errorf("keep_latest: got %d, want 3", d.Schemas.KeepLatest)
	}
	if d.Retention.SessionMonths != 3 {
		t.Errorf("session_months: got %d, want 3", d.Retention.SessionMonths)
	}
	if d.Behavior.MaxTokens != 1000 {
		t.Errorf("max_tokens: got %d, want 1000", d.Behavior.MaxTokens)
	}
	if d.Behavior.Path != "behavior.md" {
		t.Errorf("behavior.path: got %q, want behavior.md", d.Behavior.Path)
	}
	rc := d.Recall
	if rc.TopN != 3 || rc.MaxChars != 1000 || rc.MinWord != 4 {
		t.Errorf("recall defaults wrong: %+v", rc)
	}
	if !d.Params.Thinking {
		t.Error("params.thinking must default to true")
	}
	if d.Model.Server != "http://127.0.0.1:8080/v1" {
		t.Errorf("model.server: got %q", d.Model.Server)
	}
	b := d.Tools.Bash
	if b.DefaultTimeout != 30 || b.MaxTimeout != 600 || b.MaxBytes != 65536 || b.MaxLines != 2000 {
		t.Errorf("bash defaults wrong: %+v", b)
	}
	s := d.Tools.Search
	if s.MaxMatches != 100 || s.MaxBytes != 65536 || s.MaxLineLength != 1000 {
		t.Errorf("search defaults wrong: %+v", s)
	}
	f := d.Tools.Fetch
	if f.TimeoutSeconds != 30 || f.MaxBytes != 262144 {
		t.Errorf("fetch defaults wrong: %+v", f)
	}
	if d.Agent.MaxSteps != 0 {
		t.Errorf("agent.max_steps=%d, want 0", d.Agent.MaxSteps)
	}
	if d.Store.Path != "data/kuzu" {
		t.Errorf("store.path=%q, want data/kuzu", d.Store.Path)
	}
	if d.Sessions.Path != "data/sessions" {
		t.Errorf("sessions.path=%q, want data/sessions", d.Sessions.Path)
	}
	if d.Templates.Path != "templates" {
		t.Errorf("templates.path=%q, want templates", d.Templates.Path)
	}
	sb := d.Subagents
	if sb.Mode != SubagentsConcurrent || sb.MaxSubagents != 0 || sb.ContextWindow != 0 || sb.MaxResultChars != 2000 {
		t.Errorf("subagents defaults wrong: %+v", sb)
	}
	wf := d.Workflows
	if wf.MaxRetries != 2 || wf.RetryDelaySeconds != 0 {
		t.Errorf("workflows defaults wrong: %+v", wf)
	}
	dc := d.Decision
	if dc.Server != "http://127.0.0.1:8090" || dc.Model != "typed-decisions" || dc.Device != "auto" || dc.TimeoutSeconds != 30 || dc.RiskThreshold != 0.7 || dc.CoherenceMaxChars != 0 || dc.JudgmentContextChars != 500 {
		t.Errorf("decision defaults wrong: %+v", dc)
	}
	if len(dc.ConfidenceBuckets) != 3 || dc.ConfidenceBuckets[0] != 0.5 || dc.ConfidenceBuckets[1] != 0.7 || dc.ConfidenceBuckets[2] != 0.9 {
		t.Errorf("confidence_buckets default wrong: %v", dc.ConfidenceBuckets)
	}
	au := d.Auth
	if au.Mode != AuthAll || len(au.JudgedTools) != 2 || au.JudgedTools[0] != "bash" || au.JudgedTools[1] != "fetch" || len(au.FreeTools) != 8 || au.FreeTools[0] != "grep" || au.FreeTools[1] != "find" || au.FreeTools[2] != "ask" || au.FreeTools[3] != "judge" || au.FreeTools[4] != "recall" || au.FreeTools[5] != "find_file" || au.FreeTools[6] != "find_dir" || au.FreeTools[7] != "help" || au.MaxArgChars != 80 {
		t.Errorf("auth defaults wrong: %+v", au)
	}
	ag := d.Agent
	if ag.MaxSteps != 0 || ag.LoopRepeat != 3 || ag.LoopStall != 5 {
		t.Errorf("agent defaults wrong: %+v", ag)
	}
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, usedFile, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if usedFile {
		t.Error("usedFile must be false for a missing file")
	}
	if cfg.Context.Trigger != 90 {
		t.Errorf("trigger: got %v, want 90", cfg.Context.Trigger)
	}
}

func TestLoadOverlayChangesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	body := "context:\n  compaction_trigger: 80\nauth:\n  mode: yolo\nstore:\n  path: /tmp/custom-kuzu\nsessions:\n  path: /tmp/custom-sessions\ntemplates:\n  path: /tmp/custom-templates\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, usedFile, err := Load(path)
	if err != nil {
		t.Fatalf("valid overlay must load: %v", err)
	}
	if !usedFile {
		t.Error("usedFile must be true when the file exists")
	}
	if cfg.Context.Trigger != 80 {
		t.Errorf("trigger: got %v, want 80", cfg.Context.Trigger)
	}
	if cfg.Auth.Mode != AuthYOLO {
		t.Errorf("mode: got %q, want yolo", cfg.Auth.Mode)
	}
	if cfg.Context.Keep != 40 {
		t.Errorf("unset keys must keep defaults, keep=%v", cfg.Context.Keep)
	}
	if cfg.Store.Path != "/tmp/custom-kuzu" {
		t.Errorf("store.path: got %q, want /tmp/custom-kuzu", cfg.Store.Path)
	}
	if cfg.Sessions.Path != "/tmp/custom-sessions" {
		t.Errorf("sessions.path: got %q, want /tmp/custom-sessions", cfg.Sessions.Path)
	}
	if cfg.Templates.Path != "/tmp/custom-templates" {
		t.Errorf("templates.path: got %q, want /tmp/custom-templates", cfg.Templates.Path)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte("context:\n  compactoin_trigger: 80\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(path)
	if err == nil {
		t.Fatal("typo'd field must error, got nil")
	}
	if !strings.Contains(err.Error(), "compactoin_trigger") {
		t.Errorf("error must name the bad field, got: %v", err)
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte("context: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("malformed YAML must error, got nil")
	}
}

func TestValidationIsPlainLanguage(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"trigger over 100", func(c *Config) { c.Context.Trigger = 140 }, "context.compaction_trigger must be between 0 and 100, got 140"},
		{"summary over max", func(c *Config) { c.Context.Summary = 30 }, "cannot exceed context.compaction_summary_max"},
		{"bad auth mode", func(c *Config) { c.Auth.Mode = "sometimes" }, `auth.mode must be one of yolo, authorize-risky, authorize-all, got "sometimes"`},
		{"negative keep", func(c *Config) { c.Schemas.KeepLatest = -1 }, "schemas.keep_latest must be at least 3, got -1"},
		{"bad server URL", func(c *Config) { c.Model.Server = "not-a-url" }, `model.server must be an http(s) URL, got "not-a-url"`},
		{"bash default over max", func(c *Config) { c.Tools.Bash.DefaultTimeout = 9999 }, "tools.bash.default_timeout (9999) cannot exceed tools.bash.max_timeout (600)"},
		{"search caps must be positive", func(c *Config) { c.Tools.Search.MaxMatches = 0 }, "tools.search caps must all be positive"},
		{"fetch caps must be positive", func(c *Config) { c.Tools.Fetch.MaxBytes = 0 }, "tools.fetch timeout and cap must both be positive"},
		{"negative agent steps", func(c *Config) { c.Agent.MaxSteps = -1 }, "agent.max_steps cannot be negative"},
		{"negative loop repeat", func(c *Config) { c.Agent.LoopRepeat = -1 }, "agent.loop_repeat cannot be negative, got -1"},
		{"negative loop stall", func(c *Config) { c.Agent.LoopStall = -1 }, "agent.loop_stall cannot be negative, got -1"},
		{"negative coherence cap", func(c *Config) { c.Decision.CoherenceMaxChars = -1 }, "decision.coherence_max_chars cannot be negative, got -1"},
		{"negative arg chars", func(c *Config) { c.Auth.MaxArgChars = -1 }, "auth.max_arg_chars cannot be negative, got -1"},
		{"empty store path", func(c *Config) { c.Store.Path = "" }, "store.path cannot be empty"},
		{"empty sessions path", func(c *Config) { c.Sessions.Path = "" }, "sessions.path cannot be empty"},
		{"empty templates path", func(c *Config) { c.Templates.Path = "" }, "templates.path cannot be empty"},
		{"bad subagents mode", func(c *Config) { c.Subagents.Mode = "parallel" }, `subagents.mode must be sequential or concurrent, got "parallel"`},
		{"negative max subagents", func(c *Config) { c.Subagents.MaxSubagents = -1 }, "subagents.max_subagents cannot be negative, got -1"},
		{"zero result chars", func(c *Config) { c.Subagents.MaxResultChars = 0 }, "subagents.max_result_chars must be positive, got 0"},
		{"negative max retries", func(c *Config) { c.Workflows.MaxRetries = -1 }, "workflows.max_retries cannot be negative, got -1"},
		{"negative retry delay", func(c *Config) { c.Workflows.RetryDelaySeconds = -1 }, "workflows.retry_delay_seconds cannot be negative, got -1"},
		{"empty decision server", func(c *Config) { c.Decision.Server = "" }, "decision.server must be an http(s) URL"},
		{"empty decision model", func(c *Config) { c.Decision.Model = "" }, "decision.model must name the checkpoint id"},
		{"bad decision device", func(c *Config) { c.Decision.Device = "tpu" }, `decision.device must be auto, cpu, or gpu, got "tpu"`},
		{"retired npu device", func(c *Config) { c.Decision.Device = "npu" }, `decision.device must be auto, cpu, or gpu, got "npu"`},
		{"zero decision timeout", func(c *Config) { c.Decision.TimeoutSeconds = 0 }, "decision.timeout_seconds must be positive, got 0"},
		{"risk threshold over 1", func(c *Config) { c.Decision.RiskThreshold = 1.5 }, "decision.risk_threshold must be between 0 and 1, got 1.5"},
		{"negative risk threshold", func(c *Config) { c.Decision.RiskThreshold = -0.1 }, "decision.risk_threshold must be between 0 and 1, got -0.1"},
		{"empty behavior path", func(c *Config) { c.Behavior.Path = "" }, "behavior.path cannot be empty"},
		{"zero compaction summary", func(c *Config) { c.Context.Summary = 0; c.Context.SummaryMax = 0 }, "context.compaction_summary must be positive, got 0"},
		{"negative judgment context", func(c *Config) { c.Decision.JudgmentContextChars = -1 }, "decision.judgment_context_chars cannot be negative, got -1"},
		{"bucket over 1", func(c *Config) { c.Decision.ConfidenceBuckets = []float64{1.5} }, "decision.confidence_buckets edge 0 must be between 0 and 1 exclusive, got 1.5"},
		{"bucket at 0", func(c *Config) { c.Decision.ConfidenceBuckets = []float64{0} }, "decision.confidence_buckets edge 0 must be between 0 and 1 exclusive, got 0"},
		{"buckets not ascending", func(c *Config) { c.Decision.ConfidenceBuckets = []float64{0.7, 0.5} }, "decision.confidence_buckets must ascend"},
		{"zero words per token", func(c *Config) { c.Context.SummaryWordsPerToken = 0 }, "context.summary_words_per_token must be positive, got 0"},
		{"zero chars per token", func(c *Config) { c.Behavior.CharsPerToken = 0 }, "behavior.chars_per_token must be positive, got 0"},
		{"negative recall top_n", func(c *Config) { c.Recall.TopN = -1 }, "recall.top_n cannot be negative, got -1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("got %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestEmptyBucketsValid(t *testing.T) {
	cfg := Defaults()
	cfg.Decision.ConfidenceBuckets = nil
	if err := cfg.Validate(); err != nil {
		t.Errorf("empty buckets must validate (one all-bucket): %v", err)
	}
}

// TestSaveRoundTrip pins the runtime switch: Save writes, Load reads
// back the same values through the real file.
func TestSaveRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.Model.Server = "http://127.0.0.1:11434/v1"
	cfg.Decision.Device = "gpu"
	path := filepath.Join(t.TempDir(), "specific-harness.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, used, err := Load(path)
	if err != nil || !used {
		t.Fatalf("load: %v used=%v", err, used)
	}
	if back.Model.Server != cfg.Model.Server || back.Decision.Device != "gpu" {
		t.Errorf("round trip lost values: %+v", back.Model)
	}
	if !reflect.DeepEqual(back, cfg) {
		t.Error("round trip drifted")
	}
}

// TestSaveRefusesBad pins fail-closed: a bad value never clobbers the file.
func TestSaveRefusesBad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "specific-harness.yaml")
	if err := Save(path, Defaults()); err != nil {
		t.Fatalf("save defaults: %v", err)
	}
	bad := Defaults()
	bad.Decision.Device = "npu"
	if err := Save(path, bad); err == nil || !strings.Contains(err.Error(), "decision.device") {
		t.Errorf("bad save must refuse loudly: %v", err)
	}
	back, _, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(back, Defaults()) {
		t.Error("bad save clobbered the good file")
	}
}

func TestRecipeValidation(t *testing.T) {
	good := RecipeConfig{Name: "vllm", Kind: "openai", URL: "http://127.0.0.1:8000/v1", Argv: []string{"x"}}
	for _, r := range []RecipeConfig{
		{Kind: "openai", URL: "u", Argv: []string{"x"}},
		{Name: "n", Kind: "bogus", URL: "u", Argv: []string{"x"}},
		{Name: "n", Kind: "openai", Argv: []string{"x"}},
		{Name: "n", Kind: "openai", URL: "u"},
	} {
		cfg := Defaults()
		cfg.Serve.Recipes = []RecipeConfig{r}
		if err := cfg.Validate(); err == nil {
			t.Errorf("recipe %+v must refuse", r)
		}
	}
	cfg := Defaults()
	cfg.Serve.Recipes = []RecipeConfig{good}
	if err := cfg.Validate(); err != nil {
		t.Errorf("good recipe refused: %v", err)
	}
}

func TestTabsTitleEveryValidation(t *testing.T) {
	cfg := Defaults()
	cfg.Tabs.TitleEvery = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tabs.title_every") {
		t.Errorf("negative title_every must refuse: %v", err)
	}
}

func TestExampleMatchesDefaults(t *testing.T) {
	cfg, usedFile, err := Load("../../specific-harness.yaml.example")
	if err != nil {
		t.Fatalf("example must load: %v", err)
	}
	if !usedFile {
		t.Fatal("example file missing")
	}
	want := Defaults()
	want.templatesDefault = false // The example explicitly selects the project template path.
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("specific-harness.yaml.example drifted from Defaults(): %+v", cfg)
	}
}

func TestWatchStartupCaps(t *testing.T) {
	c := Defaults()
	if c.Watch.MaxFileBytes != 131072 || c.Watch.StartupMaxBytes != 8388608 || c.Watch.StartupMaxFiles != 512 {
		t.Fatalf("watch defaults=%+v", c.Watch)
	}
	for name, mutate := range map[string]func(*Config){
		"watch.max_file_bytes":    func(c *Config) { c.Watch.MaxFileBytes = 0 },
		"watch.startup_max_bytes": func(c *Config) { c.Watch.StartupMaxBytes = -1 },
		"watch.startup_max_files": func(c *Config) { c.Watch.StartupMaxFiles = 0 },
	} {
		bad := c
		mutate(&bad)
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s admitted: %v", name, err)
		}
	}
}

func TestLegacyCompactionTriggerRetired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("context:\n  window: 131072\n  compaction_absolute_tokens: 65536\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, used, err := Load(path)
	if err != nil || !used || cfg.Context.CompactionAbsoluteTokens != 0 || cfg.Context.Trigger != 90 {
		t.Fatalf("old trigger remains active: %+v used=%t err=%v", cfg.Context, used, err)
	}
}
