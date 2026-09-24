package store

import "encoding/json"

// Store is the versioned, append-only local database.
type Store struct {
	db DB
}

// Open opens (creating/migrating) the local SQLite database.
func Open(path string) (*Store, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Branch is a review branch with its own linear event chain.
type Branch struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ParentBranch  string `json:"parent_branch,omitempty"`
	ForkedFromEvent string `json:"forked_from_event,omitempty"`
	BaseEvent     string `json:"base_event,omitempty"`
	CreatedAt     string `json:"created_at"`
	Kind          string `json:"kind"`
	Note          string `json:"note,omitempty"`
	Seq           int    `json:"seq"`
}

// Event is one append-only entry on a branch.
type Event struct {
	ID          string          `json:"id"`
	BranchID    string          `json:"branch_id"`
	Seq         int             `json:"seq"`
	Kind        string          `json:"kind"`
	CreatedAt   string          `json:"created_at"`
	Actor       string          `json:"actor"`
	NetID       string          `json:"net_id,omitempty"`
	RunID       string          `json:"run_id,omitempty"`
	CandidateID string          `json:"candidate_id,omitempty"`
	SupersededBy string         `json:"superseded_by,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// SectionRef identifies one versioned section and its preserved raw blob.
type SectionRef struct {
	Section     string          `json:"section"`
	Version     string          `json:"version"`
	BlobID      string          `json:"blob_id"`
	SHA256      string          `json:"sha256"`
	Content     json.RawMessage `json:"content"`
}

// SourceDoc is one import (all seven sections versioned together).
type SourceDoc struct {
	ID           string       `json:"id"`
	BranchID     string       `json:"branch_id"`
	BundleID     string       `json:"bundle_id"`
	ModelVersion string       `json:"model_version"`
	Title        string       `json:"title"`
	CreatedAt    string       `json:"created_at"`
	Sections     []SectionRef `json:"sections"`
}

// InvalidatedEvent is one affected derived result after a source/parameter change.
type InvalidatedEvent struct {
	ID             string `json:"id"`
	EventID        string `json:"event_id"`
	ChangedSection string `json:"changed_section"`
	ChangedSource  string `json:"changed_source"`
	AffectedEvent  string `json:"affected_event"`
	AffectedKind   string `json:"affected_kind"`
	Reason         string `json:"reason"`
}

// ConflictError carries both sides of a concurrent-save collision.
type ConflictError struct {
	BaseHead   string   `json:"base_head"`
	ActualHead string   `json:"actual_head"`
	Diff       []string `json:"diff"`
}

func (e *ConflictError) Error() string { return "version conflict: branch moved since edit" }
