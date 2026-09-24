package store

import "encoding/json"

// Event is one row of the global append-only ledger.
type Event struct {
	When     string `json:"at"`
	Type     string `json:"type"`
	Actor    string `json:"actor"`
	BranchID string `json:"branch_id,omitempty"`
	RefType  string `json:"ref_type,omitempty"`
	RefID    string `json:"ref_id,omitempty"`
	Payload  string `json:"payload,omitempty"`
	BaseRev  *int   `json:"base_rev,omitempty"`
}

// Event row view with sequence.
type EventRow struct {
	Seq       int64           `json:"seq"`
	Event
	PayloadV  json.RawMessage `json:"payload"`
}

// Event types. They are machine-readable judgements: replay code branches on
// these codes instead of display strings.
const (
	EvImportCommitted   = "import.committed"
	EvMaterialAdded     = "material.added"
	EvDiagnosticKept    = "material.diagnostic"
	EvBranchCreated     = "branch.created"
	EvModelInterpreted  = "model.interpreted"
	EvRunExplored       = "run.explored"
	EvEnablementChecked = "enablement.checked" // input+inhibitor+capacity
	EvFrontierTruncated = "run.frontier_truncated"
	EvCounterexample    = "counterexample.recorded"
	EvDecisionApplied   = "decision.applied"
	EvDecisionUndone    = "decision.undone"
	EvDecisionConflict  = "decision.conflict"
	EvInvalidation      = "invalidation.scoped"
)

// Material kinds.
const (
	MatNet      = "net"
	MatMarking  = "marking"
	MatRules    = "rules"
	MatStrategy = "strategy"
	MatDecision = "human_decision"
)

// MaterialRow is a versioned source material.
type MaterialRow struct {
	ID          string          `json:"id"`
	Version     int             `json:"version"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Raw         json.RawMessage `json:"raw"`
	SHA256      string          `json:"sha256"`
	ImportBatch string          `json:"import_batch"`
	Supersedes  string          `json:"supersedes,omitempty"`
}

// BranchRow is one interpretation/adjudication branch.
type BranchRow struct {
	ID           string         `json:"id"`
	Seq          int64          `json:"seq"`
	Title        string         `json:"title"`
	ParentID     string         `json:"parent_id,omitempty"`
	CreatedEvent int64          `json:"created_event"`
	Open         bool           `json:"open"`
	Materials    []MaterialSlot `json:"materials"`
}

// MaterialSlot is the ordered pin of a material version into a branch.
type MaterialSlot struct {
	MaterialID string `json:"material_id"`
	Version    int    `json:"version"`
	Slot       int    `json:"slot"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
}

// ModelRow is a derived interpretation.
type ModelRow struct {
	ID              string          `json:"id"`
	Seq             int64           `json:"seq"`
	BranchID        string          `json:"branch_id"`
	MaterialID      string          `json:"material_id"`
	MaterialVersion int             `json:"material_version"`
	RulesVersion    string          `json:"rules_version"`
	Derived         json.RawMessage `json:"derived"`
	CreatedEvent    int64           `json:"created_event"`
}

// RunRow is one exploration/coverability run with its parameter versions.
type RunRow struct {
	ID           string          `json:"id"`
	Seq          int64           `json:"seq"`
	BranchID     string          `json:"branch_id"`
	ModelID      string          `json:"model_id"`
	RulesVersion string          `json:"rules_version"`
	Strategy     string          `json:"strategy"`
	FireBudget   int64           `json:"fire_budget"`
	StateBudget  int64           `json:"state_budget"`
	Target       json.RawMessage `json:"target,omitempty"`
	Result       json.RawMessage `json:"result"`
	Cover        json.RawMessage `json:"cover,omitempty"`
	CreatedEvent int64           `json:"created_event"`
}

// CounterexampleRow is one archived finding (adopted or not).
type CounterexampleRow struct {
	ID          string          `json:"id"`
	Seq         int64           `json:"seq"`
	BranchID    string          `json:"branch_id"`
	RunID       string          `json:"run_id"`
	Scenario    string          `json:"scenario"`
	Title       string          `json:"title"`
	Detail      json.RawMessage `json:"detail"`
	Verdict     string          `json:"verdict"`
	Rev         int             `json:"rev"`
	DecidedEvent *int64         `json:"decided_event,omitempty"`
	DecidedBy   string          `json:"decided_by,omitempty"`
}

// InvalidationRow records the scoped blast radius of a version change.
type InvalidationRow struct {
	Seq          int64    `json:"seq"`
	At           string   `json:"at"`
	Scope        []string `json:"scope"`
	ReasonCode   string   `json:"reason_code"`
	CauseType    string   `json:"cause_type"`
	CauseID      string   `json:"cause_id"`
	CauseVersion int      `json:"cause_version"`
}
