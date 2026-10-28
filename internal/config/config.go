// Package config loads the harness YAML configuration. Every tunable named
// for the harness lives here, with explicit context policy applied below.
// Anything absent falls back to the built-in defaults, and a missing file means
// pure defaults (disclosed, never
// silent). Percentages are 0-100 in YAML.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"specific-harness/internal/checkreport"
	"specific-harness/internal/prompts"
)

// Authorization modes.
const (
	AuthYOLO  = "yolo"
	AuthRisky = "authorize-risky"
	AuthAll   = "authorize-all"
)

// DefaultPath is the config location when no --config flag is given.
const DefaultPath = "caliper-harness.yaml"

const LegacyPath = "specific-harness.yaml"

// DefaultFile keeps existing projects working without copying or migrating
// their configuration. An explicit --config always bypasses this selection.
func DefaultFile() string {
	if _, err := os.Stat(DefaultPath); !errors.Is(err, os.ErrNotExist) {
		return DefaultPath
	}
	if _, err := os.Stat(LegacyPath); !errors.Is(err, os.ErrNotExist) {
		return LegacyPath
	}
	if dir, err := WorkspaceDir(); err == nil {
		return filepath.Join(dir, DefaultPath)
	}
	// Load reports the underlying user-directory error before any write.
	return DefaultPath
}

type CodingConfig struct {
	CheckCommand  string                         `yaml:"check_command"`
	ReportFormat  string                         `yaml:"report_format,omitempty"`
	ExpectedTests []string                       `yaml:"expected_tests,omitempty"`
	Stages        []checkreport.StageRequirement `yaml:"stages,omitempty"`
}

func (c CodingConfig) ReportContract() checkreport.Contract {
	return (checkreport.Contract{Format: c.ReportFormat, Expected: c.ExpectedTests, Stages: c.Stages}).Clone()
}

type UIConfig struct {
	MaxFileEntries    int `yaml:"max_file_entries"`
	MaxFileViewLines  int `yaml:"max_file_view_lines"`
	MaxRunIDs         int `yaml:"max_run_ids"`
	MaxDecisionRows   int `yaml:"max_decision_rows"`
	MaxAlerts         int `yaml:"max_alerts"`
	MaxLegacyMessages int `yaml:"max_legacy_messages"`
}

type Config struct {
	ProjectOverride bool             `yaml:"-"` // --config explicitly selects project scope, even for a managed path.
	Prompts         PromptsConfig    `yaml:"prompts"`
	PromptCatalog   *prompts.Catalog `yaml:"-"`
	UI              UIConfig         `yaml:"ui"`
	Coding          CodingConfig     `yaml:"coding"`
	Testing         TestingConfig    `yaml:"testing"`
	Context         ContextConfig    `yaml:"context"`
	Schemas         SchemaConfig     `yaml:"schemas"`
	Retention       RetentionConfig  `yaml:"retention"`
	Behavior        BehaviorConfig   `yaml:"behavior"`
	Recall          RecallConfig     `yaml:"recall"`
	Auth            AuthConfig       `yaml:"auth"`
	Params          ParamsConfig     `yaml:"params"`
	Model           ModelConfig      `yaml:"model"`
	Tools           ToolsConfig      `yaml:"tools"`
	Agent           AgentConfig      `yaml:"agent"`
	Store           StoreConfig      `yaml:"store"`
	Sessions        SessionsConfig   `yaml:"sessions"`
	Templates       TemplatesConfig  `yaml:"templates"`
	Subagents       SubagentsConfig  `yaml:"subagents"`
	Workflows       WorkflowsConfig  `yaml:"workflows"`
	Decision        DecisionConfig   `yaml:"decision"`
	Watch           WatchConfig      `yaml:"watch"`
	Serve           ServeConfig      `yaml:"serve"`
	Tabs            TabsConfig       `yaml:"tabs"`

	templatesDefault bool
}

type PromptsConfig struct {
	Path string `yaml:"path,omitempty"`
}

// LoadPrompts takes one immutable request/run snapshot. A custom legacy
// templates directory must contain the same catalog, never standalone prompts.
func (c Config) LoadPrompts() (*prompts.Catalog, error) {
	if c.PromptCatalog != nil {
		return c.PromptCatalog, nil
	}
	legacy := c.Templates.Path != "" && c.Templates.Path != "templates"
	if c.Prompts.Path != "" {
		if legacy {
			return nil, fmt.Errorf("prompts.path replaces templates.path; remove the legacy override and select one prompts.yaml")
		}
		return prompts.Load(c.Prompts.Path)
	}
	if legacy {
		return prompts.Legacy(c.Templates.Path)
	}
	return prompts.Default()
}

type ContextConfig struct {
	// Window is the model context size in tokens. 0 means unset:
	// hardware autodetect fills it in.
	Window int `yaml:"window"`
	// Positive Trigger is the emergency window threshold; zero disables automatic summaries.
	// Deterministic pruning only replaces consumed tool-output bodies older than
	// OutputPruneAfterTokens estimated context tokens; reasoning/schemas stay exact.
	// Emergency compaction keeps latest Keep percent and targets Summary percent,
	// growing by SummaryStep up to SummaryMax. Targets guide concise model writing;
	// admission requires a smaller complete continuation that fits the window.
	Trigger float64 `yaml:"compaction_trigger"`
	// CompactionAbsoluteTokens is a retired key accepted for old configuration files.
	// It has no effect; automatic summaries use Trigger percent of Window only.
	CompactionAbsoluteTokens int     `yaml:"compaction_absolute_tokens,omitempty"`
	Keep                     float64 `yaml:"compaction_keep"`
	Summary                  float64 `yaml:"compaction_summary"`
	SummaryStep              float64 `yaml:"compaction_summary_step"`
	SummaryMax               float64 `yaml:"compaction_summary_max"`
	// SummaryWordsPerToken sizes the summary writing target from its token target.
	// A wording ratio, not a measured count or hard summary quota.
	SummaryWordsPerToken float64 `yaml:"summary_words_per_token"`
	// OutputPruneAfterTokens counts all subsequent model context with the existing
	// calibrated content estimate. Only strictly older consumed outputs qualify.
	// Zero disables; protected/unread receipts and raw session events remain.
	OutputPruneAfterTokens int `yaml:"output_prune_after_tokens"`
	// InflightOutputs is a deprecated compatibility key, accepted but ignored.
	// Startup discloses nonzero old percentages; OutputPruneAfterTokens owns pruning.
	InflightOutputs float64 `yaml:"inflight_outputs"`
}

