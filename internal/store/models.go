package store

import (
	"encoding/json"
	"time"
)

// Aspect names, kept in one stable order everywhere (UI, export, seeding).
var Aspects = []string{
	"model", "identity", "weights", "initial_tokens",
	"capacity", "strategy", "budget",
}

// Case is the archive dossier root.
type Case struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	RuleVersion string `json:"rule_version"`
	Rev         int64  `json:"rev"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Branch is one interpretation / decision line.
type Branch struct {
	ID              string `json:"id"`
	CaseID          string `json:"case_id"`
	ParentBranchID  string `json:"parent_branch_id,omitempty"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Note            string `json:"note"`
	CreatedEventSeq int64  `json:"created_event_seq"`
	CreatedAt       string `json:"created_at"`
}

// Source is the identity of one versioned material aspect.
type Source struct {
	ID     string `json:"id"`
	CaseID string `json:"case_id"`
	Aspect string `json:"aspect"`
	Label  string `json:"label"`
}

// SourceVersion stores immutable raw bytes for one version.
type SourceVersion struct {
	ID        string    `json:"id"`
	SourceID  string    `json:"source_id"`
	Version   int       `json:"version"`
	RawBytes  []byte    `json:"raw_bytes"`
	SHA256    string    `json:"sha256"`
	MediaType string    `json:"media_type"`
	CreatedAt time.Time `json:"created_at"`
}

// BranchAspect pins a branch to a specific version of a source aspect.
type BranchAspect struct {
	BranchID string `json:"branch_id"`
	Aspect   string `json:"aspect"`
	SourceID string `json:"source_id"`
	Version  int    `json:"version"`
}

// Event is one append-only machine-readable record.
type Event struct {
	Seq       int64           `json:"seq"`
	EventID   string          `json:"event_id"`
	CaseID    string          `json:"case_id"`
	BranchID  string          `json:"branch_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	DependsOn []string        `json:"depends_on"`
	Rev       int64           `json:"rev"`
	Undone    bool            `json:"undone"`
	CreatedAt string          `json:"created_at"`
}

// Interpretation is a cached derived artifact.
type Interpretation struct {
	ID                    string          `json:"id"`
	CaseID                string          `json:"case_id"`
	BranchID              string          `json:"branch_id"`
	Kind                  string          `json:"kind"`
	InputRef              string          `json:"input_ref"`
	SHA256                string          `json:"sha256"`
	Payload               json.RawMessage `json:"payload"`
	Diagnostics           json.RawMessage `json:"diagnostics"`
	Valid                 bool            `json:"valid"`
	InvalidatedByEventSeq *int64          `json:"invalidated_by_event_seq,omitempty"`
	CreatedEventSeq       int64           `json:"created_event_seq"`
}

// Exploration is a stored run.
type Exploration struct {
	ID                    string          `json:"id"`
	CaseID                string          `json:"case_id"`
	BranchID              string          `json:"branch_id"`
	Question              string          `json:"question"`
	Goal                  string          `json:"goal"`
	Result                json.RawMessage `json:"result"`
	Deps                  []string        `json:"deps"`
	Valid                 bool            `json:"valid"`
	InvalidatedByEventSeq *int64          `json:"invalidated_by_event_seq,omitempty"`
	CreatedEventSeq       int64           `json:"created_event_seq"`
}

// Candidate is one legal path candidate within a run.
type Candidate struct {
	ID            string          `json:"id"`
	ExplorationID string          `json:"exploration_id"`
	Ord           int             `json:"ord"`
	Label         string          `json:"label"`
	Payload       json.RawMessage `json:"payload"`
}

// Adjudication is a reversible human decision.
type Adjudication struct {
	ID              string `json:"id"`
	CaseID          string `json:"case_id"`
	BranchID        string `json:"branch_id"`
	CandidateID     string `json:"candidate_id,omitempty"`
	Decision        string `json:"decision"`
	Rationale       string `json:"rationale"`
	BaseRev         int64  `json:"base_rev"`
	Undone          bool   `json:"undone"`
	UndoEventSeq    *int64 `json:"undo_event_seq,omitempty"`
	ConflictStatus  string `json:"conflict_status,omitempty"`
	CreatedEventSeq int64  `json:"created_event_seq"`
	CreatedAt       string `json:"created_at"`
}
