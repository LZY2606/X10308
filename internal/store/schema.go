package store

import (
	"database/sql"
	"fmt"
	"time"
)

// migrations is the ordered schema history. The migration test applies
// them stepwise to prove old data survives every upgrade.
var migrations = []string{
	// v1: core versioned model + event log.
	`CREATE TABLE sources (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		raw BLOB NOT NULL,
		hash TEXT NOT NULL,
		unit TEXT NOT NULL DEFAULT '',
		basis TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);
	CREATE TABLE model_versions (
		id TEXT PRIMARY KEY,
		seq INTEGER NOT NULL,
		parent_id TEXT NOT NULL DEFAULT '',
		label TEXT NOT NULL,
		model_json TEXT NOT NULL,
		source_ids TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL
	);
	CREATE TABLE branches (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		parent_id TEXT NOT NULL DEFAULT '',
		model_version_id TEXT NOT NULL,
		rule_version INTEGER NOT NULL,
		head_seq INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);
	CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL,
		model_version_id TEXT NOT NULL,
		rule_version INTEGER NOT NULL,
		strategy TEXT NOT NULL,
		budget INTEGER NOT NULL,
		status TEXT NOT NULL,
		result_json TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL UNIQUE,
		branch_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		payload_json TEXT NOT NULL DEFAULT '{}',
		model_version_id TEXT NOT NULL DEFAULT '',
		rule_version INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);`,
	// v2: candidates (multi-candidate rulings), dependency tracking,
	// import diagnostics.
	`CREATE TABLE candidates (
		id TEXT PRIMARY KEY,
		branch_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		payload_json TEXT NOT NULL DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'pending',
		created_at TEXT NOT NULL
	);
	CREATE TABLE dependencies (
		derived_kind TEXT NOT NULL,
		derived_id TEXT NOT NULL,
		source_id TEXT NOT NULL,
		version_seq INTEGER NOT NULL,
		PRIMARY KEY (derived_kind, derived_id, source_id)
	);
	CREATE TABLE diagnostics (
		id TEXT PRIMARY KEY,
		source_id TEXT NOT NULL,
		message TEXT NOT NULL,
		raw_value TEXT NOT NULL,
		created_at TEXT NOT NULL
	);`,
	// v3: counterexample archive + ruling notes.
	`CREATE TABLE counterexamples (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		scenario TEXT NOT NULL,
		description TEXT NOT NULL,
		branch_id TEXT NOT NULL,
		baseline_run_id TEXT NOT NULL,
		variant_run_id TEXT NOT NULL,
		changed_source_id TEXT NOT NULL,
		judgment TEXT NOT NULL DEFAULT 'pending',
		created_at TEXT NOT NULL
	);
	ALTER TABLE candidates ADD COLUMN note TEXT NOT NULL DEFAULT '';`,
}

var schemaVersion = len(migrations)

func migrate(db *sql.DB, target int) error {
	var cur int
	row := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`)
	if err := row.Scan(&cur); err != nil {
		if _, err2 := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err2 != nil {
			return err2
		}
		cur = 0
	}
	if target <= 0 || target > len(migrations) {
		target = len(migrations)
	}
	for v := cur + 1; v <= target; v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v-1]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`,
			v, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
