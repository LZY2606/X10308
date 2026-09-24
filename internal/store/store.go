// Package store is the local SQLite persistence layer. It is append-only for
// sources and events: re-interpretations create new branches and never mutate
// the original bytes or old conclusions.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrations is a state migration sequence. Applied versions are recorded in
// schema_migrations; each migration is tested by the state migration test.
var migrations = []struct {
	version int
	stmts   string
}{
	{1, `
CREATE TABLE sources(
  source_id   TEXT PRIMARY KEY,
  doc_id      TEXT NOT NULL,
  revision    TEXT NOT NULL,
  kind        TEXT NOT NULL,
  filename    TEXT NOT NULL,
  raw         BLOB NOT NULL,
  sha256      TEXT NOT NULL,
  created_seq INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE params_versions(
  params_version TEXT PRIMARY KEY,
  params_json    TEXT NOT NULL,
  created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE branches(
  branch_id       TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  source_id       TEXT NOT NULL REFERENCES sources(source_id),
  params_version  TEXT NOT NULL REFERENCES params_versions(params_version),
  parent_branch   TEXT,
  created_from_event INTEGER,
  note            TEXT NOT NULL DEFAULT '',
  version_counter INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE events(
  seq         INTEGER PRIMARY KEY,
  event_id    TEXT NOT NULL UNIQUE,
  branch_id   TEXT NOT NULL,
  type        TEXT NOT NULL,
  payload     TEXT NOT NULL,
  source_id   TEXT,
  params_version TEXT,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE runs(
  run_id        TEXT PRIMARY KEY,
  branch_id     TEXT NOT NULL,
  scenario_id   TEXT NOT NULL,
  kind          TEXT NOT NULL,
  net_hash      TEXT NOT NULL,
  params_version TEXT NOT NULL,
  source_id     TEXT NOT NULL,
  result_json   TEXT NOT NULL,
  seq           INTEGER NOT NULL
);
CREATE TABLE candidates(
  candidate_id TEXT PRIMARY KEY,
  branch_id    TEXT NOT NULL,
  scenario_id  TEXT NOT NULL,
  run_id       TEXT NOT NULL,
  kind         TEXT NOT NULL,
  label        TEXT NOT NULL,
  outcome      TEXT NOT NULL,
  decision     TEXT NOT NULL DEFAULT 'open',
  decided_event_seq INTEGER,
  stale        INTEGER NOT NULL DEFAULT 0,
  stale_reason TEXT NOT NULL DEFAULT '',
  created_seq  INTEGER NOT NULL
);
CREATE TABLE deps(
  candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id),
  dep_type     TEXT NOT NULL,
  dep_ref      TEXT NOT NULL,
  PRIMARY KEY(candidate_id, dep_type, dep_ref)
);
CREATE TABLE invalidations(
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  branch_id     TEXT NOT NULL,
  changed_type  TEXT NOT NULL,
  changed_ref   TEXT NOT NULL,
  candidate_id  TEXT NOT NULL,
  reason        TEXT NOT NULL,
  seq           INTEGER NOT NULL
);
`},
	{2, `
ALTER TABLE branches ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_events_branch ON events(branch_id, seq);
CREATE INDEX idx_runs_branch ON runs(branch_id);
CREATE INDEX idx_candidates_branch ON candidates(branch_id);
`},
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		return err
	}
	var v int
	row := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`)
	if err := row.Scan(&v); err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= v {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.stmts); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, m.version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
