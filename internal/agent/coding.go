package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

var CodingTools = []string{"read", "read_outside", "find_outside", "edit", "write", "bash", "grep", "find_file", "find_dir", "help", "ask", "check"}

// FullToolNames is a display of registered public names, not another admission policy.
func ToolNames(view string) []string {
	if view != "full" {
		return append([]string(nil), CodingTools...)
	}
	return append(append([]string(nil), CodingTools...), "graphql", "fetch", "judge", "recall", "launch", "workflow_plan", "workflow_approach", "workflow_task", "workflow_block", "workflow_ready", "workflow_transition", "workflow_status", "workflow_pause", "workflow_retire", "workflow_resume")
}
func (r *Runner) codingStart() {
	if !r.CodingRequired {
		return
	}
	c := r.Continuation.Coding
	if c == nil || c.Closed || !r.assignedClaim && c.Attempt > 0 {
		c = &session.CodingState{TaskID: session.NewID(), Workspace: r.Workspace}
		r.Continuation.Coding = c
	}
	if c.Workspace != r.Workspace || c.ConfigCommand != r.CheckCommand {
		c.Invalidate("workspace or configured check changed")
	}
	c.Workspace = r.Workspace
	c.ConfigCommand = r.CheckCommand
	if strings.TrimSpace(r.CheckCommand) != "" {
		c.Command = r.CheckCommand
		c.Source = "configured"
	}
}
func (r *Runner) codingRecord(ctx context.Context) error {
	if r.Session == nil {
		return fmt.Errorf("coding check requires a durable session receipt")
	}
	raw, e := json.Marshal(r.Continuation.Coding)
	if e == nil {
		e = r.Session.Append(context.WithoutCancel(ctx), r.SessionID, "coding", string(raw))
	}
	if e != nil {
		r.codingErr = e
	}
	return e
}

