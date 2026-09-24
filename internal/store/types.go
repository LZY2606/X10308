// Package store persists sources, versions, analyses, branches, events
// and decisions in SQLite. Sources (raw bytes) and interpretations
// (derived analyses) are stored separately; every import is a single
// transaction that either commits fully or rolls back.
package store

import "time"

// RulesetVersion identifies the derivation rules used for analyses.
// Enablement considers inputs, inhibitors and capacities together;
// canonicalization never merges markings with different token counts;
// budget exhaustion yields unknown with the frontier retained.
const RulesetVersion = 1

// Source is an immutable imported raw document.
type Source struct {
	ID          string    `json:"id"`
	Seq         int64     `json:"seq"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Raw         []byte    `json:"raw"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
}

// Diagnostic mirrors importx.Diagnostic at rest.
type Diagnostic struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Seq      int    `json:"seq"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Raw      string `json:"raw"`
	Message  string `json:"message"`
}

// ModelVersion is one versioned interpretation of a model document.
type ModelVersion struct {
	ID          string    `json:"id"`
	ModelID     string    `json:"model_id"`
	Version     int       `json:"version"`
	SourceID    string    `json:"source_id"`
	Payload     string    `json:"payload"`      // original document JSON (raw fields)
	NetJSON     string    `json:"net_json"`     // normalized net
	InitialJSON string    `json:"initial_json"` // normalized initial marking
	TargetJSON  string    `json:"target_json"`  // normalized target marking
	CreatedAt   time.Time `json:"created_at"`
}

// StrategyVersion is a versioned exploration configuration.
type StrategyVersion struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	SourceID  string    `json:"source_id"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// Analysis is a derived result pinned to model+strategy+ruleset versions.
type Analysis struct {
	ID                string    `json:"id"`
	ModelVersionID    string    `json:"model_version_id"`
	StrategyVersionID string    `json:"strategy_version_id"`
	RulesetVersion    int       `json:"ruleset_version"`
	Status            string    `json:"status"`
	ResultJSON        string    `json:"result_json"`
	CoverJSON         string    `json:"cover_json"`
	CreatedAt         time.Time `json:"created_at"`
}

// Candidate is an explicit multi-candidate record: a deadlock, a
// truncated frontier item or a witness path awaiting adjudication.
type Candidate struct {
	ID         string `json:"id"`
	AnalysisID string `json:"analysis_id"`
	Kind       string `json:"kind"` // deadlock | frontier | path
	Seq        int    `json:"seq"`
	Payload    string `json:"payload"`
}

// Branch is a line of interpretation. Re-interpretation of the same
// material creates a new branch; old branches stay readable.
type Branch struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	ParentBranchID string    `json:"parent_branch_id"`
	ForkEventID    string    `json:"fork_event_id"`
	HeadEventID    string    `json:"head_event_id"`
	HeadSeq        int64     `json:"head_seq"`
	CreatedAt      time.Time `json:"created_at"`
}

// Event is an immutable, machine-readable record. It pins the dependency
// versions visible at the time so revisiting it restores that view.
type Event struct {
	ID                string    `json:"id"`
	BranchID          string    `json:"branch_id"`
	Seq               int64     `json:"seq"`
	Type              string    `json:"type"`
	Actor             string    `json:"actor"`
	Payload           string    `json:"payload"`
	ModelVersionID    string    `json:"model_version_id"`
	StrategyVersionID string    `json:"strategy_version_id"`
	AnalysisID        string    `json:"analysis_id"`
	RulesetVersion    int       `json:"ruleset_version"`
	CreatedAt         time.Time `json:"created_at"`
}

// Event types.
const (
	EvImport            = "model_imported"
	EvStrategyRevision  = "strategy_revised"
	EvParamRevision     = "parameter_revised"
	EvBranchCreated     = "branch_created"
	EvDecision          = "decision_recorded"
	EvDecisionReverted  = "decision_reverted"
	EvInvalidation      = "invalidation_computed"
	EvArchiveImported   = "archive_imported"
	EvSeed              = "case_seeded"
)

// Decision kinds.
const (
	DecAccept      = "accept"
	DecReject      = "reject"
	DecParamAdjust = "param_adjust"
)

// Decision is a human ruling attached to a branch event. Conflicting
// rulings on the same target are both preserved.
type Decision struct {
	ID         string    `json:"id"`
	BranchID   string    `json:"branch_id"`
	EventID    string    `json:"event_id"`
	Kind       string    `json:"kind"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Rationale  string    `json:"rationale"`
	Reverted   bool      `json:"reverted"`
	CreatedAt  time.Time `json:"created_at"`
}

// Invalidation records the computed scope of a version change.
type Invalidation struct {
	ID             string    `json:"id"`
	TriggerEventID string    `json:"trigger_event_id"`
	AffectedType   string    `json:"affected_type"` // analysis | decision | event
	AffectedID     string    `json:"affected_id"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
}

// ErrConflict is returned when an optimistic-concurrency check fails.
type ErrConflict struct {
	ExpectedHeadSeq int64   `json:"expected_head_seq"`
	ActualHeadSeq   int64   `json:"actual_head_seq"`
	ActualHeadEvent string  `json:"actual_head_event"`
	MissedEvents    []Event `json:"missed_events"`
}

func (e *ErrConflict) Error() string {
	return "branch head conflict: expected seq differs from actual head"
}
