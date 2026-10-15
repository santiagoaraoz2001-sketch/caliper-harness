// Package auth gates tool execution by mode: yolo runs free,
// authorize-all prompts every call, authorize-risky judges each call
// through the decision model and prompts only when unsure. Declined
// and blocked calls return as Blocked results (not errors) so the model
// adapts instead of crashing and the loop counts no progress.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"specific-harness/internal/prompts"
	"strconv"
	"strings"
	"sync"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/ask"
)

// ValidateRegistry admits configured names against the complete owning registry.
// Child registries and exposure views intentionally omit some valid parent tools.
func ValidateRegistry(a config.AuthConfig, reg *tool.Registry) error {
	for _, list := range []struct {
		key   string
		names []string
	}{{"auth.free_tools", a.FreeTools}, {"auth.judged_tools", a.JudgedTools}} {
		for _, name := range list.names {
			def, ok := reg.Lookup(name)
			if !ok || def.Name != name {
				hint := ""
				if name == "search" {
					hint = "; use grep and find instead"
				}
				return fmt.Errorf("%s tool %q is not registered%s", list.key, name, hint)
			}
		}
	}
	return nil
}

// Judge asks the decision model one question. Main wires it to the
// sidecar; tests stub it.
type Judge func(context.Context, decision.Request) (decision.Verdict, error)

// Gate prompts on In/Out. Judge, Threshold, and the tool lists drive
// authorize-risky; yolo and authorize-all ignore them. MaxArgChars caps
// displayed arg values (0 = unlimited). Log takes one judgment event per
// verdict (nil skips); JudgmentContextChars caps its context (0 = whole).
type Gate struct {
	Prompts     *prompts.Catalog
	Mode        string
	In          io.Reader
	Out         io.Writer
	Judge       Judge
	Threshold   float64
	Judged      []string
	Free        []string
	MaxArgChars int
	// AskFunc answers y/n prompts. Nil means stdin/stdout transport.
	AskFunc              func(ctx context.Context, prompt string) bool
	Log                  func(kind, text string)
	JudgmentContextChars int
	// Configured builders persist exact manual authority before effects.
	Record        func(context.Context, ApprovalEvent) error
	questionsOnce sync.Once
	questions     chan struct{}
	contracts     map[string]string // gate/session only; never restored from history
}

// free reports whether name runs without judgment. Anything not free is
// judged — fail closed toward the decision model, never toward allow.
func (g *Gate) free(name string) bool {
	for _, f := range g.Free {
		if f == name {
			return true
		}
	}
	return false
}

// Ask prompts one y/n question. False on any read trouble.
func Ask(ctx context.Context, in io.Reader, out io.Writer, prompt string) bool {
	if ctx.Err() != nil {
		return false
	}
	fmt.Fprintf(out, "%s [y/n] ", prompt)
	line, err := ask.Lines(in)(ctx)
	if err != nil && len(line) == 0 {
		return false
	}
	return ctx.Err() == nil && isYes(line)
}

// ask answers one y/n prompt through AskFunc, else stdin.
func (g *Gate) askCurrent(ctx context.Context, prompt string) bool {
	if ctx.Err() != nil {
		return false
	}
	if g.AskFunc != nil {
		return g.AskFunc(ctx, prompt) && ctx.Err() == nil
	}
	if g.In == nil || g.Out == nil {
		return false
	}
	return Ask(ctx, g.In, g.Out, prompt) && ctx.Err() == nil
}

// Wrap returns an agent.Exec enforcing the mode around next.
func (g *Gate) Wrap(next agent.Exec) agent.Exec {
	if g.Mode == config.AuthYOLO {
		return next
	}
	if g.Mode == config.AuthRisky {
		return g.risky(next)
	}
	return func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		action, err := encodeAction(def.Name, args)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("blocked: cannot encode complete approval action: %v", err), Blocked: true}, nil
		}
		allowed := g.ask(ctx, manualPrompt(fmt.Sprintf("run %s?", def.Name), args, g.MaxArgChars, action))
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		if err := g.recordAction(ctx, def.Name, action, allowed); err != nil {
			return tool.Result{}, err
		}
		if !allowed {
			return tool.Result{Text: "declined by user", Blocked: true}, nil
		}
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		return g.executeApproved(ctx, def, args, action, next)
	}
}