// CodingExec belongs inside the approval gate: only admitted work invalidates.
func (r *Runner) CodingExec(next Exec) Exec {
	return func(ctx context.Context, d tool.Definition, a tool.Args) (tool.Result, error) {
		// Recheck after approval: an intervening revision/state change grants no effect.
		if err := r.codingAdmission(ctx, d); err != nil {
			return tool.Result{}, err
		}
		c := r.Continuation.Coding
		if c == nil {
			return next(ctx, d, a)
		}
		if d.Name == "bash" || d.Name == "launch" || d.Name == "workflow_resume" {
			c.Invalidate("opaque Bash or child workflow admitted")
			if e := r.codingRecord(ctx); e != nil {
				return tool.Result{}, e
			}
		}
		res, e := next(ctx, d, a)
		if d.Name == "edit" || d.Name == "write" {
			r.observeFile(res, e)
		}
		if (d.Name == "edit" || d.Name == "write") && res.Changed {
			c.Invalidate("file mutation or uncertain mutation outcome")
			if saved := r.codingRecord(ctx); e == nil {
				e = saved
			}
		}
		return res, e
	}
}
func (r *Runner) CheckDefinition(bash tool.Definition, acceptanceCommand ...string) tool.Definition {
	params := append([]tool.Param(nil), bash.Params...)
	params[0].Required = strings.TrimSpace(r.CheckCommand) == ""
	params[0].Default = r.CheckCommand
	if params[0].Required {
		params[0].Default = ""
	} else {
		params[0].AllowEmpty = true
		params[0].Enum = []string{r.CheckCommand}
		params[0].ExactEnum = true
	}
	acceptance := ""
	if c := r.Continuation.Coding; c != nil && (r.Assigned == nil || r.assignedClaim) {
		acceptance = c.AcceptanceCommand
	}
	if len(acceptanceCommand) == 1 {
		acceptance = acceptanceCommand[0]
	}
	if acceptance != "" || r.assignedClaim {
		params = append(params, tool.Param{Name: "acceptance_cmd", Desc: r.Prompts.Text("tools.check.parameters.acceptance_cmd"), Type: tool.TypeString, Default: acceptance, Required: acceptance == "", ExactEnum: true, Enum: []string{acceptance}})
	}
	return tool.Definition{Name: "check", Desc: r.Prompts.Text("tools.check.description"), Purpose: r.Prompts.Text("tools.check.purpose"), Params: params, Execute: func(ctx context.Context, a tool.Args) (tool.Result, error) {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		c := r.Continuation.Coding
		cmd, _ := a["cmd"].(string)
		acceptance, _ := a["acceptance_cmd"].(string)
		if c == nil {
			return tool.Result{}, fmt.Errorf("ordinary coding state is unavailable")
		}
		paired := c.AcceptanceCommand != "" || c.Attempt > 0
		if paired && (strings.TrimSpace(c.AcceptanceCommand) == "" || acceptance != c.AcceptanceCommand) {
			return tool.Result{}, fmt.Errorf("check acceptance_cmd must match the frozen task command exactly; legacy attempts require readmission")
		}
		if strings.TrimSpace(cmd) == "" || strings.TrimSpace(r.CheckCommand) != "" && cmd != r.CheckCommand {
			return tool.Result{}, fmt.Errorf("check cmd must match the configured command exactly, or provide a nonblank proposal when unset")
		}
		c.Command, c.Source = cmd, "proposed"
		if r.CheckCommand != "" {
			c.Source = "configured"
		}
		if r.Assigned != nil && r.assignedClaim {
			c.Source = "assigned"
		}
		c.Invalidate("verification episode admitted")
		receipt := &session.CheckReceipt{Command: cmd, Cwd: c.Workspace, Revision: c.Revision, EpisodeStarted: time.Now().UTC().Format(time.RFC3339Nano), Status: "unexecuted", Reason: "configured suite not executed", Acceptance: &session.CommandReceipt{Command: acceptance, Cwd: c.Workspace, Status: "unexecuted"}}
		if !paired {
			receipt.Direct = true
			receipt.Acceptance = nil
		}
		c.Receipt = receipt
		if e := r.codingRecord(ctx); e != nil {
			return tool.Result{}, e
		}
		if err := ctx.Err(); err != nil {
			receipt.EpisodeEnded = time.Now().UTC().Format(time.RFC3339Nano)
			return tool.Result{}, errors.Join(err, r.codingRecord(ctx))
		}
		args := make(tool.Args)
		for key, value := range a {
			if key != "acceptance_cmd" {
				args[key] = value
			}
		}
		var first tool.Result
		var err error
		ar := receipt.Acceptance
		if paired {
			args["cmd"] = acceptance
			ar.Started, ar.Status = time.Now().UTC().Format(time.RFC3339Nano), "incomplete"
			first, err = Direct(ctx, bash, args)
			ar.Ended, ar.ExitCode, ar.Text, ar.Truncated = time.Now().UTC().Format(time.RFC3339Nano), first.ExitCode, first.Text, first.Truncated
			ar.Status, ar.Reason = checkStatus(first, err)
			if !ar.Passed() || ctx.Err() != nil {
				receipt.EpisodeEnded = ar.Ended
			}
			if saved := r.codingRecord(ctx); saved != nil {
				receipt.Status = "unverified"
				receipt.EpisodeEnded = time.Now().UTC().Format(time.RFC3339Nano)
				return first, errors.Join(err, saved)
			}
			if !ar.Passed() || ctx.Err() != nil {
				if receipt.EpisodeEnded == "" {
					receipt.EpisodeEnded = time.Now().UTC().Format(time.RFC3339Nano)
					err = errors.Join(err, r.codingRecord(ctx))
				}
				if err == nil {
					err = ctx.Err()
				}
				first.Text = "Acceptance: " + ar.Reason + "\n" + first.Text + "\nConfigured suite: unexecuted"
				return first, err
			}
		}
		receipt.Started, receipt.Status = time.Now().UTC().Format(time.RFC3339Nano), "incomplete"
		if e := r.codingRecord(ctx); e != nil {
			receipt.Started, receipt.Status, receipt.Reason = "", "unexecuted", "configured suite not executed: "+e.Error()
			receipt.EpisodeEnded = time.Now().UTC().Format(time.RFC3339Nano)
			return first, e
		}
		args["cmd"] = cmd
		res, err := Direct(ctx, bash, args)
		receipt.Ended, receipt.ExitCode, receipt.Text, receipt.Truncated = time.Now().UTC().Format(time.RFC3339Nano), res.ExitCode, res.Text, res.Truncated
		receipt.EpisodeEnded = receipt.Ended
		receipt.Status, receipt.Reason = checkStatus(res, err)
		if ctx.Err() != nil {
			receipt.Status, receipt.Reason, err = "unverified", ctx.Err().Error(), ctx.Err()
		}
		if saved := r.codingRecord(ctx); saved != nil {
			receipt.Status = "unverified"
			err = errors.Join(err, saved)
		}
		if paired {
			res.Text = "Acceptance: " + ar.Reason + "\n" + ar.Text + "\nConfigured suite: " + receipt.Reason + "\n" + res.Text
			res.Truncated = res.Truncated || ar.Truncated
		} else {
			res.Text = "Declared check: " + receipt.Reason + "\n" + res.Text
		}
		return res, err
	}}
}

func checkStatus(res tool.Result, err error) (string, string) {
	if err != nil {
		return "incomplete", err.Error()
	}
	if res.Blocked || res.ExitCode == nil {
		return "incomplete", "command has no complete exit receipt"
	}
	if *res.ExitCode != 0 || res.Failed {
		return "failed", fmt.Sprintf("command exited%d", *res.ExitCode)
	}
	return "passed", "declared check passed; command exited0, coverage unknown"
}

// RetainHostCheck keeps the factory definition without advertising it to the model.
func (r *Runner) RetainHostCheck(def tool.Definition) { r.hostCheckDefinition = def }

