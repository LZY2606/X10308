// Package store persists the token-flow workspace in a local SQLite database.
//
// Persistence principles enforced here:
//   - source bytes and derived interpretations are stored separately;
//   - everything is append-only and addressed by a stable identity;
//   - imports are transactional (a failed import rolls back completely);
//   - decisions use optimistic revision control and conflicts are retained;
//   - invalidation is dependency-scoped, never a global silent replacement.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed application database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path. Use ":memory:" for tests.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for package-internal helpers/tests.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// nextEventSeq returns the next global monotonic event number.
func (s *Store) nextEventSeqTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var maxSeq int64
	row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM event_log`)
	if err := row.Scan(&maxSeq); err != nil {
		return 0, err
	}
	return maxSeq, nil
}

// recordEvent appends one machine-readable judgement event.
// RecordEvent appends a ledger event inside a transaction.
func (s *Store) RecordEvent(ctx context.Context, tx *sql.Tx, e Event) (int64, error) {
	seq, err := s.nextEventSeqTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_log
(seq, at_event, event_type, actor, branch_id, ref_type, ref_id, payload_json, base_rev)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seq, e.When, e.Type, e.Actor, e.BranchID, e.RefType, e.RefID, e.Payload, e.BaseRev)
	if err != nil {
		return 0, fmt.Errorf("record event: %w", err)
	}
	return seq, nil
}
