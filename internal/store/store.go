// Package store persists the workspace in SQLite. Every mutation happens
// inside a transaction so a failed import rolls back completely and never
// leaves half a record behind.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"petristudio/internal/journal"
)

const schemaV1 = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at TEXT NOT NULL,
  head_event_id TEXT, head_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE branches (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL,
  head_event_id TEXT, head_seq INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active',
  parent_branch_id TEXT, parent_event_id TEXT);
CREATE TABLE events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL, workspace_id TEXT NOT NULL, branch_id TEXT NOT NULL,
  type TEXT NOT NULL, actor TEXT NOT NULL, at TEXT NOT NULL,
  parent_hash TEXT NOT NULL, hash TEXT NOT NULL,
  base_event_id TEXT, payload_json TEXT NOT NULL, rules_json TEXT);
CREATE TABLE source_docs (
  id TEXT NOT NULL, workspace_id TEXT NOT NULL, branch_id TEXT NOT NULL,
  kind TEXT NOT NULL, revision TEXT NOT NULL, title TEXT,
  raw_sha TEXT NOT NULL, raw_bytes BLOB NOT NULL, payload_json TEXT NOT NULL,
  imported_event_id TEXT NOT NULL, created_seq INTEGER NOT NULL,
  PRIMARY KEY (id, workspace_id));
CREATE TABLE interpretations (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, branch_id TEXT NOT NULL,
  parent_id TEXT, net_doc_id TEXT NOT NULL, net_rev TEXT NOT NULL,
  strategy_doc_id TEXT NOT NULL, strategy_rev TEXT NOT NULL,
  change_note TEXT, aspects_json TEXT NOT NULL, normalized_json TEXT NOT NULL,
  diagnostics_json TEXT NOT NULL, rules_json TEXT NOT NULL,
  created_seq INTEGER NOT NULL, event_id TEXT NOT NULL);
CREATE TABLE runs (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, branch_id TEXT NOT NULL,
  interpretation_id TEXT NOT NULL, status TEXT NOT NULL, rules_json TEXT NOT NULL,
  result_json TEXT NOT NULL, cover_json TEXT NOT NULL,
  created_seq INTEGER NOT NULL, event_id TEXT NOT NULL,
  stale INTEGER NOT NULL DEFAULT 0, superseded_by TEXT);
CREATE TABLE candidates (
  id TEXT NOT NULL, run_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
  branch_id TEXT NOT NULL, kind TEXT NOT NULL, target_id TEXT, key TEXT,
  verdict TEXT NOT NULL, reason TEXT, path_json TEXT, deadlock_json TEXT,
  covered_by_key TEXT, depends_json TEXT NOT NULL, state_seq INTEGER,
  stale INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (id, run_id));
CREATE TABLE decisions (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, branch_id TEXT NOT NULL,
  candidate_id TEXT NOT NULL, run_id TEXT NOT NULL, action TEXT NOT NULL,
  rationale TEXT, actor TEXT NOT NULL, event_id TEXT NOT NULL,
  seq INTEGER NOT NULL, revoked_by_event_id TEXT);
`

const migrationV2 = `
CREATE TABLE net_summaries (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, branch_id TEXT NOT NULL,
  interpretation_id TEXT NOT NULL, summary_json TEXT NOT NULL);
CREATE INDEX idx_events_ws ON events(workspace_id, seq);
CREATE INDEX idx_candidates_ws ON candidates(workspace_id, branch_id);
`

// Store wraps the SQLite handle.
type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the database and applies migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	row := s.DB.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`)
	var v string
	if err := row.Scan(&v); err != nil {
		if _, err := s.DB.Exec(schemaV1); err != nil {
			return fmt.Errorf("schema v1: %w", err)
		}
		if _, err := s.DB.Exec(`INSERT INTO meta(key,value) VALUES('schema_version','1')`); err != nil {
			return err
		}
		version = 1
	} else {
		fmt.Sscanf(v, "%d", &version)
	}
	if version < 2 {
		if _, err := s.DB.Exec(migrationV2); err != nil {
			return fmt.Errorf("migration v2: %w", err)
		}
		if _, err := s.DB.Exec(`UPDATE meta SET value='2' WHERE key='schema_version'`); err != nil {
			return err
		}
	}
	return nil
}