// hostCheck uses the same validated, approved check path; it is not a model call.
func (r *Runner) hostCheck(ctx context.Context, exec Exec, log func(string, string) error) (string, error) {
	def := r.hostCheckDefinition
	if def.Execute == nil && r.Registry != nil {
		def, _ = r.Registry.Lookup("check")
	}
	if def.Execute == nil {
		return "", fmt.Errorf("host configured check is unavailable")
	}
	raw, _ := json.Marshal(map[string]string{"cmd": r.CheckCommand})
	args, repair := tool.ValidateJSON(def, string(raw))
	if repair != "" {
		return "", fmt.Errorf("host configured check refused: %s", repair)
	}
	if err := log("host_check", r.Prompts.Text("verification.host_finalization")); err != nil {
		return "", err
	}
	obs, _, _ := r.invokeValidated(ctx, def, args, exec)
	return obs, log("result", "Host verification episode:\n"+obs)
}

func (r *Runner) codingOutcome() (outcome.Status, string) {
	c := r.Continuation.Coding
	if r.codingErr != nil {
		return outcome.Failed, "coding receipt recording failed: " + r.codingErr.Error()
	}
	if c.Passed() {
		return outcome.Completed, "declared check passed; coverage/test count unknown"
	}
	if c != nil && c.Receipt != nil && c.Receipt.Acceptance != nil && c.Receipt.Acceptance.Status == "failed" {
		return outcome.Failed, "behavioral acceptance failed: " + c.Receipt.Acceptance.Reason
	}
	if c != nil && c.Receipt != nil && c.Receipt.Status == "failed" {
		return outcome.Failed, c.Receipt.Reason
	}
	return outcome.Stopped, r.Prompts.Text("verification.current_check_missing")
}

// Transient, bounded publication comparisons; not a current filesystem inventory.
type fileEvidence struct {
	paths        map[string]tool.FileEffect
	publications int
	partial      bool
}

func (r *Runner) observeFile(res tool.Result, err error) {
	f := &r.fileEvidence
	f.partial = f.partial || err != nil || res.Failed
	if !res.Changed {
		return
	}
	f.publications++
	e := res.FileEffect
	if e == nil || e.Path == "" || len(e.Path) > 4096 || len(e.BeforeHash) != 64 || len(e.AfterHash) != 64 {
		f.partial = true
		return
	}
	if f.paths == nil {
		f.paths = make(map[string]tool.FileEffect)
	}
	if prior, ok := f.paths[e.Path]; ok {
		f.partial = f.partial || prior.AfterHash != e.BeforeHash || e.Created
		prior.AfterHash = e.AfterHash
		f.paths[e.Path] = prior
	} else if len(f.paths) < 8 {
		f.paths[e.Path] = *e
	} else {
		f.partial = true
	}
}
func evidenceQuote(s string) string {
	runes := []rune(s)
	if len(runes) > 160 {
		s = string(runes[:160]) + "…[display omitted]"
	}
	return fmt.Sprintf("%q", s)
}
func (r *Runner) completionEvidence() string {
	if !r.CodingRequired {
		return ""
	}
	f := &r.fileEvidence
	keys := make([]string, 0, len(f.paths))
	for p := range f.paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	labels := make([]string, 0, len(keys))
	net := 0
	for _, p := range keys {
		e := f.paths[p]
		kind := "reverted"
		if e.Created {
			kind = "created"
		} else if e.BeforeHash != e.AfterHash {
			kind = "changed"
		}
		if kind != "reverted" {
			net++
		}
		labels = append(labels, evidenceQuote(p)+":"+kind)
	}
	text := fmt.Sprintf(" File-tool publications this run: publications=%d tracked=%d net=%d partial=%t [%s]; first-before versus last-publication comparison only, not current filesystem. Opaque Bash/check/workflow effects not inventoried.", f.publications, len(keys), net, f.partial, strings.Join(labels, ", "))
	c := r.Continuation.Coding
	if c != nil && c.Receipt != nil {
		exit := "unknown"
		if c.Receipt.ExitCode != nil {
			exit = fmt.Sprint(*c.Receipt.ExitCode)
		}
		if a := c.Receipt.Acceptance; a != nil {
			acceptanceExit := "unknown"
			if a.ExitCode != nil {
				acceptanceExit = fmt.Sprint(*a.ExitCode)
			}
			text += fmt.Sprintf(" Behavioral acceptance cmd=%s status=%s exit=%s observations=%s; episode current=%t.", evidenceQuote(a.Command), evidenceQuote(a.Status), acceptanceExit, evidenceQuote(a.Text), c.Passed())
		}
		text += fmt.Sprintf(" Declared check source=%s cmd=%s cwd=%s status=%s exit=%s current=%t; coverage/requested-task correctness unverified.", evidenceQuote(c.Source), evidenceQuote(c.Receipt.Command), evidenceQuote(c.Receipt.Cwd), evidenceQuote(c.Receipt.Status), exit, c.Passed())
	}
	return text
}
