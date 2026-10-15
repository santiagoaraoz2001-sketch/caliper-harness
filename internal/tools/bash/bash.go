// Package bash implements the bash tool: managed shell execution with
// timeouts and capped output. Harness failures (timeout, spawn) are errors;
// a command's own non-zero exit rides in the Result with its code.
package bash

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/process"
)

// New returns the bash definition bound to cfg caps.
func New(cfg config.BashConfig, catalogs ...*prompts.Catalog) tool.Definition {
	return NewAt(cfg, "", catalogs...)
}

func NewAt(cfg config.BashConfig, dir string, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	return tool.Definition{Prompts: pc,
		Name:    "bash",
		Label:   "bash",
		Desc:    pc.Text("tools.bash.description"),
		Purpose: pc.Text("tools.bash.purpose"),
		Params: []tool.Param{
			{Name: "cmd", Desc: pc.Text("tools.bash.parameters.cmd"), Type: tool.TypeString, Required: true},
			{Name: "timeout", Desc: pc.Text("tools.bash.parameters.timeout"), Type: tool.TypeInt, Default: fmt.Sprint(cfg.DefaultTimeout)},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			return run(ctx, cfg, args, dir, pc)
		},
	}
}

func run(parent context.Context, cfg config.BashConfig, args tool.Args, dir string, catalogs ...*prompts.Catalog) (tool.Result, error) {
	pc := prompts.Select(catalogs...)
	cmdStr, _ := args["cmd"].(string)
	timeout, _ := args["timeout"].(int64)
	if timeout <= 0 {
		timeout = int64(cfg.DefaultTimeout)
	}
	if timeout > int64(cfg.MaxTimeout) {
		return tool.Result{}, fmt.Errorf("timeout %ds exceeds the %ds limit (tools.bash.max_timeout) — %s", timeout, cfg.MaxTimeout, pc.Text("guidance.bash.timeout_cap"))
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := process.Command(ctx, "bash", "-c", cmdStr)
	cmd.Dir = dir
	out := cappedOutput{maxBytes: cfg.MaxBytes}
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := process.Run(cmd)
	text, truncated := capOutput(out.buf.String(), cfg.MaxBytes, cfg.MaxLines, out.truncated, pc)
	if cause := ctx.Err(); cause != nil {
		return tool.Result{Text: text, Truncated: truncated, Failed: true}, fmt.Errorf("%s: %w — retained partial output; %s (max %ds)", interruption(cause, timeout), cause, pc.Text("guidance.bash.interrupted"), cfg.MaxTimeout)
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			text += fmt.Sprintf("\n[exit code %d]", exit.ExitCode())
			code := exit.ExitCode()
			return tool.Result{Text: text, Truncated: truncated, Failed: true, ExitCode: &code}, nil
		}
		return tool.Result{}, fmt.Errorf("cannot run command: %v — %s", err, pc.Text("guidance.bash.missing_shell"))
	}
	code := 0
	return tool.Result{Text: text, Truncated: truncated, ExitCode: &code}, nil
}

func interruption(cause error, timeout int64) string {
	if cause == context.DeadlineExceeded {
		return fmt.Sprintf("command timed out after %ds", timeout)
	}
	return "command cancelled"
}

// cappedOutput keeps the prefix while draining every write. The same writer is
// used for both streams, so os/exec serializes their writes through one pipe.
type cappedOutput struct {
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

func (o *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, o.maxBytes-o.buf.Len())
	if len(p) > remaining {
		p = p[:remaining]
		o.truncated = true
	}
	_, _ = o.buf.Write(p)
	return n, nil
}

// capOutput cuts text to maxBytes and maxLines, marking the cut plainly.
func capOutput(text string, maxBytes, maxLines int, truncated bool, catalogs ...*prompts.Catalog) (string, bool) {
	pc := prompts.Select(catalogs...)
	if len(text) > maxBytes {
		text = text[:maxBytes]
		truncated = true
	}
	if lines := strings.Count(text, "\n"); lines > maxLines {
		cut := 0
		for i := 0; i < maxLines; i++ {
			cut += strings.Index(text[cut:], "\n") + 1
		}
		text = text[:cut]
		truncated = true
	}
	if truncated {
		text += fmt.Sprintf("\n[output truncated: showing within %s / %d lines — %s]",
			formatSize(maxBytes), maxLines, pc.Text("guidance.bash.truncated"))
	}
	return text, truncated
}

func formatSize(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	}
	if n >= 1<<10 {
		return fmt.Sprintf("%dKB", n/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}
