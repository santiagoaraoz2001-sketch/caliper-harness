package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

// ApprovalEvent records exact manual authority, not source/check qualification.
type ApprovalEvent struct {
	Kind, Mode, Scope, ID, Snapshot, Brief string
	Store, Workspace, Session              string
	Approved                               bool
	Cancelled                              bool
}

// ConfirmReadiness records the exact inspected snapshot without granting any
// execution authority. Cancellation and receipt failure cannot look approved.
func (g *Gate) ConfirmReadiness(ctx context.Context, id, snapshot, brief, prompt string) (bool, error) {
	if snapshot == "" || g.Record == nil {
		return false, fmt.Errorf("readiness needs an exact snapshot and durable approval sink")
	}
	approved := g.Confirm(ctx, prompt)
	cancelled := ctx.Err() != nil
	if cancelled {
		approved = false
	}
	event := ApprovalEvent{Kind: "readiness", Mode: g.Mode, Scope: "approach", ID: id, Snapshot: snapshot, Brief: brief, Approved: approved, Cancelled: cancelled}
	if err := g.Record(context.WithoutCancel(ctx), event); err != nil {
		return false, fmt.Errorf("readiness decision recording failed: %w", err)
	}
	return approved, ctx.Err()
}

func (g *Gate) question(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.questionsOnce.Do(func() { g.questions = make(chan struct{}, 1) })
	select {
	case g.questions <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-g.questions }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// Confirm is the single serialized, cancellable human transport shared by roles.
func (g *Gate) Confirm(ctx context.Context, prompt string) bool {
	release, err := g.question(ctx)
	if err != nil {
		return false
	}
	defer release()
	return g.askCurrent(ctx, prompt)
}
func (g *Gate) ask(ctx context.Context, prompt string) bool { return g.Confirm(ctx, prompt) }
func (g *Gate) record(ctx context.Context, event ApprovalEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.Record != nil {
		return g.Record(ctx, event)
	}
	return nil
}

// ApproveContract binds Risky authority to exact current owner readback. All
// gates each actual action in Wrap, without re-asking for internal receipt writes.
func (g *Gate) ApproveContract(ctx context.Context, kind, id string, load func(context.Context) (string, string, error)) error {
	if g.Mode != config.AuthRisky {
		return ctx.Err()
	}
	release, err := g.question(ctx)
	if err != nil {
		return err
	}
	defer release()
	snapshot, brief, err := load(ctx)
	if err != nil {
		return err
	}
	key := kind + ":" + id
	if snapshot == "" {
		return fmt.Errorf("%s %s has no current contract snapshot", kind, id)
	}
	if g.contracts == nil {
		g.contracts = make(map[string]string)
	}
	if g.contracts[key] != snapshot {
		approved := g.askCurrent(ctx, "Approve completed "+kind+" "+id+" contract?\n"+brief)
		if err := g.record(ctx, ApprovalEvent{Kind: "contract", Mode: g.Mode, Scope: kind, ID: id, Snapshot: snapshot, Brief: brief, Approved: approved}); err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("%s %s contract declined; work remains unadmitted", kind, id)
		}
	}
	current, _, err := load(ctx)
	if err != nil {
		return err
	}
	if current != snapshot {
		return fmt.Errorf("%s %s contract changed during approval; inspect and approve its current brief", kind, id)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g.contracts[key] = snapshot
	return nil
}
func (g *Gate) recordAction(ctx context.Context, name string, action []byte, approved bool) error {
	return g.record(ctx, ApprovalEvent{Kind: "tool", Mode: g.Mode, Scope: name, Snapshot: fmt.Sprintf("%x", sha256.Sum256(action)), Brief: string(action), Approved: approved})
}

type approvalKey struct{}
type permittedAction struct {
	name string
	raw  string
}

// Approved reports this exact owning action only; it is never a child grant.
func Approved(ctx context.Context, name string, args tool.Args) bool {
	granted, ok := ctx.Value(approvalKey{}).(permittedAction)
	if !ok || granted.name != name {
		return false
	}
	raw, err := encodeAction(name, args)
	return err == nil && string(raw) == granted.raw
}
func (g *Gate) executeApproved(ctx context.Context, def tool.Definition, args tool.Args, action []byte, next agent.Exec) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	current, err := encodeAction(def.Name, args)
	if err != nil {
		return tool.Result{}, err
	}
	if string(current) != string(action) {
		return tool.Result{Blocked: true, Text: "validated action changed during authorization; no tool executed"}, nil
	}
	return next(context.WithValue(ctx, approvalKey{}, permittedAction{def.Name, string(action)}), def, args)
}
