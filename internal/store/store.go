// Package store persists the versioned workspace in SQLite: models and
// versions, strategies, exploration runs, an append-only event log, review
// branches with optimistic-concurrency conflict detection, rulings with
// undo, the counterexample archive, and full export/import with stable
// identities.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies pending schema migrations in order.
func (s *Store) Migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var have int
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&have)
	for i := have; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`,
			i+1, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion reports the latest applied migration version.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v, err
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// WithTx runs fn in a single transaction: an import failure rolls back only
// the current change and never leaves half a record behind.
func (s *Store) WithTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---- events ----

type Event struct {
	Seq      int64           `json:"seq"`
	ID       string          `json:"id"`
	BranchID string          `json:"branch_id,omitempty"`
	Kind     string          `json:"kind"`
	Payload  json.RawMessage `json:"payload"`
	Created  string          `json:"created_at"`
}

// AppendEvent appends a machine-readable event to the log. Inside a tx it
// returns the assigned sequence number.
func AppendEvent(tx *sql.Tx, branchID, kind string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	id := fmt.Sprintf("evt_%s_%s", kind, randSuffix())
	var seq int64
	err = tx.QueryRow(
		`INSERT INTO events(id,branch_id,kind,payload,created_at) VALUES(?,?,?,?,?) RETURNING seq`,
		id, nullStr(branchID), kind, string(raw), now()).Scan(&seq)
	if err != nil {
		return 0, err
	}
	if branchID != "" {
		if _, err := tx.Exec(`UPDATE branches SET head_seq=? WHERE id=?`, seq, branchID); err != nil {
			return 0, err
		}
	}
	return seq, nil
}

func (s *Store) EventsUpTo(seq int64) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq,id,COALESCE(branch_id,''),kind,payload,created_at
		FROM events WHERE seq<=? ORDER BY seq`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var p string
		if err := rows.Scan(&e.Seq, &e.ID, &e.BranchID, &e.Kind, &p, &e.Created); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) HeadSeq() (int64, error) {
	var v int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&v)
	return v, err
}

// ---- models & versions ----

type ModelVersion struct {
	ID         string `json:"id"`
	ModelID    string `json:"model_id"`
	VersionNo  int    `json:"version_no"`
	ParentID   string `json:"parent_id,omitempty"`
	ChangeKind string `json:"change_kind"` // baseline|parameter_revision|source_amendment
	Payload    string `json:"payload"`
	Created    string `json:"created_at"`
	Stale      bool   `json:"stale"`
}

func (s *Store) DB() *sql.DB { return s.db }

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

var errSentinel = errors.New("store")
