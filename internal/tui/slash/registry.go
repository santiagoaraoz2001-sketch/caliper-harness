// Package slash owns the TUI command registry: parse, completion, and
// help for every /command. Views share this one registry and supply
// completion context (Context); the registry never imports views.
package slash

import (
	"fmt"
	"strings"
)

// Command is one slash command. View names the view it navigates to
// ("" for actions the app handles). Lands names the task wiring an
// action ("" when live). Args lists fixed argument options; a bare
// submit opens them as a menu.
type Command struct {
	Name  string
	Desc  string
	Usage string
	View  string
	Lands string
	Args  []string
}

// Context carries the active view's completion state: RefPrefix
// pre-fills /comment's task ref (the plan tree supplies it).
type Context struct {
	View      string
	RefPrefix string
}

// Commands is the full registry in help order.
func Commands() []Command {
	return []Command{
		{Name: "chat", Desc: "talk to the agent", Usage: "/chat", View: "chat"},
		{Name: "diff", Desc: "live file diffs", Usage: "/diff", View: "diff"},
		{Name: "plans", Desc: "plan/approach/task trees", Usage: "/plans", View: "plans"},
		{Name: "sessions", Desc: "browse past runs", Usage: "/sessions [id]", View: "sessions"},
		{Name: "decisions", Desc: "decision model activity", Usage: "/decisions", View: "decisions"},
		{Name: "workflow", Desc: "explicitly focus one owning workflow node", Usage: "/workflow [root|qualified-ref|UUID]"},
		{Name: "workflows", Desc: "approach launches + retries", Usage: "/workflows", View: "workflows"},
		{Name: "files", Desc: "browse files", Usage: "/files", View: "files"},
		{Name: "settings", Desc: "edit configuration", Usage: "/settings", View: "settings"},
		{Name: "status", Desc: "read-only runtime dashboard", Usage: "/status", View: "status"},
		{Name: "manage", Desc: "Models & Servers: saved connections and controls", Usage: "/manage", View: "manage"},
		{Name: "models", Desc: "Models & Servers shortcut", Usage: "/models [exact name/path]", View: "manage"},
		{Name: "new", Desc: "new session", Usage: "/new"},
		{Name: "stop", Desc: "stop the run", Usage: "/stop"},
		{Name: "retry", Desc: "re-run last prompt", Usage: "/retry"},
		{Name: "btw", Desc: "interject the running model", Usage: "/btw <text>"},
		{Name: "tools", Desc: "coding/full tool exposure; checks remain required", Usage: "/tools [coding|full]", Args: []string{"coding", "full"}},
		{Name: "auth", Desc: "switch auth mode", Usage: "/auth <yolo|risky|all>", Args: []string{"yolo", "risky", "all"}},
		{Name: "recall", Desc: "search memories", Usage: "/recall <query>"},
		{Name: "remember", Desc: "save a memory", Usage: "/remember <text>"},
		{Name: "compact", Desc: "compact context now", Usage: "/compact"},
		{Name: "context", Desc: "show context usage", Usage: "/context"},
		{Name: "clear", Desc: "clear chat view", Usage: "/clear"},
		{Name: "comment", Desc: "queue comment for next turn", Usage: "/comment [ref] [lines] <text>"},
		{Name: "goto", Desc: "jump the open viewer to a line", Usage: "/goto <line>"},
		{Name: "open-diff", Desc: "open retained change in file viewer", Usage: "/open-diff [event]"},
		{Name: "changes", Desc: "recent observed history for the open file", Usage: "/changes [path]"},
		{Name: "turns", Desc: "scroll to an earlier turn or return to latest", Usage: "/turns [-N|0|current]"},
		{Name: "help", Desc: "this list", Usage: "/help"},
		{Name: "update", Desc: "check and install an update for next launch", Usage: "/update"},
		{Name: "setup", Desc: "choose an existing model and project check", Usage: "/setup"},
		{Name: "quit", Desc: "exit", Usage: "/quit"},
	}
}

// Registry answers parse, completion, and help.
type Registry struct {
	byName map[string]Command
	order  []Command
}

// New builds the registry over Commands.
func New() *Registry {
	r := &Registry{byName: map[string]Command{}}
	r.order = Commands()
	for _, c := range r.order {
		r.byName[c.Name] = c
	}
	return r
}

// Parse splits "/name args..." into its command plus args. Names are
// case-sensitive; anything else reports found=false.
func (r *Registry) Parse(input string) (Command, []string, bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return Command{}, nil, false
	}
	c, ok := r.byName[strings.TrimPrefix(fields[0], "/")]
	if !ok {
		return Command{}, nil, false
	}
	if c.Name == "models" || c.Name == "changes" {
		rest := strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(input, " \t\r\n"), fields[0]), " \t")
		if rest != "" {
			return c, []string{rest}, true
		}
		return c, nil, true
	}
	return c, fields[1:], true
}

// Complete suggests display strings for a partial input: command
// names before the first space, the context ref for /comment after
// it. Anything else completes to nothing.
func (r *Registry) Complete(input string, ctx Context) []string {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	if head, rest, ok := strings.Cut(input, " "); ok {
		if head == "/comment" && ctx.RefPrefix != "" {
			if rest == "" || strings.HasPrefix(ctx.RefPrefix, strings.TrimSpace(rest)) {
				return []string{ctx.RefPrefix}
			}
		}
		if c, ok := r.byName[strings.TrimPrefix(head, "/")]; ok && len(c.Args) > 0 {
			tok := strings.TrimSpace(rest)
			var out []string
			for _, a := range c.Args {
				if a == tok {
					return nil // complete arg: enter sends
				}
				if strings.HasPrefix(a, tok) {
					out = append(out, a)
				}
			}
			return out
		}
		return nil
	}
	var out []string
	for _, c := range r.order {
		if strings.HasPrefix("/"+c.Name, input) {
			out = append(out, "/"+c.Name)
		}
	}
	return out
}

// Help renders the grouped command list.
func (r *Registry) Help() string {
	groups := []struct {
		title string
		names []string
	}{
		{"VIEWS", []string{"chat", "diff", "plans", "sessions", "decisions", "workflows", "files", "settings", "status", "manage"}},
		{"MODELS", []string{"models"}},
		{"RUN", []string{"new", "stop", "retry", "btw", "tools", "workflow"}},
		{"AUTH", []string{"auth"}},
		{"MEMORY", []string{"recall", "remember"}},
		{"CONTEXT", []string{"compact", "context", "clear", "turns"}},
		{"COMMENT", []string{"comment", "goto"}},
		{"CHANGES", []string{"open-diff", "changes"}},
		{"META", []string{"help", "setup", "update", "quit"}},
	}
	var b strings.Builder
	for gi, g := range groups {
		if gi > 0 {
			b.WriteString("\n")
		}
		b.WriteString(g.title + "\n")
		for _, n := range g.names {
			c := r.byName[n]
			fmt.Fprintf(&b, "  %-10s %s\n", "/"+c.Name, c.Desc)
		}
	}
	return b.String()
}