// RecallConfig caps recall to the top TopN matches, with MaxChars
// Unicode runes for the complete returned text, including metadata and
// ellipsis. Words must have at least MinWord characters (stopwords dropped).
// TopN 0 disables; MaxChars 0 is unlimited; MinWord 0 keeps every word.
type RecallConfig struct {
	TopN     int `yaml:"top_n"`
	MaxChars int `yaml:"max_chars"`
	MinWord  int `yaml:"min_word"`
}

type SchemaConfig struct {
	// InflightCap is a deprecated compatibility key, accepted but ignored.
	// Schemas are never routinely pruned.
	InflightCap float64 `yaml:"inflight_cap"`
	// KeepLatest protects recent schemas during model-written compaction/resume.
	KeepLatest int `yaml:"keep_latest"`
}

type RetentionConfig struct {
	// SessionMonths auto-deletes sessions this many months after last use.
	SessionMonths int `yaml:"session_months"`
}

type BehaviorConfig struct {
	// MaxTokens caps the injected behavior document. 0 disables.
	MaxTokens int `yaml:"max_tokens"`
	// CharsPerToken converts the token cap to a char cap for the file
	// pre-read. A sizing ratio, not a measurement.
	CharsPerToken float64 `yaml:"chars_per_token"`
	// Path is the behavior document. A missing file means no behavior
	// (silent); an unreadable one fails loud.
	Path string `yaml:"path"`
}

type AuthConfig struct {
	// Mode is one of: yolo, authorize-risky, authorize-all.
	Mode string `yaml:"mode"`
	// JudgedTools are explicitly judged under authorize-risky.
	JudgedTools []string `yaml:"judged_tools,omitempty"`
	// FreeTools always run under authorize-risky. Anything not free
	// is judged — fail closed toward the decision model.
	FreeTools []string `yaml:"free_tools,omitempty"`
	// MaxArgChars caps displayed arg values in prompts. 0 = unlimited.
	MaxArgChars int `yaml:"max_arg_chars"`
}

// ValidateLists checks explicit authorization intent without changing names.
func (a AuthConfig) ValidateLists() error {
	seen := map[string]string{}
	for _, list := range []struct {
		key   string
		names []string
	}{{"auth.free_tools", a.FreeTools}, {"auth.judged_tools", a.JudgedTools}} {
		for _, name := range list.names {
			if name == "" || name != strings.TrimSpace(name) || name != strings.ToLower(name) {
				return fmt.Errorf("%s tool %q must be a nonblank canonical registered name", list.key, name)
			}
			if prior, ok := seen[name]; ok {
				return fmt.Errorf("%s tool %q duplicates or overlaps %s", list.key, name, prior)
			}
			seen[name] = list.key
		}
	}
	return nil
}

type ParamsConfig struct {
	MaxOutputTokens   OutputTokens `yaml:"max_output_tokens"`
	Temperature       float64      `yaml:"temperature"`
	TopK              int          `yaml:"top_k"`
	RepetitionPenalty float64      `yaml:"repetition_penalty"`
	// Thinking enables the model's thinking traces. Threaded into the
	// client with the other live params in T6.10.
	Thinking bool `yaml:"thinking"`
}

// OutputTokens rejects YAML's fractional-to-int coercion. Zero defers to runtime policy.
type OutputTokens int

func (n *OutputTokens) UnmarshalYAML(node *yaml.Node) error {
	var value int
	if node.Tag != "!!int" {
		return fmt.Errorf("params.max_output_tokens must be an integer, got %q", node.Value)
	}
	if err := node.Decode(&value); err != nil {
		return fmt.Errorf("params.max_output_tokens is not a valid integer: %w", err)
	}
	*n = OutputTokens(value)
	return nil
}

type ModelConfig struct {
	PlainRequestTimeoutSeconds int    `yaml:"plain_request_timeout_seconds"`
	ToolProtocol               string `yaml:"tool_protocol"`
	// Runtime selects the wire contract: openai (default), ollama, mlx, or rulith.
	Runtime string `yaml:"runtime"`
	// Server is the selected runtime base URL.
	Server string `yaml:"server"`
	// Name is the model ID. Empty means "pass --model".
	Name string `yaml:"name"`
}

type ToolsConfig struct {
	GraphQL GraphQLConfig `yaml:"graphql"`
	Bash    BashConfig    `yaml:"bash"`
	Search  SearchConfig  `yaml:"search"`
	Fetch   FetchConfig   `yaml:"fetch"`
}

