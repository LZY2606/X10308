// Package store is the fully local SQLite persistence layer.
//
// Design points required by the workflow:
//
//   - source bytes and their interpretation are stored separately
//     (sources / source_versions vs interpretations);
//   - every aspect (model, node identity, arc weights, initial tokens,
//     capacities, exploration strategy, truncation budget) is versioned;
//   - the event log is append-only: recomputation after a parameter/reference
//     change appends invalidation/derived events instead of overwriting old
//     conclusions, and historical views replay up to an old event;
//   - branches keep alternative interpretations, human decisions and the raw
//     bytes they reference readable forever;
//   - each import runs in one transaction: any failure rolls the whole change
//     back, so no half record persists;
//   - optimistic version numbers turn two-window saves into a 409 conflict
//     with both sides' differences.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct{ db *sql.DB }

// ErrConflict is returned when an optimistic-concurrency check fails.
var ErrConflict = errors.New("version conflict")

// ErrNotFound is returned for missing rows.
var ErrNotFound = errors.New("not found")

// Open opens (creating if needed) the local database and runs migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
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

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for tests/backup tooling.
func (s *Store) DB() *sql.DB { return s.db }

const migrations = `
CREATE TABLE IF NOT EXISTS schema_meta (
  version INTEGER NOT NULL,
  applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Case/package root (the counterexample archive dossier).
CREATE TABLE IF NOT EXISTS cases (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  rule_version TEXT NOT NULL,
  rev INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Branch of interpretation / adjudication. Revisions fork rather than replace.
CREATE TABLE IF NOT EXISTS branches (
  id TEXT PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES cases(id),
  parent_branch_id TEXT REFERENCES branches(id),
  name TEXT NOT NULL,
  kind TEXT NOT NULL,                      -- baseline | revision | local_dep | decision
  note TEXT NOT NULL DEFAULT '',
  created_event_seq INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- A versioned source aspect: identity of the material and its immutable bytes.
CREATE TABLE IF NOT EXISTS sources (
  id TEXT PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES cases(id),
  aspect TEXT NOT NULL,                   -- model|identity|weights|initial_tokens|capacity|strategy|budget
  label TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS source_versions (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES sources(id),
  version INTEGER NOT NULL,
  raw_bytes BLOB NOT NULL,
  sha256 TEXT NOT NULL,
  media_type TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(source_id, version)
);

-- Which source version a branch is pinned to for each aspect.
CREATE TABLE IF NOT EXISTS branch_aspects (
  branch_id TEXT NOT NULL REFERENCES branches(id),
  aspect TEXT NOT NULL,
  source_id TEXT NOT NULL REFERENCES sources(id),
  version INTEGER NOT NULL,
  PRIMARY KEY (branch_id, aspect)
);

-- Append-only event log: the machine-readable judgment basis.
CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id TEXT NOT NULL UNIQUE,
  case_id TEXT NOT NULL REFERENCES cases(id),
  branch_id TEXT NOT NULL REFERENCES branches(id),
  type TEXT NOT NULL,
  payload TEXT NOT NULL,
  depends_on TEXT NOT NULL DEFAULT '[]',  -- JSON [aspect@version, rule@version, event#seq]
  rev INTEGER NOT NULL,
  undone INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Interpreted/cached derived results, keyed by their inputs.
CREATE TABLE IF NOT EXISTS interpretations (
  id TEXT PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES cases(id),
  branch_id TEXT NOT NULL REFERENCES branches(id),
  kind TEXT NOT NULL,                      -- net | marking | strategy | budget
  input_ref TEXT NOT NULL,                -- source@version
  sha256 TEXT NOT NULL,
  payload TEXT NOT NULL,
  diagnostics TEXT NOT NULL DEFAULT '[]',
  valid INTEGER NOT NULL DEFAULT 1,
  invalidated_by_event_seq INTEGER,
  created_event_seq INTEGER NOT NULL,
  UNIQUE(branch_id, kind, input_ref)
);

-- Exploration runs and their full machine-readable result.
CREATE TABLE IF NOT EXISTS explorations (
  id TEXT PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES cases(id),
  branch_id TEXT NOT NULL REFERENCES branches(id),
  question TEXT NOT NULL,                 -- reachability | enumerate
  goal TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL,
  deps TEXT NOT NULL,                     -- JSON dependency keys
  valid INTEGER NOT NULL DEFAULT 1,
  invalidated_by_event_seq INTEGER,
  created_event_seq INTEGER NOT NULL
);

-- Candidate paths produced by a run (multi-candidate is mandatory).
CREATE TABLE IF NOT EXISTS candidates (
  id TEXT PRIMARY KEY,
  exploration_id TEXT NOT NULL REFERENCES explorations(id),
  ord INTEGER NOT NULL,
  label TEXT NOT NULL,
  payload TEXT NOT NULL,
  UNIQUE(exploration_id, ord)
);

-- Human adjudications on candidates / runs; reversible, never deleting sources.
CREATE TABLE IF NOT EXISTS adjudications (
  id TEXT PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES cases(id),
  branch_id TEXT NOT NULL REFERENCES branches(id),
  candidate_id TEXT REFERENCES candidates(id),
  decision TEXT NOT NULL,                 -- accepted | rejected
  rationale TEXT NOT NULL DEFAULT '',
  base_rev INTEGER NOT NULL,
  undone INTEGER NOT NULL DEFAULT 0,
  undo_event_seq INTEGER,
  created_event_seq INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_events_branch ON events(branch_id, seq);
CREATE INDEX IF NOT EXISTS idx_sv_source ON source_versions(source_id, version);
`

const migration2 = `
-- v2: record conflict status for audit; existing databases get the column.
ALTER TABLE adjudications ADD COLUMN conflict_status TEXT NOT NULL DEFAULT '';
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, migrations); err != nil {
		return err
	}
	var v int
	row := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_meta`)
	if err := row.Scan(&v); err != nil {
		return err
	}
	if v < 2 {
		if _, err := s.db.ExecContext(ctx, migration2); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_meta(version) SELECT 2 WHERE COALESCE((SELECT MAX(version) FROM schema_meta),0) < 2`)
	return err
}

// JSON helpers ---------------------------------------------------------------

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
