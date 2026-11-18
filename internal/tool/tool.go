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

// MethodStep identifies one phase in a published reusable workflow.
type MethodStep struct {
	BlockID      string `json:"block_id"`
	BlockVersion int    `json:"block_version"`
	Title        string `json:"title"`
}

// MethodLens is protected session state for thinking with one reusable
// workflow inside ordinary or PAT work. It is a method snapshot and cursor,
// never work ownership, approval, settlement, or verification evidence.
type MethodLens struct {
	WorkflowID         string            `json:"workflow_id"`
	WorkflowVersion    int               `json:"workflow_version"`
	Title              string            `json:"title"`
	Purpose            string            `json:"purpose"`
	AppliesTo          []string          `json:"applies_to,omitempty"`
	Inputs             map[string]string `json:"inputs,omitempty"`
	Steps              []MethodStep      `json:"steps,omitempty"`
	StepIndex          int               `json:"step_index"` // -1 means no current phase
	BlockID            string            `json:"block_id,omitempty"`
	BlockVersion       int               `json:"block_version,omitempty"`
	PhaseTitle         string            `json:"phase_title,omitempty"`
	PhasePurpose       string            `json:"phase_purpose,omitempty"`
	PhaseInputs        []string          `json:"phase_inputs,omitempty"`
	PhaseOutputs       []string          `json:"phase_outputs,omitempty"`
	Instructions       string            `json:"instructions,omitempty"`
	CompletionEvidence string            `json:"completion_evidence,omitempty"`
	FailureHandling    string            `json:"failure_handling,omitempty"`
	Adaptation         string            `json:"adaptation,omitempty"` // task-local overlay; never edits the reusable workflow
	Observations       string            `json:"observations,omitempty"`
	Risks              string            `json:"risks,omitempty"`
	NextCheck          string            `json:"next_check,omitempty"`
	ScopeKind          string            `json:"scope_kind,omitempty"`
	ScopeID            string            `json:"scope_id,omitempty"`
	TaskID             string            `json:"task_id,omitempty"`
	Attempt            int               `json:"attempt,omitempty"`
	Revision           int64             `json:"revision,omitempty"`
}

func (l *MethodLens) Clone() *MethodLens {
	if l == nil {
		return nil
	}
	value := *l
	value.AppliesTo = append([]string(nil), l.AppliesTo...)
	value.Steps = append([]MethodStep(nil), l.Steps...)
	value.PhaseInputs = append([]string(nil), l.PhaseInputs...)
	value.PhaseOutputs = append([]string(nil), l.PhaseOutputs...)
	if l.Inputs != nil {
		value.Inputs = make(map[string]string, len(l.Inputs))
		for k, v := range l.Inputs {
			value.Inputs[k] = v
		}
	}
	return &value
}

// MethodLensUpdate is committed by the agent only after durable settlement.
type MethodLensUpdate struct {
	Mode string      `json:"mode"`
	Lens *MethodLens `json:"lens,omitempty"`
}

type Result struct {
	StateChanged bool              `json:",omitempty"` // Actual committed graph effects; never filesystem invalidation.
	ReadyNode    *CreatedNode      `json:"-"`          // actual Ready receipt, not creation or execution authority
	Created      *CreatedNode      `json:"-"`
	Focus        *WorkflowScope    `json:"-"` // actual successful focus, committed after durable settlement
	MethodLens   *MethodLensUpdate `json:"-"` // method cursor candidate, committed after durable settlement
	Changed      bool              // actual file publication, including effects followed by errors
	FileEffect   *FileEffect       `json:"-"` // never inferred from arguments or result prose
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
	// GraphQLFields is a host-owned help projection; nil means the normal model API.
	GraphQLFields []string

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

// Replace changes an existing host-owned definition without losing aliases,
// dynamic help closures or role restrictions. It cannot register a new tool.
func (r *Registry) Replace(d Definition) {
	if _, ok := r.tools[d.Name]; !ok {
		panic(fmt.Sprintf("tool: replacement %q is not registered", d.Name))
	}
	if d.Prompts == nil {
		d.Prompts = r.Prompts
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

// Restrict narrows this role's existing view in place so help closures see the
// same availability as dispatch. Unlike View it cannot widen a prior view.
func (r *Registry) Restrict(names []string) {
	allowed := map[string]bool{}
	for _, name := range names {
		if r.allowed == nil || r.allowed[name] {
			allowed[name] = true
		}
	}
	r.allowed = allowed
}