// GraphQLConfig bounds the explicit graph interface, not host session archives.
type GraphQLConfig struct {
	MaxRequestBytes int `yaml:"max_request_bytes"`
	MaxSelections   int `yaml:"max_selections"`
	MaxRows         int `yaml:"max_rows"`

	// Shared result/source budget; source admission conservatively charges four
	// bytes per character. Keep enough headroom for complete guided PAT briefs.
	MaxResultBytes int `yaml:"max_result_bytes"`
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

func (g GraphQLConfig) Validate() error {
	for _, v := range []struct {
		name string
		n    int
	}{
		{"max_request_bytes", g.MaxRequestBytes}, {"max_selections", g.MaxSelections},
		{"max_rows", g.MaxRows}, {"max_result_bytes", g.MaxResultBytes}, {"timeout_seconds", g.TimeoutSeconds},
	} {
		if v.n <= 0 {
			return fmt.Errorf("tools.graphql.%s must be positive", v.name)
		}
	}
	if g.TimeoutSeconds > 9223372036 {
		return fmt.Errorf("tools.graphql.timeout_seconds exceeds representable duration")
	}
	return nil
}

type BashConfig struct {
	// DefaultTimeout seconds apply when the call omits timeout.
	DefaultTimeout int `yaml:"default_timeout"`
	// MaxTimeout seconds cap any call.
	MaxTimeout int `yaml:"max_timeout"`
	// MaxBytes/MaxLines cap returned output; overflows truncate with a marker.
	MaxBytes int `yaml:"max_bytes"`
	MaxLines int `yaml:"max_lines"`
}

type SearchConfig struct {
	// MaxMatches caps grep results; MaxBytes caps result text;
	// MaxLineLength cuts long match lines with a marker.
	// MaxCaptureBytes bounds aggregate raw stdout/stderr retained before parsing.
	MaxCaptureBytes int `yaml:"max_capture_bytes"`
	MaxMatches      int `yaml:"max_matches"`
	MaxBytes        int `yaml:"max_bytes"`
	MaxLineLength   int `yaml:"max_line_length"`
}

type FetchConfig struct {
	// TimeoutSeconds bounds the request; MaxBytes caps the body kept.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxBytes       int `yaml:"max_bytes"`
}

type RunSeconds int

func (n *RunSeconds) UnmarshalYAML(node *yaml.Node) error {
	var v OutputTokens
	err := v.UnmarshalYAML(node)
	if err != nil {
		return fmt.Errorf("agent.max_run_seconds: %s", strings.ReplaceAll(err.Error(), "params.max_output_tokens", "value"))
	}
	*n = RunSeconds(v)
	return nil
}

type ThinkingLoopConfig struct {
	Repeat        int `yaml:"repeat"`
	MinUnitBytes  int `yaml:"min_unit_bytes"`
	MaxUnitBytes  int `yaml:"max_unit_bytes"`
	SampleBytes   int `yaml:"sample_bytes"`
	MaxInputBytes int `yaml:"max_input_bytes"`
}

type AgentConfig struct {
	ThinkingLoop  ThinkingLoopConfig `yaml:"thinking_loop"`
	MaxRunSeconds RunSeconds         `yaml:"max_run_seconds"`
	// MaxSteps caps charged calls/attempts; 0 means no separate call ceiling.
	MaxSteps int `yaml:"max_steps"`
	// LoopRepeat requests advisory direction after N identical executed calls. Zero disables it.
	LoopRepeat int `yaml:"loop_repeat"`
	// LoopStall requests advisory direction after M attempts without fresh observed information.
	// This is cadence, never a call/error ceiling. Zero disables it.
	LoopStall int `yaml:"loop_stall"`
}

type StoreConfig struct {
	// Path is the Kuzu database directory. Created on first open.
	Path string `yaml:"path"`
	// BufferPoolBytes is a construction policy; 0 lets Kuzu size automatically.
	BufferPoolBytes BufferPoolBytes `yaml:"buffer_pool_bytes"`
}

// BufferPoolBytes rejects YAML float-to-uint coercion, including overflow.
type BufferPoolBytes uint64

func (n *BufferPoolBytes) UnmarshalYAML(node *yaml.Node) error {
	var value uint64
	if node.Tag != "!!int" {
		return fmt.Errorf("store.buffer_pool_bytes must be an unsigned integer, got %q", node.Value)
	}
	if err := node.Decode(&value); err != nil {
		return fmt.Errorf("store.buffer_pool_bytes is not a valid unsigned integer: %w", err)
	}
	*n = BufferPoolBytes(value)
	return nil
}

// RecipeConfig is one user-supplied server start recipe. Built-ins
// cover ollama, mlx, and the sidecar; a same-name entry here
// replaces its built-in.
type RecipeConfig struct {
	Launcher string   `yaml:"launcher,omitempty"` // optional explicit preset or executable entered by the user
	Runtime  string   `yaml:"runtime,omitempty"`  // explicit worker wire contract; never inferred from its name
	Name     string   `yaml:"name"`
	Kind     string   `yaml:"kind"` // ollama, openai, or sidecar
	URL      string   `yaml:"url"`
	Argv     []string `yaml:"argv,omitempty"`
	Dir      string   `yaml:"dir"`
}

// SavedModel is an explicit model choice associated with one saved endpoint.
type SavedModel struct {
	Server string `yaml:"server"`
	Name   string `yaml:"name"`
	Path   string `yaml:"path,omitempty"`
}

type ServeConfig struct {
	ProbeTimeoutSeconds int          `yaml:"probe_timeout_seconds"`
	ModelDirs           []string     `yaml:"model_dirs,omitempty"` // deprecated; retained for compatibility, never enumerated
	Models              []SavedModel `yaml:"models,omitempty"`
	// Recipes are extra start recipes beyond the built-ins.
	Recipes []RecipeConfig `yaml:"recipes,omitempty"`
}

type TabsConfig struct {
	TitleTimeoutSeconds int `yaml:"title_timeout_seconds"`
	// TitleEvery retitles chat tabs via the worker every N completed
	// turns; 0 disables.
	TitleEvery int `yaml:"title_every"`
}

type SessionsConfig struct {
	// Path is the JSONL sessions directory. Created on first append.
	Path string `yaml:"path"`
}

type TemplatesConfig struct {
	// Path is the scaffold templates directory. Must exist.
	Path string `yaml:"path,omitempty"`
}

// Subagent modes.
const (
	SubagentsSequential = "sequential"
	SubagentsConcurrent = "concurrent"
)

type SubagentsConfig struct {
	// Mode is sequential (task order, one at a time) or concurrent
	// (semaphore-capped goroutines, results in task order).
	Mode string `yaml:"mode"`
	// MaxSubagents caps concurrent fan-out. 0 autodetects from hardware.
	MaxSubagents int `yaml:"max_subagents"`
	// ContextWindow is the per-subagent token budget (crude chars/4
	// estimate until Phase 5). 0 inherits the parent's window.
	ContextWindow int `yaml:"context_window"`
	// MaxResultChars truncates each subagent output in the launch result
	// fed to the parent context; full text stays on the task node.
	MaxResultChars int `yaml:"max_result_chars"`
}

type WorkflowsConfig struct {
	// MaxRetries caps per-task attempts after the first (0 = run once).
	MaxRetries int `yaml:"max_retries"`
	// RetryDelaySeconds waits this long between attempts. 0 = no wait.
	RetryDelaySeconds int `yaml:"retry_delay_seconds"`
	// VerifyFollowup queues a review run after tasks complete: one
	// reviewer subagent per done task, driven by its verify field.
	VerifyFollowup bool `yaml:"verify_followup"`
}

type DecisionConfig struct {
	Protocol        string `yaml:"protocol,omitempty"` // system_one selects Ollama's typed decision endpoint; empty keeps the runtime default
	Disabled        bool   `yaml:"disabled"`           // Explicit false must override a personal true selection on project reload.
	Runtime         string `yaml:"runtime"`            // laya or native system_one, never inferred from the checkpoint name
	Path            string `yaml:"path,omitempty"`     // optional downloaded local assets bound to Model
	Revision        string `yaml:"revision,omitempty"`
	TrustRemoteCode bool   `yaml:"trust_remote_code"` // opt in to downloaded checkpoint Python

	// Server is the decision sidecar base URL.
	Server string `yaml:"server"`
	// Model is the checkpoint id the sidecar must serve; the sidecar
	// refuses a request naming anything else, so config skew fails loud.
	Model string `yaml:"model"`
	// Device is the wanted backend: auto, cpu, or gpu (npu retired to future/npu).
	Device string `yaml:"device"`
	// TimeoutSeconds caps one judgment round-trip.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// MaxInputTokens includes context, instructions and choices, counted by the native tokenizer.
	MaxInputTokens int `yaml:"max_input_tokens"`
	// RiskThreshold grades authorization/readiness and factual-admission gates;
	// below it the gate asks the human or refuses admission. Advisory supervision
	// uses the owner's separate fixed 0.5 floor and never grants authority.
	RiskThreshold float64 `yaml:"risk_threshold"`
	// CoherenceMaxChars is a legacy optional logical-request byte guard.
	// 0 delegates capacity to native full-input token admission; positive values add a local policy.
	CoherenceMaxChars int `yaml:"coherence_max_chars"`
	// JudgmentContextChars caps the context kept in a judgment log event.
	// 0 = unlimited.
	JudgmentContextChars int `yaml:"judgment_context_chars"`
	// StyleEnforcement enables the post-task style judge against the
	// workspace styleguide document (knowledge key "styleguide"). The
	// document stays readable through GraphQL whether this is on or off.
	StyleEnforcement bool `yaml:"style_enforcement"`
	// ConfidenceBuckets are the ascending cut points of the judgments
	// histogram. Keep the risk threshold among them so the summary shows
	// the obey/ask split. Empty = one "all" bucket.
	ConfidenceBuckets []float64 `yaml:"confidence_buckets,omitempty"`
}

// Defaults returns product defaults, including the owner's output-age policy.
func Defaults() Config {
	return Config{
		Testing:          TestingConfig{Guidance: true},
		templatesDefault: true,
		UI:               UIConfig{MaxFileEntries: 500, MaxFileViewLines: 2000, MaxRunIDs: 20, MaxDecisionRows: 100, MaxAlerts: 50, MaxLegacyMessages: 100},
		Serve:            ServeConfig{ProbeTimeoutSeconds: 3},
		Context: ContextConfig{
			Window:                 0,
			Trigger:                90,
			Keep:                   40,
			Summary:                10,
			SummaryStep:            1,
			SummaryMax:             20,
			SummaryWordsPerToken:   0.75,
			OutputPruneAfterTokens: 50000,
			InflightOutputs:        0,
		},
		Schemas:   SchemaConfig{InflightCap: 0, KeepLatest: 3},
		Retention: RetentionConfig{SessionMonths: 3},
		Behavior:  BehaviorConfig{MaxTokens: 1000, CharsPerToken: 4, Path: "behavior.md"},
		Recall:    RecallConfig{TopN: 3, MaxChars: 1000, MinWord: 4},
		Auth:      AuthConfig{Mode: AuthAll, JudgedTools: []string{"bash", "fetch"}, FreeTools: []string{"grep", "find", "ask", "judge", "recall", "find_file", "find_dir", "help"}, MaxArgChars: 80},
		Params:    ParamsConfig{Temperature: 0.7, TopK: 40, RepetitionPenalty: 1.0, Thinking: true, MaxOutputTokens: 16384},
		// MLX default: the Mac-native server. vLLM users point at :8000/v1.
		Model: ModelConfig{PlainRequestTimeoutSeconds: 300, ToolProtocol: "structured", Runtime: "openai", Server: "http://127.0.0.1:8080/v1"},
		Tools: ToolsConfig{
			GraphQL: GraphQLConfig{MaxRequestBytes: 65536, MaxSelections: 1024, MaxRows: 256, MaxResultBytes: 1048576, TimeoutSeconds: 30},
			Bash: BashConfig{
				DefaultTimeout: 30, MaxTimeout: 600, MaxBytes: 65536, MaxLines: 2000,
			},
			Search: SearchConfig{MaxCaptureBytes: 16777216, MaxMatches: 100, MaxBytes: 65536, MaxLineLength: 1000},
			Fetch:  FetchConfig{TimeoutSeconds: 30, MaxBytes: 262144},
		},
		Agent:     AgentConfig{MaxSteps: 0, LoopRepeat: 3, LoopStall: 5, ThinkingLoop: ThinkingLoopConfig{16, 8, 256, 4096, 8192}},
		Store:     StoreConfig{Path: "data/kuzu", BufferPoolBytes: 1024 * 1024 * 1024},
		Sessions:  SessionsConfig{Path: "data/sessions"},
		Templates: TemplatesConfig{Path: "templates"},
		Subagents: SubagentsConfig{Mode: SubagentsConcurrent, MaxSubagents: 0, ContextWindow: 0, MaxResultChars: 2000},
		Workflows: WorkflowsConfig{MaxRetries: 2, RetryDelaySeconds: 0, VerifyFollowup: true},
		Decision:  DecisionConfig{Runtime: "laya", Server: "http://127.0.0.1:8090", Model: "typed-decisions", Device: "auto", TimeoutSeconds: 30, MaxInputTokens: 15999, RiskThreshold: 0.7, CoherenceMaxChars: 0, JudgmentContextChars: 500, ConfidenceBuckets: []float64{0.5, 0.7, 0.9}},
		Watch:     WatchConfig{Enabled: true, MaxFileBytes: 131072, StartupMaxBytes: 8388608, StartupMaxFiles: 512, StartupMaxEntries: 4096, DebounceMs: 200, MaxEvents: 100, DiffLines: 6, FileLines: 200, AttrWindowS: 30},
		Tabs:      TabsConfig{TitleEvery: 5, TitleTimeoutSeconds: 20},
	}
}

// WatchConfig tunes the diff-view file watcher. Ignore prefixes add
// to the built-ins (.git, data); harness state paths join them too.
type WatchConfig struct {
	MaxFileBytes      int      `yaml:"max_file_bytes"`
	StartupMaxBytes   int      `yaml:"startup_max_bytes"`
	StartupMaxFiles   int      `yaml:"startup_max_files"`
	StartupMaxEntries int      `yaml:"startup_max_entries"` // bounds discovery, including directories and ignored entries
	Enabled           bool     `yaml:"enabled"`
	DebounceMs        int      `yaml:"debounce_ms"`
	MaxEvents         int      `yaml:"max_events"`
	DiffLines         int      `yaml:"diff_lines"`
	FileLines         int      `yaml:"file_lines"`
	AttrWindowS       int      `yaml:"attribute_window_s"`
	Ignore            []string `yaml:"ignore,omitempty"`
}

// MarshalYAML keeps omitted defaults portable for Save and config output.
func (c Config) MarshalYAML() (any, error) {
	type plain Config
	if c.templatesDefault && c.Templates.Path == "templates" {
		c.Templates.Path = ""
	}
	return plain(c), nil
}

// Save writes cfg back to path (the runtime/device switch), failing
// closed: the config re-validates first, so a bad in-memory value
// never clobbers a good file.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("cannot save config: %s", err.Error())
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cannot save config: %v", err)
	}
	if err := CreateWorkspaceConfigDir(path); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}
	if err := writeConfig(path, out); err != nil {
		return fmt.Errorf("cannot save config file %s: %s", path, plainIO(err))
	}
	return nil
}

