package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"unicode/utf8"
)

var runBudgetReached = errors.New("configured run budget reached")
var userCommentInterrupt = errors.New("generation interrupted for queued user comment")

func (r *Runner) recordUsage(u llm.Usage) {
	if u.Reported {
		r.UsedIn += u.In
		r.UsedOut += u.Out
	} else {
		r.UsageUnknown = true
	}
}

// User steering retains received thought/text but grants no incomplete call
// authority. The full raw tokens/calls remain in the separate partial archive.
func (r *Runner) keepSteered(reply llm.Reply, log func(string, string) error) error {
	if _, err := r.keepPartial(reply, userCommentInterrupt, log); err != nil {
		return err
	}
	return r.retainUnfinished(reply, log)
}

func (r *Runner) retainUnfinished(reply llm.Reply, log func(string, string) error) error {
	message := reply.Message.Clone()
	if len(message.ToolCalls) > 0 {
		raw, err := json.Marshal(message.ToolCalls)
		if err != nil {
			return err
		}
		message.Content += "\nUnverified unfinished native call drafts (none executed):\n" + string(raw)
	}
	if reply.Prefix != nil && reply.Prefix.PendingToolDraft != "" {
		message.Content += "\nUnverified unfinished native argument draft (not executed):\n" + reply.Prefix.PendingToolDraft
	}
	message.ToolCalls = nil
	if message.Size() == 0 {
		return nil
	}
	entry := session.Entry{Message: message, Kind: "reply"}
	candidate := r.Continuation.Clone()
	candidate.Entries = append(candidate.Entries, entry)
	if err := candidate.Snapshot().Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := log("native_reply", string(raw)); err != nil {
		return err
	}
	r.Continuation.Entries = candidate.Entries
	r.Continuation.Invalidate()
	return nil
}

// keepCompleted replaces the just-added text reply without duplicating resume.
func (r *Runner) keepCompleted(reply llm.Reply, log func(string, string) error) error {
	index := len(r.Continuation.Entries) - 1
	candidate := r.Continuation.Clone()
	candidate.Entries[index].Message = reply.Message.Clone()
	if err := candidate.Snapshot().Validate(); err != nil {
		return err
	}
	raw, _ := json.Marshal(reply)
	if r.Session != nil {
		if err := nativeArchiveHeader(map[string]any{"Usage": reply.Usage, "Bytes": len(raw), "RawOnly": false}, "completed native", log); err != nil {
			return err
		}
		if err := nativeArchiveFragments(raw, "completed native", log); err != nil {
			return err
		}
	}
	if err := candidate.Snapshot().Validate(); err != nil {
		return err
	}
	entry, _ := json.Marshal(candidate.Entries[index])
	if err := log("native_reply", string(entry)); err != nil {
		return err
	}
	r.Continuation.Entries = candidate.Entries
	return nil
}

// keepThinking retains original nonexecuted fields, never assistant text.
func (r *Runner) keepThinking(reply llm.Reply, reason string, log func(string, string) error) (string, error) {
	entry := session.Entry{Message: reply.Message.Clone(), Kind: "reply"}
	raw, _ := json.Marshal(entry)
	if err := nativeArchiveHeader(struct {
		Served string
		Usage  llm.Usage
		Bytes  int
	}{reply.Served, reply.Usage, len(raw)}, "thinking", log); err != nil {
		return "", err
	}
	candidate := r.Continuation.Clone()
	candidate.Entries = append(candidate.Entries, entry)
	candidate.Add("failure", "user", "Harness outcome: "+reason, false)
	if err := candidate.Snapshot().Validate(); err != nil {
		return "", err
	}
	if err := log("native_reply", string(raw)); err != nil {
		return "", err
	}
	r.Continuation.Entries = append(r.Continuation.Entries, entry)
	return "", nil
}

// keepPartial archives a refused native response without making it replayable.
func (r *Runner) keepPartial(reply llm.Reply, cause error, log func(string, string) error) (string, error) {
	if r.Session == nil {
		return "original partial fields not archived", fmt.Errorf("partial native response has no session archive; original not retained")
	}
	raw, err := json.Marshal(struct {
		Reply llm.Reply
		Error string
	}{reply, cause.Error()})
	if err != nil {
		return "original partial fields not archived", err
	}
	err = nativeArchiveHeader(struct {
		Partial bool
		Usage   llm.Usage
		Bytes   int
	}{true, reply.Usage, len(raw)}, "partial native", log)
	if err == nil {
		err = nativeArchiveFragments(raw, "partial native", log)
	}
	if err != nil {
		return "original partial archive incomplete", err
	}
	return fmt.Sprintf("original %d-byte native response archived in raw fragments; incomplete fields omitted from bounded resume", len(raw)), nil
}

