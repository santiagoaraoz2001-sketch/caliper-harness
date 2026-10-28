package tui

import (
	"fmt"
	"strconv"
	"strings"

	"specific-harness/internal/config"
)

// Only the curated settings bind to config fields. Validation and publication
// remain in applyField; this is not a path-based configuration mutation API.
type settingBinding struct {
	field  SettingField
	target func(*config.Config) any
}

var settingBindings = []settingBinding{
	{SettingField{"model", "server", "model server", "text"}, func(c *config.Config) any { return &c.Model.Server }},
	{SettingField{"model", "name", "model name", "text"}, func(c *config.Config) any { return &c.Model.Name }},
	{SettingField{"model", "runtime", "model runtime", "runtime"}, func(c *config.Config) any { return &c.Model.Runtime }},
	{SettingField{"model", "tool_protocol", "tool protocol", "protocol"}, func(c *config.Config) any { return &c.Model.ToolProtocol }},
	{SettingField{"coding", "check_command", "project check command (empty = recorded proposal)", "text"}, func(c *config.Config) any { return &c.Coding.CheckCommand }},
	{SettingField{"agent", "max_run_seconds", "run seconds (0 = caller budget only)", "int"}, func(c *config.Config) any { return &c.Agent.MaxRunSeconds }},
	{SettingField{"params", "temperature", "temperature", "float"}, func(c *config.Config) any { return &c.Params.Temperature }},
	{SettingField{"params", "top_k", "top_k", "int"}, func(c *config.Config) any { return &c.Params.TopK }},
	{SettingField{"params", "repetition_penalty", "repetition penalty", "float"}, func(c *config.Config) any { return &c.Params.RepetitionPenalty }},
	{SettingField{"params", "thinking", "thinking requested", "bool"}, func(c *config.Config) any { return &c.Params.Thinking }},
	{SettingField{"params", "max_output_tokens", "output tokens including reasoning (0 = runtime policy unknown)", "int"}, func(c *config.Config) any { return &c.Params.MaxOutputTokens }},
	{SettingField{"auth", "mode", "auth mode", "auth"}, func(c *config.Config) any { return &c.Auth.Mode }},
	{SettingField{"context", "window", "context window (0 = auto)", "int"}, func(c *config.Config) any { return &c.Context.Window }},
	{SettingField{"decision", "disabled", "decision model disabled (readiness/risky ask you)", "bool"}, func(c *config.Config) any { return &c.Decision.Disabled }},
	{SettingField{"decision", "server", "decision server URL", "text"}, func(c *config.Config) any { return &c.Decision.Server }},
	{SettingField{"decision", "runtime", "decision runtime", "runtime"}, func(c *config.Config) any { return &c.Decision.Runtime }},
	{SettingField{"decision", "protocol", "decision protocol (empty or system_one for Ollama D1)", "text"}, func(c *config.Config) any { return &c.Decision.Protocol }},
	{SettingField{"decision", "path", "decision assets path (optional)", "text"}, func(c *config.Config) any { return &c.Decision.Path }},
	{SettingField{"decision", "revision", "decision revision (optional)", "text"}, func(c *config.Config) any { return &c.Decision.Revision }},
	{SettingField{"decision", "trust_remote_code", "trust decision remote code", "bool"}, func(c *config.Config) any { return &c.Decision.TrustRemoteCode }},
	{SettingField{"decision", "model", "decision model", "text"}, func(c *config.Config) any { return &c.Decision.Model }},
	{SettingField{"decision", "device", "decision device", "device"}, func(c *config.Config) any { return &c.Decision.Device }},
	{SettingField{"subagents", "mode", "subagent mode", "submode"}, func(c *config.Config) any { return &c.Subagents.Mode }},
	{SettingField{"subagents", "max_subagents", "max subagents (0 = auto)", "int"}, func(c *config.Config) any { return &c.Subagents.MaxSubagents }},
	{SettingField{"subagents", "context_window", "subagent window (0 = inherit)", "int"}, func(c *config.Config) any { return &c.Subagents.ContextWindow }},
	{SettingField{"workflows", "verify_followup", "review runs", "bool"}, func(c *config.Config) any { return &c.Workflows.VerifyFollowup }},
	{SettingField{"workflows", "max_retries", "max retries", "int"}, func(c *config.Config) any { return &c.Workflows.MaxRetries }},
}

func settingTarget(c *config.Config, f SettingField) any {
	for _, binding := range settingBindings {
		if binding.field.Section == f.Section && binding.field.Key == f.Key {
			return binding.target(c)
		}
	}
	return nil
}

func settingRead(target any) string {
	switch v := target.(type) {
	case *string:
		return *v
	case *bool:
		return strconv.FormatBool(*v)
	case *int:
		return strconv.Itoa(*v)
	case *config.RunSeconds:
		return strconv.Itoa(int(*v))
	case *config.OutputTokens:
		return strconv.Itoa(int(*v))
	case *float64:
		return strconv.FormatFloat(*v, 'f', -1, 64)
	}
	return "?"
}

func settingWrite(c *config.Config, f SettingField, val string) error {
	if f.Section == "decision" && f.Key == "runtime" {
		c.Decision = c.Decision.WithRuntime(val)
		return nil
	}
	switch target := settingTarget(c, f).(type) {
	case *string:
		if (f.Section == "model" && (f.Key == "server" || f.Key == "name")) || (f.Section == "decision" && (f.Key == "server" || f.Key == "path" || f.Key == "revision" || f.Key == "model")) {
			val = strings.TrimSpace(val)
		}
		*target = val
	case *bool:
		*target = val == "true"
	case *float64:
		v, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			return err
		}
		*target = v
	case *int:
		v, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return err
		}
		*target = v
	case *config.RunSeconds:
		v, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return err
		}
		*target = config.RunSeconds(v)
	case *config.OutputTokens:
		v, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return err
		}
		*target = config.OutputTokens(v)
	default:
		return fmt.Errorf("unknown setting %s/%s", f.Section, f.Key)
	}
	return nil
}