// Load reads a project file over personal defaults and connections. A missing
// project file retains those defaults with usedFile false. Malformed YAML, unknown fields, and out-of-range
// values are plain-language errors.
func Load(path string) (Config, bool, error) { return load(path, false) }

// LoadProject retains explicit --config precedence even for a managed filename.
func LoadProject(path string) (Config, bool, error) { return load(path, true) }

func load(path string, explicit bool) (cfg Config, usedFile bool, err error) {
	cfg = Defaults()
	cfg.ProjectOverride = explicit
	if err := workspaceDefaults(&cfg); err != nil {
		return Config{}, false, err
	}
	personal, err := loadPreferences(&cfg)
	if err != nil {
		return Config{}, false, err
	}
	if err := loadConnections(&cfg); err != nil {
		return Config{}, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, false, nil
		}
		return Config{}, false, fmt.Errorf("cannot read config file %s: %s", path, plainIO(err))
	}
	// Decode over a copy of the defaults so absent keys keep them.
	// KnownFields rejects typo'd keys instead of silently dropping them.
	merged := cfg
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&merged); err != nil {
		return Config{}, false, fmt.Errorf("config file %s has a problem: %s", path, plainYAML(err))
	}
	// A project selecting another runtime cannot inherit a protocol that only
	// applies to the personal Ollama connection. Explicit protocol still wins.
	var selected struct{ Decision map[string]any }
	if e := yaml.Unmarshal(raw, &selected); e != nil {
		return Config{}, false, e
	}
	if _, hasRuntime := selected.Decision["runtime"]; hasRuntime && merged.Decision.Runtime != cfg.Decision.Runtime {
		if _, hasProtocol := selected.Decision["protocol"]; !hasProtocol {
			merged.Decision.Protocol = cfg.Decision.WithRuntime(merged.Decision.Runtime).Protocol
		}
	}
	// Old automatically saved workspace snapshots must not freeze personal
	// settings once the user has deliberately saved global defaults. Authored
	// project files still overlay globals, preserving explicit project choices.
	if personal && !explicit && ManagedWorkspaceConfig(path) {
		preferences(cfg).apply(&merged)
		merged.Model, merged.Decision, merged.Serve = cfg.Model, cfg.Decision, cfg.Serve
	}
	// Retire the historical fixed trigger without breaking strict loading of old files.
	merged.Context.CompactionAbsoluteTokens = 0
	if err := merged.Validate(); err != nil {
		return Config{}, false, fmt.Errorf("config file %s has a problem: %s", path, err.Error())
	}
	var origin struct{ Templates map[string]any }
	if err := yaml.Unmarshal(raw, &origin); err != nil {
		return Config{}, false, fmt.Errorf("config file %s has a problem: %s", path, plainYAML(err))
	}
	_, templatesExplicit := origin.Templates["path"]
	merged.templatesDefault = !templatesExplicit
	return merged, true, nil
}

