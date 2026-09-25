// Package journal defines the machine-readable event vocabulary used for
// provenance, adjudication and time-travel review.
package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Event types. These strings are part of the archive contract.
const (
	EvWorkspaceCreated      = "workspace.created"
	EvSourceImported        = "source.imported"
	EvImportRejected        = "source.import_rejected"
	EvInterpretationBuilt   = "interpretation.built"
	EvCandidateProduced     = "candidate.produced"
	EvRunCompleted          = "run.completed"
	EvRunTruncated          = "run.truncated"
	EvDeadlockFound         = "deadlock.found"
	EvCoverRelation         = "cover.relation"
	EvDecisionAccepted      = "decision.accepted"
	EvDecisionRejected      = "decision.rejected"
	EvDecisionRevoked       = "decision.revoked"
	EvParamRevision         = "param.revised"
	EvDependencyInvalidated = "dependency.invalidated"
	EvBranchCreated         = "branch.created"
	EvCaseLoaded            = "case.loaded"
	EvExported              = "archive.exported"
	EvImportedBundle        = "archive.bundle_imported"
	EvConflictDetected      = "conflict.detected"
)

// Event is an append-only, hash-chained journal record.
type Event struct {
	Seq       int64                  `json:"seq"`
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	BranchID  string                 `json:"branch_id"`
	Actor     string                 `json:"actor"`
	At        time.Time              `json:"at"`
	ParentHash string                `json:"parent_hash"`
	Hash      string                 `json:"hash"`
	Payload   map[string]interface{} `json:"payload"`
	Rules     []string               `json:"rules,omitempty"`
	BaseEvID  string                 `json:"base_event_id,omitempty"` // optimistic-concurrency base
}

// HashOf computes the content hash (excludes the stored Hash field).
func HashOf(e Event) string {
	cp := e
	cp.Hash = ""
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}
