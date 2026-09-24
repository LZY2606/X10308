package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

const schemaVersion = 3

// Open opens (and migrates) the database at path. Use ":memory:" for tests.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for tests.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var v int
	row := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`)
	if err := row.Scan(&v); err != nil {
		v = 0
	}
	if v >= schemaVersion {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE IF NOT EXISTS sources(
			id TEXT PRIMARY KEY, seq INTEGER UNIQUE, name TEXT,
			content_type TEXT, raw BLOB, sha256 TEXT, created_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS diagnostics(
			id TEXT PRIMARY KEY, source_id TEXT, seq INTEGER,
			severity TEXT, path TEXT, raw TEXT, message TEXT)`,
		`CREATE TABLE IF NOT EXISTS model_versions(
			id TEXT PRIMARY KEY, model_id TEXT, version INTEGER,
			source_id TEXT, payload TEXT, net_json TEXT,
			initial_json TEXT, target_json TEXT, created_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS strategy_versions(
			id TEXT PRIMARY KEY, version INTEGER, source_id TEXT,
			payload TEXT, created_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS analyses(
			id TEXT PRIMARY KEY, model_version_id TEXT,
			strategy_version_id TEXT, ruleset_version INTEGER,
			status TEXT, result_json TEXT, cover_json TEXT, created_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS candidates(
			id TEXT PRIMARY KEY, analysis_id TEXT, kind TEXT,
			seq INTEGER, payload TEXT)`,
		`CREATE TABLE IF NOT EXISTS branches(
			id TEXT PRIMARY KEY, name TEXT, parent_branch_id TEXT,
			fork_event_id TEXT, head_event_id TEXT, head_seq INTEGER,
			created_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS events(
			id TEXT PRIMARY KEY, branch_id TEXT, seq INTEGER, type TEXT,
			actor TEXT, payload TEXT, model_version_id TEXT,
			strategy_version_id TEXT, analysis_id TEXT,
			ruleset_version INTEGER, created_at TEXT,
			UNIQUE(branch_id, seq))`,
		`CREATE TABLE IF NOT EXISTS decisions(
			id TEXT PRIMARY KEY, branch_id TEXT, event_id TEXT, kind TEXT,
			target_type TEXT, target_id TEXT, rationale TEXT,
			reverted INTEGER DEFAULT 0, created_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS invalidations(
			id TEXT PRIMARY KEY, trigger_event_id TEXT, affected_type TEXT,
			affected_id TEXT, reason TEXT, created_at TEXT)`,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version',?)`, fmt.Sprint(schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// nextID allocates a stable, monotonically increasing id for a table.
// Ids survive export/import because archives carry them verbatim.
func nextID(ctx context.Context, tx *sql.Tx, table, prefix string) (string, error) {
	var maxSeq sql.NullInt64
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT MAX(CAST(SUBSTR(id, ?) AS INTEGER)) FROM %s`, table),
		len(prefix)+2).Scan(&maxSeq)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%08d", prefix, maxSeq.Int64+1), nil
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

const timeLayout = "2006-01-02 15:04:05.999999Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
