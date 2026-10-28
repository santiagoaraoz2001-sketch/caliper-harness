// Typed values for the schema-driven GraphQL API. Keep JSON names aligned with SDL.

package model

type Approach struct {
	// Read-only readable rendering; title retains the exact authored contract.
	Display    string   `json:"display"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	State      string   `json:"state"`
	NeedsReady bool     `json:"needsReady"`
	CreatedAt  string   `json:"createdAt"`
	Tasks      []*Task  `json:"tasks"`
	Qualified  bool     `json:"qualified"`
	Next       []string `json:"next"`
}

type EvidenceCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type EvidenceGroup struct {
	Reference string            `json:"reference"`
	Offset    int               `json:"offset"`
	CallIds   []string          `json:"callIds"`
	Records   []*EvidenceRecord `json:"records"`
}

// Exact decoded native message fields. Call arguments/results are independently selectable from reasoning and raw event text.
type EvidenceMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	Thinking         string          `json:"thinking"`
	Reasoning        string          `json:"reasoning"`
	ReasoningContent string          `json:"reasoningContent"`
	ToolCallID       string          `json:"toolCallId"`
	Name             string          `json:"name"`
	ToolCalls        []*EvidenceCall `json:"toolCalls"`
}

type EvidencePage struct {
	ProjectRoot string `json:"projectRoot"`
	SessionID   string `json:"sessionId"`
	TaskID      string `json:"taskId"`
	Historical  bool   `json:"historical"`
	NextOffset  *int   `json:"nextOffset,omitempty"`
	// An unfinished recorded call group, with no invented results. Complete earlier groups remain readable; reread this offset after settlement.
	UnsettledOffset *int             `json:"unsettledOffset,omitempty"`
	Groups          []*EvidenceGroup `json:"groups"`
}

type EvidenceRecord struct {
	Offset int    `json:"offset"`
	Time   string `json:"time"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Recorded host workspace provenance; a historical worker path grants no filesystem or execution authority.
	Workspace string `json:"workspace"`
	// Exact archived event text or task record JSON. Select message instead to avoid importing native reasoning.
	Text    string           `json:"text"`
	Message *EvidenceMessage `json:"message,omitempty"`
}

type FreshTaskInput struct {
	PlanTitle         string `json:"planTitle"`
	PlanGoal          string `json:"planGoal"`
	ApproachTitle     string `json:"approachTitle"`
	TaskTitle         string `json:"taskTitle"`
	TaskPrompt        string `json:"taskPrompt"`
	Current           string `json:"current"`
	Target            string `json:"target"`
	Sketch            string `json:"sketch"`
	Acceptance        string `json:"acceptance"`
	Tests             string `json:"tests"`
	Verify            string `json:"verify"`
	CheckCommand      string `json:"checkCommand"`
	AcceptanceCommand string `json:"acceptanceCommand"`
}

// An immutable authored note. Host scope/session provenance cannot be supplied by the model; freshness is unknown.
type Knowledge struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
	SessionRef string `json:"sessionRef"`
	PreviousID string `json:"previousId"`
	TaskID     string `json:"taskId"`
	FilePath   string `json:"filePath"`
	// caliper-default or project-authored; neither is verification evidence.
	Source string `json:"source"`
	// Recorded source fingerprint; empty means unknown.
	FileSha256 string `json:"fileSHA256"`
	// unchanged, changed, missing or unknown; source freshness does not prove correctness.
	Freshness string `json:"freshness"`
	// Selected heading sections. text matches keywords case-insensitively; section selects an exact anchor.
	Sections   []*KnowledgeSection   `json:"sections"`
	References []*KnowledgeReference `json:"references"`
}

type KnowledgeReference struct {
	Key           string `json:"key"`
	Relation      string `json:"relation"`
	Section       string `json:"section"`
	SourceSection string `json:"sourceSection"`
	Label         string `json:"label"`
	Status        string `json:"status"`
}

type KnowledgeReferenceInput struct {
	Key           string  `json:"key"`
	Relation      string  `json:"relation"`
	Section       *string `json:"section,omitempty"`
	SourceSection *string `json:"sourceSection,omitempty"`
	Label         *string `json:"label,omitempty"`
}

type KnowledgeSection struct {
	ID         string                `json:"id"`
	Title      string                `json:"title"`
	Body       string                `json:"body"`
	References []*KnowledgeReference `json:"references"`
}

type MemoryEntry struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
	SessionRef string `json:"sessionRef"`
}

type Mutation struct {
}

type Plan struct {
	ProjectRoot *string     `json:"projectRoot,omitempty"`
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Goal        string      `json:"goal"`
	State       string      `json:"state"`
	CreatedAt   string      `json:"createdAt"`
	Approaches  []*Approach `json:"approaches"`
	Next        []string    `json:"next"`
}

type PreparedTask struct {
	Plan     *Plan     `json:"plan"`
	Approach *Approach `json:"approach"`
	Task     *Task     `json:"task"`
}

type Query struct {
}

type Revision struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	TargetKind string   `json:"targetKind"`
	TargetID   string   `json:"targetId"`
	Text       string   `json:"text"`
	Source     string   `json:"source"`
	SessionRef string   `json:"sessionRef"`
	CreatedAt  string   `json:"createdAt"`
	Resolves   string   `json:"resolves"`
	TaskIds    []string `json:"taskIds"`
}

type RevisionContract struct {
	Snapshot string      `json:"snapshot"`
	Brief    string      `json:"brief"`
	Pending  []*Revision `json:"pending"`
}

type Session struct {
	ID     string          `json:"id"`
	Events []*SessionEvent `json:"events"`
}

type SessionEvent struct {
	Time string `json:"time"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type Task struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Prompt             string   `json:"prompt"`
	State              string   `json:"state"`
	CreatedAt          string   `json:"createdAt"`
	Current            string   `json:"current"`
	Target             string   `json:"target"`
	Sketch             string   `json:"sketch"`
	Acceptance         string   `json:"acceptance"`
	Tests              string   `json:"tests"`
	Verify             string   `json:"verify"`
	CheckCommand       string   `json:"checkCommand"`
	AcceptanceCommand  string   `json:"acceptanceCommand"`
	Result             string   `json:"result"`
	Attempts           int      `json:"attempts"`
	Execution          string   `json:"execution"`
	ExecutionReason    string   `json:"executionReason"`
	Verification       string   `json:"verification"`
	VerificationReason string   `json:"verificationReason"`
	Review             string   `json:"review"`
	AttemptLog         string   `json:"attemptLog"`
	Qualified          bool     `json:"qualified"`
	Next               []string `json:"next"`
}

type TaskComment struct {
	ID         string `json:"id"`
	TaskID     string `json:"taskId"`
	Attempt    int    `json:"attempt"`
	Source     string `json:"source"`
	SessionRef string `json:"sessionRef"`
	Text       string `json:"text"`
	CreatedAt  string `json:"createdAt"`
}