func nativeArchiveHeader(provenance any, label string, log func(string, string) error) error {
	header, err := json.Marshal(provenance)
	if err != nil {
		return err
	}
	if len(header) > 32<<10 {
		return fmt.Errorf("%s provenance identity exceeds archive header bound; original not archived", label)
	}
	return log("reasoning", string(header))
}

func nativeArchiveFragments(raw []byte, label string, log func(string, string) error) error {
	for offset := 0; offset < len(raw); {
		end := min(offset+(32<<10), len(raw))
		for end < len(raw) && !utf8.RuneStart(raw[end]) {
			end--
		}
		if err := log("reasoning", fmt.Sprintf("offset=%d total=%d\n%s", offset, len(raw), raw[offset:end])); err != nil {
			return fmt.Errorf("%s archive incomplete at byte %d: %w", label, offset, err)
		}
		offset = end
	}
	return nil
}

// ToolModel keeps completed calls separate from executable text compatibility.
type ToolModel interface {
	ChatTools(context.Context, []llm.Message, []llm.ToolDefinition, func(string)) (llm.Reply, error)
}

func structured(m Model) bool {
	if _, ok := m.(ToolModel); !ok {
		return false
	}
	if p, ok := m.(interface{ ToolProtocol() string }); ok {
		return p.ToolProtocol() == "structured"
	}
	return true
}
func (r *Runner) definitions() ([]llm.ToolDefinition, error) {
	var defs []llm.ToolDefinition
	var err error
	if structured(r.Model) {
		defs, err = r.Registry.NativeDefinitions()
		if err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(defs)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(raw))
	bytes := len(raw)
	if len(defs) == 0 {
		bytes = 0
		key = ""
	}
	if r.Continuation.SchemaKey != key {
		r.Continuation.Invalidate()
	}
	r.Continuation.SchemaBytes = bytes
	r.Continuation.SchemaKey = key
	return defs, nil
}
func (r *Runner) nativeStep(ctx context.Context, reply llm.Message, exec Exec, log func(string, string) error, remaining *int, observe func(string, bool, string) string) (obs, stopped string, err error) {
	r.nativeCharges = 0
	ids := map[string]bool{}
	defs := make([]tool.Definition, len(reply.ToolCalls))
	args := make([]tool.Args, len(reply.ToolCalls))
	names := make([]string, len(reply.ToolCalls))
	q := ""
	scopeChange, codingEffect := false, false
	for i, call := range reply.ToolCalls {
		if def, ok := r.Registry.ResolveName(call.Name); ok {
			names[i] = def.Name
		}
		switch names[i] {
		case "workflow_plan", "workflow_approach", "workflow_task", "workflow_focus", "task_start":
			scopeChange = true
		}
		codingEffect = codingEffect || executionTool(names[i])
	}
	if scopeChange && codingEffect {
		q = "scope-changing and coding calls cannot share this array; send separately and wait for each receipt; entire call array refused; no calls ran"
	}
	for i, c := range reply.ToolCalls {
		if c.ID == "" || strings.ContainsAny(c.ID, "\r\n\t ") || c.Name == "" || ids[c.ID] {
			return "", "", fmt.Errorf("uncorrelatable native call IDs/names")
		}
		ids[c.ID] = true
		if names[i] == "plan" && len(reply.ToolCalls) != 1 {
			q = "plan changes the planning phase; call it alone, then wait for its receipt; entire call array refused; no calls ran"
		}
		// Shape errors in argument JSON are repairable refusals via
		// ValidateJSON below; genuinely ambiguous bytes (duplicate keys,
		// trailing data, not one object) have no settled meaning to
		// repair toward and stay hard failures.
		if _, e := tool.JSONObject(c.Arguments); errors.Is(e, tool.ErrAmbiguousArguments) {
			return "", "", e
		}
		def, ok := r.Registry.ResolveName(c.Name)
		if !ok {
			q = fmt.Sprintf("unknown tool %q; entire call array refused; no calls ran", c.Name)
		} else {
			defs[i] = def
			var repair string
			args[i], repair = tool.ValidateJSON(def, c.Arguments)
			if repair == "" {
				defs[i], args[i], repair = prepareInvocation(ctx, def, args[i])
			}
			if repair != "" {
				q = "entire call array refused; no calls ran: " + repair
			}
		}
	}
	if _, found, _ := tool.Extract(reply.Content, r.Prompts); found {
		q = r.Prompts.Text("repair.native.mixed_protocol")
	}
	if remaining != nil && len(reply.ToolCalls) > *remaining {
		q = "native call array exceeds remaining step budget; no calls ran"
		stopped = q
	}
	entry := session.Entry{Message: reply.Clone(), Kind: "reply"}
	candidate := r.Continuation.Clone()
	candidate.Entries = append(candidate.Entries, entry)
	resultStart := len(candidate.Entries)
	for _, c := range reply.ToolCalls {
		candidate.Entries = append(candidate.Entries, session.Entry{Message: llm.Message{Role: "tool", Name: c.Name, ToolCallID: c.ID, Content: "no settled receipt; execution outcome unknown"}, Kind: "failure", Unread: true})
	}
	if e := candidate.Snapshot().Validate(); e != nil {
		return "", "", e
	}
	appendEntry := func(kind string, entry session.Entry) {
		raw, _ := json.Marshal(entry)
		r.Continuation.Entries = append(r.Continuation.Entries, entry)
		if e := log(kind, string(raw)); e != nil {
			err = errors.Join(err, e)
		}
	}
	appendEntry("native_reply", entry)
	tail := ""
	var preparation []string
	for i, c := range reply.ToolCalls {
		r.enforcement.current = nil
		text, sig, productive := q, "", false
		executed := false
		switch {
		case err != nil:
			text = "call not executed: prior settlement failed; see run error"
		case ctx.Err() != nil:
			text = "call not executed: " + ctx.Err().Error()
		case tail != "":
			text = "call not executed: " + tail
		case r.UserCommentPending != nil && r.UserCommentPending():
			tail = "user comment pending; preceding effects settled before steering"
			text = "call not executed: " + tail
		case q == "":
			if e := log("tool", c.Name+" "+c.Arguments); e != nil {
				err = e
				text = "call not executed: session recording failed"
			} else {
				receipt, e := callResolution(c.ID, tool.Call{Name: c.Name}, c.Arguments, defs[i], args[i])
				if e == nil {
					e = log("call_resolution", receipt)
				}
				if e != nil {
					err, text = e, "call not executed: normalized invocation recording failed"
				} else {
					executed = true
					r.nativeCharges++
					text, sig, productive = r.invokeValidated(ctx, defs[i], args[i], exec)
				}
			}
		}
		kind := "output"
		if !productive {
			kind = "failure"
		}
		result := session.Entry{Message: llm.Message{Role: "tool", Name: c.Name, ToolCallID: c.ID, Content: text}, Kind: kind, Unread: true}
		appendEntry("native_result", result)
		if q == "" && preparationTool(names[i]) {
			preparation = append(preparation, r.preparationReceipt(names[i], c.ID, text, executed))
		}
		candidate.Entries[resultStart+i] = result
		candidate.Coding = r.Continuation.Coding
		if e := candidate.Snapshot().Validate(); e != nil {
			err = errors.Join(err, e)
		}
		obs = text
		if r.enforcement.current != nil {
			r.enforcement.current.CallID = c.ID
		}
		if executed && tail == "" && err == nil && ctx.Err() == nil && stopped == "" {
			if msg := observe(sig, productive, ""); msg != "" {
				stopped, tail = msg, "managed stop after call "+c.ID
			}
		}
		if executed && !productive && tail == "" {
			tail = "preceding call " + c.ID + " did not complete successfully"
		}
	}
	if q != "" && err == nil && ctx.Err() == nil && stopped == "" {
		r.nativeCharges = len(reply.ToolCalls) // Preserve charges for calls actually preflighted/refused.
		stopped = observe("", false, q)        // One refused reply, after every correlated receipt.
	}
	if err == nil {
		err = r.settleFocus(log)
	}
	if err == nil && ctx.Err() == nil {
		if q != "" {
			r.preparationDisposition = "no calls ran; no IDs were returned by this reply: " + q
		} else if len(preparation) > 0 {
			r.preparationDisposition = r.Prompts.Text("continuation.native.preparation_header") + strings.Join(preparation, "\n")
		}
	}
	return obs, stopped, err
}
