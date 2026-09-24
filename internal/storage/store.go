// Package storage is the SQLite-backed append-only event store for the
// Petri-net workspace.
//
// Source documents are versioned per kind and per branch. Branches are
// immutable event chains: every import, exploration, adjudication and
// parameter revision appends an event; nothing is ever rewritten in place.
// A failed import rolls its transaction back, so no half records remain.
package storage

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Store wraps the workspace database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database and runs pending migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// DB exposes the handle for transaction composition in higher layers.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		return err
	}
	for i := 0; i < len(migrations); i++ {
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, i+1).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		stmts := splitStatements(migrations[i])
		for _, stmt := range stmts {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration %d: %w: %s", i+1, err, truncate(stmt))
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func splitStatements(script string) []string {
	out := []string{}
	cur := strings.Builder{}
	inSingle := false
	for i := 0; i < len(script); i++ {
		ch := script[i]
		cur.WriteByte(ch)
		if ch == '\'' {
			if inSingle && i+1 < len(script) && script[i+1] == '\'' {
				cur.WriteByte(script[i+1])
				i++
				continue
			}
			inSingle = !inSingle
		}
		if ch == ';' && !inSingle {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

func truncate(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// MigrationVersion reports the applied schema version.
func (s *Store) MigrationVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v, err
}