// risky judges every non-free call: confident verdicts are obeyed, unsure
// verdicts escalate to the user, and anything unwired or unreachable
// blocks the call with an actionable message.
func (g *Gate) risky(next agent.Exec) agent.Exec {
	return func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		if g.free(def.Name) || def.Bound && def.Label == "plan" {
			return next(ctx, def, args)
		}
		if g.Judge == nil {
			return tool.Result{Text: "blocked: authorize-risky has no decision judge wired — set auth.mode to yolo or authorize-all", Blocked: true}, nil
		}
		action, err := encodeAction(def.Name, args)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("blocked: cannot encode complete judgment action: %v", err), Blocked: true}, nil
		}
		req := decision.Request{
			Context:      string(action),
			Instructions: g.Prompts.Text("decision.authorization.instructions"),
			Options:      []string{"allow", "deny"},
		}
		v, err := g.Judge(ctx, req)
		if ctx.Err() != nil {
			return tool.Result{}, ctx.Err()
		}
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("blocked: the decision sidecar refused or is unreachable (%v) — check the configured decision.server and decision.runtime; restore that selected sidecar before retrying", err), Blocked: true}, nil
		}
		if v.Confidence < g.Threshold {
			allowed := g.ask(ctx, manualPrompt(fmt.Sprintf("unsure — leans %s (confidence %.3f) — run %s?", v.Option, v.Confidence, def.Name), args, g.MaxArgChars, action))
			if err := g.recordAction(ctx, def.Name, action, allowed); err != nil {
				return tool.Result{}, err
			}
			if !allowed {
				decision.LogJudgment(g.Log, decision.SourceRiskyAuth, req, v, "asked-denied", g.JudgmentContextChars)
				return tool.Result{Text: "declined by user", Blocked: true}, nil
			}
			if ctx.Err() != nil {
				return tool.Result{}, ctx.Err()
			}
			decision.LogJudgment(g.Log, decision.SourceRiskyAuth, req, v, "asked-allowed", g.JudgmentContextChars)
			return g.executeApproved(ctx, def, args, action, next)
		}
		if v.Option == "deny" {
			decision.LogJudgment(g.Log, decision.SourceRiskyAuth, req, v, "denied", g.JudgmentContextChars)
			return tool.Result{Text: fmt.Sprintf("declined by policy: the decision model judged this %s call too risky (confidence %.2f)", def.Name, v.Confidence), Blocked: true}, nil
		}
		decision.LogJudgment(g.Log, decision.SourceRiskyAuth, req, v, "allowed", g.JudgmentContextChars)
		return g.executeApproved(ctx, def, args, action, next)
	}
}

func encodeAction(name string, args tool.Args) ([]byte, error) {
	return json.Marshal(struct {
		Tool string    `json:"tool"`
		Args tool.Args `json:"args"`
	}{name, args})
}

func manualPrompt(lead string, args tool.Args, max int, action []byte) string {
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, action, "", "  ") // action came from json.Marshal.
	var display strings.Builder
	for _, r := range pretty.String() {
		if r >= 0x7f && r <= 0x9f {
			fmt.Fprintf(&display, "\\u%04x", r)
		} else {
			display.WriteRune(r)
		}
	}
	return lead + "\nPreview: " + formatArgs(args, max) + "\nComplete validated action (JSON):\n" + display.String()
}

func formatArgs(args tool.Args, max int) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprintf("%v", args[k])
		if max > 0 && len(v) > max {
			v = v[:max] + "…"
		}
		parts = append(parts, k+"="+strconv.Quote(v))
	}
	return strings.Join(parts, " ")
}

func isYes(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