// TemplatesDir resolves only an omitted default to shipped executable assets.
// Explicit YAML and custom programmatic paths remain as supplied.
func (c Config) TemplatesDir() string {
	exe, err := os.Executable()
	if err != nil {
		return c.Templates.Path
	}
	return c.templatesDir(exe)
}

func (c Config) templatesDir(exe string) string {
	if c.templatesDefault && c.Templates.Path == "templates" {
		dir := filepath.Join(filepath.Dir(exe), "templates")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return c.Templates.Path
}

// Validate rejects out-of-range values in plain language.
func (c Config) Validate() error {
	if err := c.Coding.ReportContract().Validate(); err != nil {
		return fmt.Errorf("coding: %w", err)
	}
	if c.Coding.ReportFormat != "" && strings.TrimSpace(c.Coding.CheckCommand) == "" {
		return fmt.Errorf("coding report contract requires check_command")
	}

	if err := c.Testing.Validate(); err != nil {
		return err
	}
	for name, v := range map[string]int{"ui.max_file_entries": c.UI.MaxFileEntries, "ui.max_file_view_lines": c.UI.MaxFileViewLines, "ui.max_run_ids": c.UI.MaxRunIDs, "ui.max_decision_rows": c.UI.MaxDecisionRows, "ui.max_alerts": c.UI.MaxAlerts, "ui.max_legacy_messages": c.UI.MaxLegacyMessages} {
		if v <= 0 {
			return fmt.Errorf("%s must be positive, got %d", name, v)
		}
	}
	for name, v := range map[string]int{"tabs.title_timeout_seconds": c.Tabs.TitleTimeoutSeconds, "model.plain_request_timeout_seconds": c.Model.PlainRequestTimeoutSeconds, "serve.probe_timeout_seconds": c.Serve.ProbeTimeoutSeconds} {
		if v <= 0 || v > 9223372036 {
			return fmt.Errorf("%s must be positive representable seconds, got %d", name, v)
		}
	}

	for name, v := range map[string]int{"watch.max_file_bytes": c.Watch.MaxFileBytes, "watch.startup_max_bytes": c.Watch.StartupMaxBytes, "watch.startup_max_files": c.Watch.StartupMaxFiles, "watch.startup_max_entries": c.Watch.StartupMaxEntries} {
		if v <= 0 {
			return fmt.Errorf("%s must be positive, got %d", name, v)
		}
	}
	// Range comparisons alone admit NaN; unbounded positive knobs also
	// admit infinity. Refuse both before applying the existing domains.
	for _, f := range []struct {
		name  string
		value float64
	}{
		{"context.compaction_trigger", c.Context.Trigger},
		{"context.compaction_keep", c.Context.Keep},
		{"context.compaction_summary", c.Context.Summary},
		{"context.compaction_summary_step", c.Context.SummaryStep},
		{"context.compaction_summary_max", c.Context.SummaryMax},
		{"context.summary_words_per_token", c.Context.SummaryWordsPerToken},
		{"context.inflight_outputs", c.Context.InflightOutputs},
		{"schemas.inflight_cap", c.Schemas.InflightCap},
		{"behavior.chars_per_token", c.Behavior.CharsPerToken},
		{"params.temperature", c.Params.Temperature},
		{"params.repetition_penalty", c.Params.RepetitionPenalty},
		{"decision.risk_threshold", c.Decision.RiskThreshold},
	} {
		if math.IsNaN(f.value) || math.IsInf(f.value, 0) {
			return fmt.Errorf("%s must be finite, got %v", f.name, f.value)
		}
	}
	if c.Context.Window < 0 {
		return fmt.Errorf("context.window cannot be negative, got %d", c.Context.Window)
	}
	if c.Context.CompactionAbsoluteTokens < 0 {
		return fmt.Errorf("context.compaction_absolute_tokens must be at least 0, got %d", c.Context.CompactionAbsoluteTokens)
	}
	if c.Context.OutputPruneAfterTokens < 0 {
		return fmt.Errorf("context.output_prune_after_tokens cannot be negative, got %d", c.Context.OutputPruneAfterTokens)
	}
	for name, v := range map[string]float64{
		"context.compaction_trigger":      c.Context.Trigger,
		"context.compaction_keep":         c.Context.Keep,
		"context.compaction_summary":      c.Context.Summary,
		"context.compaction_summary_step": c.Context.SummaryStep,
		"context.compaction_summary_max":  c.Context.SummaryMax,
		"context.inflight_outputs":        c.Context.InflightOutputs,
		"schemas.inflight_cap":            c.Schemas.InflightCap,
	} {
		if v < 0 || v > 100 {
			return fmt.Errorf("%s must be between 0 and 100, got %v", name, v)
		}
	}
	if c.Context.Summary > c.Context.SummaryMax {
		return fmt.Errorf("context.compaction_summary (%v) cannot exceed context.compaction_summary_max (%v)",
			c.Context.Summary, c.Context.SummaryMax)
	}
	if c.Context.Summary <= 0 {
		return fmt.Errorf("context.compaction_summary must be positive, got %v", c.Context.Summary)
	}
	if c.Context.SummaryWordsPerToken <= 0 {
		return fmt.Errorf("context.summary_words_per_token must be positive, got %v", c.Context.SummaryWordsPerToken)
	}
	if c.Schemas.KeepLatest < 3 {
		return fmt.Errorf("schemas.keep_latest must be at least 3, got %d", c.Schemas.KeepLatest)
	}
	if c.Retention.SessionMonths < 0 {
		return fmt.Errorf("retention.session_months cannot be negative, got %d", c.Retention.SessionMonths)
	}
	if c.Behavior.MaxTokens < 0 {
		return fmt.Errorf("behavior.max_tokens cannot be negative, got %d", c.Behavior.MaxTokens)
	}
	if c.Behavior.CharsPerToken <= 0 {
		return fmt.Errorf("behavior.chars_per_token must be positive, got %v", c.Behavior.CharsPerToken)
	}
	if c.Behavior.Path == "" {
		return fmt.Errorf("behavior.path cannot be empty")
	}
	if c.Recall.TopN < 0 {
		return fmt.Errorf("recall.top_n cannot be negative, got %d", c.Recall.TopN)
	}
	if c.Recall.MaxChars < 0 {
		return fmt.Errorf("recall.max_chars cannot be negative, got %d", c.Recall.MaxChars)
	}
	if c.Recall.MinWord < 0 {
		return fmt.Errorf("recall.min_word cannot be negative, got %d", c.Recall.MinWord)
	}
	if err := c.Auth.ValidateLists(); err != nil {
		return err
	}
	switch c.Auth.Mode {
	case AuthYOLO, AuthRisky, AuthAll:
	default:
		return fmt.Errorf("auth.mode must be one of yolo, authorize-risky, authorize-all, got %q", c.Auth.Mode)
	}
	if c.Params.Temperature < 0 {
		return fmt.Errorf("params.temperature cannot be negative, got %v", c.Params.Temperature)
	}
	if c.Params.TopK < 0 {
		return fmt.Errorf("params.top_k cannot be negative, got %d", c.Params.TopK)
	}
	if c.Params.MaxOutputTokens < 0 {
		return fmt.Errorf("params.max_output_tokens cannot be negative, got %d", c.Params.MaxOutputTokens)
	}
	if c.Params.RepetitionPenalty < 0 {
		return fmt.Errorf("params.repetition_penalty cannot be negative, got %v", c.Params.RepetitionPenalty)
	}
	switch c.Model.Runtime {
	case "", "openai", "ollama", "mlx", "rulith": // Empty retains the generic contract for legacy configs.
	default:
		return fmt.Errorf("model.runtime must be openai, ollama, mlx, or rulith, got %q", c.Model.Runtime)
	}
	if u, err := url.ParseRequestURI(c.Model.Server); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("model.server must be an http(s) URL, got %q", c.Model.Server)
	}
	if err := c.Tools.GraphQL.Validate(); err != nil {
		return err
	}
	b := c.Tools.Bash
	if b.DefaultTimeout <= 0 || b.MaxTimeout <= 0 || b.MaxBytes <= 0 || b.MaxLines <= 0 {
		return fmt.Errorf("tools.bash timeouts and caps must all be positive")
	}
	if b.DefaultTimeout > b.MaxTimeout {
		return fmt.Errorf("tools.bash.default_timeout (%d) cannot exceed tools.bash.max_timeout (%d)",
			b.DefaultTimeout, b.MaxTimeout)
	}
	s := c.Tools.Search
	if s.MaxCaptureBytes <= 0 {
		return fmt.Errorf("tools.search.max_capture_bytes must be positive")
	}
	if s.MaxMatches <= 0 || s.MaxBytes <= 0 || s.MaxLineLength <= 0 {
		return fmt.Errorf("tools.search caps must all be positive")
	}
	f := c.Tools.Fetch
	if f.TimeoutSeconds <= 0 || f.MaxBytes <= 0 {
		return fmt.Errorf("tools.fetch timeout and cap must both be positive")
	}
	if c.Model.ToolProtocol != "" && c.Model.ToolProtocol != "structured" && c.Model.ToolProtocol != "text" {
		return fmt.Errorf("model.tool_protocol must be structured or text")
	}
	if c.Agent.MaxRunSeconds > 9223372036 {
		return fmt.Errorf("agent.max_run_seconds exceeds representable duration")
	}
	if c.Agent.MaxRunSeconds < 0 {
		return fmt.Errorf("agent.max_run_seconds cannot be negative")
	}
	t := c.Agent.ThinkingLoop
	if t.Repeat < 0 {
		return fmt.Errorf("agent.thinking_loop.repeat cannot be negative")
	}
	for _, field := range []struct {
		name  string
		value int
	}{{"min_unit_bytes", t.MinUnitBytes}, {"max_unit_bytes", t.MaxUnitBytes}, {"sample_bytes", t.SampleBytes}, {"max_input_bytes", t.MaxInputBytes}} {
		if field.value <= 0 {
			return fmt.Errorf("agent.thinking_loop.%s must be positive", field.name)
		}
	}
	if t.MinUnitBytes > t.MaxUnitBytes {
		return fmt.Errorf("agent.thinking_loop.min_unit_bytes exceeds max_unit_bytes")
	}
	if t.Repeat > t.SampleBytes/t.MaxUnitBytes {
		return fmt.Errorf("agent.thinking_loop.sample_bytes must contain repeat * max_unit_bytes")
	}
	if c.Agent.MaxSteps < 0 {
		return fmt.Errorf("agent.max_steps cannot be negative")
	}
	if c.Agent.LoopRepeat < 0 {
		return fmt.Errorf("agent.loop_repeat cannot be negative, got %d", c.Agent.LoopRepeat)
	}
	if c.Agent.LoopStall < 0 {
		return fmt.Errorf("agent.loop_stall cannot be negative, got %d", c.Agent.LoopStall)
	}
	if c.Store.BufferPoolBytes != 0 && c.Store.BufferPoolBytes < 4096 {
		return fmt.Errorf("store.buffer_pool_bytes must be 0 (automatic) or at least 4096")
	}
	if c.Store.Path == "" {
		return fmt.Errorf("store.path cannot be empty")
	}
	if c.Sessions.Path == "" {
		return fmt.Errorf("sessions.path cannot be empty")
	}
	if c.Templates.Path == "" {
		return fmt.Errorf("templates.path cannot be empty")
	}
	switch c.Subagents.Mode {
	case SubagentsSequential, SubagentsConcurrent:
	default:
		return fmt.Errorf("subagents.mode must be sequential or concurrent, got %q", c.Subagents.Mode)
	}
	if c.Subagents.MaxSubagents < 0 {
		return fmt.Errorf("subagents.max_subagents cannot be negative, got %d", c.Subagents.MaxSubagents)
	}
	if c.Subagents.ContextWindow < 0 {
		return fmt.Errorf("subagents.context_window cannot be negative, got %d", c.Subagents.ContextWindow)
	}
	if c.Subagents.MaxResultChars <= 0 {
		return fmt.Errorf("subagents.max_result_chars must be positive, got %d", c.Subagents.MaxResultChars)
	}
	if c.Workflows.MaxRetries < 0 {
		return fmt.Errorf("workflows.max_retries cannot be negative, got %d", c.Workflows.MaxRetries)
	}
	if c.Workflows.RetryDelaySeconds < 0 {
		return fmt.Errorf("workflows.retry_delay_seconds cannot be negative, got %d", c.Workflows.RetryDelaySeconds)
	}
	if c.Decision.Runtime != "" && c.Decision.Runtime != "laya" && c.Decision.Runtime != "system_one" && c.Decision.Runtime != "openai" && c.Decision.Runtime != "mlx" && c.Decision.Runtime != "ollama" && c.Decision.Runtime != "rulith" {
		return fmt.Errorf("decision.runtime must be laya, system_one, openai, mlx, ollama, or rulith, got %q", c.Decision.Runtime)
	}
	if c.Decision.Protocol != "" && (c.Decision.Protocol != "system_one" || c.Decision.Runtime != "ollama") {
		return fmt.Errorf("decision.protocol must be empty, or system_one with decision.runtime: ollama")
	}
	if c.Decision.Protocol == "system_one" && (c.Decision.Device != "auto" || c.Decision.Path != "" || c.Decision.Revision != "" || c.Decision.TrustRemoteCode) {
		return fmt.Errorf("Ollama owns decision assets and device; use device: auto without path, revision, or trust_remote_code")
	}
	if u, err := url.ParseRequestURI(c.Decision.Server); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("decision.server must be an http(s) URL")
	}
	if strings.TrimSpace(c.Decision.Model) == "" {
		return fmt.Errorf("decision.model must name the checkpoint id")
	}
	switch c.Decision.Device {
	case "auto", "cpu", "gpu":
	default:
		return fmt.Errorf("decision.device must be auto, cpu, or gpu, got %q", c.Decision.Device)
	}
	if c.Decision.MaxInputTokens <= 0 {
		return fmt.Errorf("decision.max_input_tokens must be positive, got %d", c.Decision.MaxInputTokens)
	}
	if c.Decision.TimeoutSeconds <= 0 {
		return fmt.Errorf("decision.timeout_seconds must be positive, got %d", c.Decision.TimeoutSeconds)
	}
	if c.Decision.RiskThreshold < 0 || c.Decision.RiskThreshold > 1 {
		return fmt.Errorf("decision.risk_threshold must be between 0 and 1, got %v", c.Decision.RiskThreshold)
	}
	if c.Decision.CoherenceMaxChars < 0 {
		return fmt.Errorf("decision.coherence_max_chars cannot be negative, got %d", c.Decision.CoherenceMaxChars)
	}
	if c.Decision.JudgmentContextChars < 0 {
		return fmt.Errorf("decision.judgment_context_chars cannot be negative, got %d", c.Decision.JudgmentContextChars)
	}
	for i, e := range c.Decision.ConfidenceBuckets {
		if math.IsNaN(e) || math.IsInf(e, 0) {
			return fmt.Errorf("decision.confidence_buckets[%d] must be finite, got %v", i, e)
		}
		if e <= 0 || e >= 1 {
			return fmt.Errorf("decision.confidence_buckets edge %d must be between 0 and 1 exclusive, got %v", i, e)
		}
		if i > 0 && e <= c.Decision.ConfidenceBuckets[i-1] {
			return fmt.Errorf("decision.confidence_buckets must ascend, got %v", c.Decision.ConfidenceBuckets)
		}
	}
	if c.Auth.MaxArgChars < 0 {
		return fmt.Errorf("auth.max_arg_chars cannot be negative, got %d", c.Auth.MaxArgChars)
	}
	if c.Tabs.TitleEvery < 0 {
		return fmt.Errorf("tabs.title_every cannot be negative, got %d", c.Tabs.TitleEvery)
	}
	for i, model := range c.Serve.Models {
		if model.Name == "" {
			return fmt.Errorf("serve.models[%d] needs a model name", i)
		}
		if u, err := url.ParseRequestURI(model.Server); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("serve.models[%d] needs an http(s) server URL", i)
		}
	}
	recipeNames := map[string]bool{}
	for i, r := range c.Serve.Recipes {
		if recipeNames[r.Name] {
			return fmt.Errorf("serve.recipes[%d] duplicates name %q", i, r.Name)
		}
		recipeNames[r.Name] = true
		if r.Name == "" {
			return fmt.Errorf("serve.recipes[%d] needs a name", i)
		}
		switch r.Kind {
		case "auto", "ollama", "openai", "sidecar", "rulith":
		default:
			return fmt.Errorf("serve.recipes[%d] kind must be auto, ollama, openai, sidecar, or rulith, got %q", i, r.Kind)
		}
		switch r.Runtime {
		case "", "openai", "ollama", "mlx", "rulith", "laya", "system_one":
			if (r.Runtime == "laya" || r.Runtime == "system_one") && r.Kind != "sidecar" {
				return fmt.Errorf("serve.recipes[%d] native decision runtime requires sidecar kind", i)
			}
		default:
			return fmt.Errorf("serve.recipes[%d] runtime must be openai, ollama, mlx, rulith, or a native sidecar runtime, got %q", i, r.Runtime)
		}
		if u, err := url.ParseRequestURI(r.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("serve.recipes[%d] needs an http(s) url", i)
		}
	}
	return nil
}

func plainIO(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "permission denied — check who owns the file"
	}
	return err.Error()
}

func plainYAML(err error) string {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		return "wrong value types: " + typeErr.Error()
	}
	return err.Error()
}
