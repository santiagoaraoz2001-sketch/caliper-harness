// Package tool is the tool registry plus the slot protocol.
//
// The model emits exactly one fenced block naming a tool and filling scalar
// slots as key: value (or key = value) lines:
//
//	```tool
//	tool: bash
//	cmd: ls -la
//	```
//
// For verbatim multiline content, use key: <<END, the raw content lines,
// then an exact unindented END line. Each content line keeps its newline,
// including CRLF, indentation, trailing spaces, comments and blank lines.
// Choose a different letter/digit/underscore label if END occurs on a line
// of its own in the content. Fence-looking lines inside a literal are data.
// Unmarked continuation lines and duplicate fields refuse, never merge.
// The harness refuses multiple blocks before any effects, then validates
// the one call against its schema. Each gap produces a targeted repair.
package tool

import (
	"context"
	"fmt"
	"sort"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"strings"
)

// Param types.
const (
	TypeString = "string"
	TypeInt    = "int"
	TypeFloat  = "float"
	TypeBool   = "bool"
)

// Param is one fillable slot.
type Param struct {
	Name       string
	Desc       string
	Type       string // one of TypeString/Int/Float/Bool
	Required   bool
	ExactEnum  bool     // command-like values require byte-exact enum membership
	AllowEmpty bool     // explicit empty string is data; presence is still required
	Default    string   // applied when the slot is absent (optional params)
	Enum       []string // allowed values, empty means any
}

// Args are validated, coerced slot values: string/int64/float64/bool.
type Args map[string]any

// FileEffect is owner-supplied metadata from an actual successful publication.
type FileEffect struct {
	Path, BeforeHash, AfterHash string
	Created                     bool
}

// Result is a tool outcome.
// WorkflowScope is an owning store/node identity, not copied graph state.
type WorkflowScope struct {
	Store string `json:"store"`
	Kind  string `json:"kind"`
	ID    string `json:"id"`
}

// CreatedNode is an owning creation fact, never selection or execution authority.
type CreatedNode struct{ Kind, ID, State string }

type Result struct {
	ReadyNode  *CreatedNode   `json:"-"` // actual Ready receipt, not creation or execution authority
	Created    *CreatedNode   `json:"-"`
	Focus      *WorkflowScope `json:"-"` // actual successful focus, committed after durable settlement
	Changed    bool           // actual file publication, including effects followed by errors
	FileEffect *FileEffect    `json:"-"` // never inferred from arguments or result prose
	// Workflow carries an owning-state snapshot, including refused attempts.
	// ExitCode is an actual settled Bash exit; nil means no complete exit receipt.
	ExitCode  *int
	Workflow  *outcome.Report
	Text      string
	Truncated bool
	NoMatches bool `json:",omitempty"` // successful zero returned matches for this query/page; not repository absence
	// Blocked marks a gate refusal (declined/blocked): the tool did not
	// run. Gates set it; the loop treats blocked calls as non-progress.
	Blocked bool
	// Failed marks the tool's own failure (e.g. bash non-zero exit): the
	// tool ran and answered, but the operation failed. Tools set it when
	// the failure rides in the Result instead of the error; the loop
	// treats failed calls as non-progress. Text still carries the output.
	Failed bool
}

// Definition describes and implements one tool.
type Definition struct {
	// Advertisement is a stable wire declaration; current Params still validate calls.
	Advertisement *Definition

	Opening       bool                                                  // harmless entry/document navigation; unsupported arguments may be disclosed and ignored
	NormalizeArgs func(context.Context, Args) (Args, error)             // owning path resolution before authorization; no lifecycle authority
	Prompts       *prompts.Catalog                                      // immutable owning request/run snapshot
	Bound         bool                                                  // machine-owned scoped expansion; never part of the model schema
	Bind          func(context.Context, Args) (Definition, Args, error) // scoped expansion before original validation/auth
	// WorkflowRefArg marks only workflow invocations: register the approach
	// attempt after validation/owner resolution, before auth. Schema-only requests are not invocations.
	WorkflowRefArg string
	// ResolveWorkflowRef returns empty for known invalid, nonempty for owned or uncertain refs.
	ResolveWorkflowRef func(context.Context, Args) (string, error)
	Name               string
	Label              string
	Desc               string // one line: what it is for
	Purpose            string // short paragraph: when and how to use it
	Params             []Param
	Example            Args // owner-authored example for conditional arguments; never defaults
	Execute            func(ctx context.Context, args Args) (Result, error)
}

// Registry holds tool definitions by name.
type Registry struct {
	Prompts *prompts.Catalog
	tools   map[string]Definition
	allowed map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry(catalogs ...*prompts.Catalog) *Registry {
	return &Registry{tools: map[string]Definition{}, Prompts: prompts.Select(catalogs...)}
}

// Register adds a tool. Duplicate names are a programmer error.
func (r *Registry) Register(d Definition) {
	if d.Prompts == nil {
		d.Prompts = r.Prompts
	}
	if _, dup := r.tools[d.Name]; dup {
		panic(fmt.Sprintf("tool: duplicate registration %q", d.Name))
	}
	r.tools[d.Name] = d
}

// Lookup returns the named tool or false.
func (r *Registry) Lookup(name string) (Definition, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if r.allowed != nil && !r.allowed[key] {
		return Definition{}, false
	}
	d, ok := r.tools[key]
	return d, ok
}

// Names lists registered tools, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		if r.allowed != nil && !r.allowed[n] {
			continue
		}
		if n == "find" {
			if _, available := r.Lookup("find_file"); available {
				continue // One advertised finder; ResolveName retains compatibility.
			}
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Schema renders purpose plus fillable slots as human text.
func (d Definition) Schema() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tool: %s — %s\n%s\n", d.Name, d.Desc, d.Purpose)
	if len(d.Params) == 0 {
		b.WriteString(d.Prompts.Text("tool.call.no_arguments") + "\n" + d.CallTemplate() + "\n")
		return b.String()
	}
	b.WriteString(d.CallTemplate() + "\n")
	b.WriteString("slots:\n")
	for _, p := range d.Params {
		req := "optional"
		if p.Required {
			req = "required"
		}
		fmt.Fprintf(&b, "  %s (%s, %s): %s", p.Name, p.Type, req, p.Desc)
		if p.Default != "" {
			fmt.Fprintf(&b, " [default: %s]", p.Default)
		}
		if len(p.Enum) > 0 {
			fmt.Fprintf(&b, " [one of: %s]", strings.Join(p.Enum, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// View shares registered definitions and constrains both schemas and lookup.
func (r *Registry) View(names []string) *Registry {
	if names == nil {
		return r
	}
	v := &Registry{tools: r.tools, allowed: map[string]bool{}, Prompts: r.Prompts}
	for _, n := range names {
		v.allowed[n] = true
	}
	return v
}