// Close shuts the handle.
func (s *Store) Close() error { return s.DB.Close() }

// InTx runs fn in a transaction; any error rolls the whole change back.
func (s *Store) InTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// AppendEvent inserts a hash-chained event and advances the branch head.
func AppendEvent(tx *sql.Tx, wsID, branchID, evType, actor, baseEvID string, payload map[string]interface{}, rules []string) (*journal.Event, error) {
	var parentHash string
	var parentSeq int64
	row := tx.QueryRow(`SELECT hash, seq FROM events WHERE workspace_id=? ORDER BY seq DESC LIMIT 1`, wsID)
	switch err := row.Scan(&parentHash, &parentSeq); err {
	case nil:
	case sql.ErrNoRows:
		parentHash = "genesis"
	default:
		return nil, err
	}
	pj, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	rj, _ := json.Marshal(rules)
	ev := &journal.Event{
		Type: evType, BranchID: branchID, Actor: actor, At: time.Now().UTC(),
		ParentHash: parentHash, Payload: payload, Rules: rules, BaseEvID: baseEvID,
	}
	ev.ID = fmt.Sprintf("ev_%s_%d", wsID, parentSeq+1)
	ev.Hash = journal.HashOf(*ev)
	res, err := tx.Exec(`INSERT INTO events(id,workspace_id,branch_id,type,actor,at,parent_hash,hash,base_event_id,payload_json,rules_json)
	  VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		ev.ID, wsID, branchID, ev.Type, ev.Actor, ev.At.Format(time.RFC3339Nano), ev.ParentHash, ev.Hash, nullStr(baseEvID), string(pj), string(rj))
	if err != nil {
		return nil, err
	}
	ev.Seq, _ = res.LastInsertId()
	if _, err := tx.Exec(`UPDATE branches SET head_event_id=?, head_seq=? WHERE id=?`, ev.ID, ev.Seq, branchID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE workspaces SET head_event_id=?, head_seq=? WHERE id=?`, ev.ID, ev.Seq, wsID); err != nil {
		return nil, err
	}
	return ev, nil
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// BranchHead returns the head event id of a branch.
func (s *Store) BranchHead(branchID string) (string, int64, error) {
	var id string
	var seq int64
	err := s.DB.QueryRow(`SELECT head_event_id, head_seq FROM branches WHERE id=?`, branchID).Scan(&id, &seq)
	return id, seq, err
}

// Events lists events of a workspace, optionally one branch, up to a seq.
func (s *Store) Events(wsID, branchID string, uptoSeq int64) ([]journal.Event, error) {
	q := `SELECT seq,id,branch_id,type,actor,at,parent_hash,hash,COALESCE(base_event_id,''),payload_json,COALESCE(rules_json,'[]') FROM events WHERE workspace_id=?`
	args := []interface{}{wsID}
	if branchID != "" {
		q += ` AND branch_id=?`
		args = append(args, branchID)
	}
	if uptoSeq > 0 {
		q += ` AND seq<=?`
		args = append(args, uptoSeq)
	}
	q += ` ORDER BY seq`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []journal.Event
	for rows.Next() {
		var e journal.Event
		var at, pj, rj string
		if err := rows.Scan(&e.Seq, &e.ID, &e.BranchID, &e.Type, &e.Actor, &at, &e.ParentHash, &e.Hash, &e.BaseEvID, &pj, &rj); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		_ = json.Unmarshal([]byte(pj), &e.Payload)
		_ = json.Unmarshal([]byte(rj), &e.Rules)
		out = append(out, e)
	}
	return out, rows.Err()
}
