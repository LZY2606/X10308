package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// withTx runs fn inside a transaction, committing on nil error.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CreateCase inserts a new dossier.
func (s *Store) CreateCase(ctx context.Context, c *Case) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cases(id,slug,title,description,rule_version,rev) VALUES(?,?,?,?,?,1)`,
		c.ID, c.Slug, c.Title, c.Description, c.RuleVersion)
	return err
}

// GetCase loads a dossier by id.
func (s *Store) GetCase(ctx context.Context, id string) (*Case, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,slug,title,description,rule_version,rev,created_at,updated_at FROM cases WHERE id=?`, id)
	c := &Case{}
	err := row.Scan(&c.ID, &c.Slug, &c.Title, &c.Description, &c.RuleVersion, &c.Rev, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListCases returns all dossiers in stable slug order.
func (s *Store) ListCases(ctx context.Context) ([]*Case, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,slug,title,description,rule_version,rev,created_at,updated_at FROM cases ORDER BY slug,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Case
	for rows.Next() {
		c := &Case{}
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title, &c.Description, &c.RuleVersion, &c.Rev, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// BumpCaseRev increments the optimistic revision and returns the new value.
func BumpCaseRev(tx *sql.Tx, ctx context.Context, id string, baseRev int64) (int64, error) {
	var cur int64
	if err := tx.QueryRowContext(ctx, `SELECT rev FROM cases WHERE id=?`, id).Scan(&cur); err != nil {
		return 0, err
	}
	if baseRev != 0 && cur != baseRev {
		return 0, fmt.Errorf("%w: case rev %d != base %d", ErrConflict, cur, baseRev)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE cases SET rev=rev+1, updated_at=datetime('now') WHERE id=?`, id); err != nil {
		return 0, err
	}
	return cur + 1, nil
}

// CreateBranch inserts a branch.
func (s *Store) CreateBranch(ctx context.Context, b *Branch) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO branches(id,case_id,parent_branch_id,name,kind,note,created_event_seq)
			 VALUES(?,?,?,?,?,?,?)`,
			b.ID, b.CaseID, nullStr(b.ParentBranchID), b.Name, b.Kind, b.Note, b.CreatedEventSeq)
		return err
	})
}

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// ListBranches returns a case's branches in creation order.
func (s *Store) ListBranches(ctx context.Context, caseID string) ([]*Branch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,case_id,COALESCE(parent_branch_id,''),name,kind,note,created_event_seq,created_at
		 FROM branches WHERE case_id=? ORDER BY created_event_seq,id`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Branch
	for rows.Next() {
		b := &Branch{}
		if err := rows.Scan(&b.ID, &b.CaseID, &b.ParentBranchID, &b.Name, &b.Kind, &b.Note,
			&b.CreatedEventSeq, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBranch loads one branch.
func (s *Store) GetBranch(ctx context.Context, id string) (*Branch, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,case_id,COALESCE(parent_branch_id,''),name,kind,note,created_event_seq,created_at
		 FROM branches WHERE id=?`, id)
	b := &Branch{}
	err := row.Scan(&b.ID, &b.CaseID, &b.ParentBranchID, &b.Name, &b.Kind, &b.Note,
		&b.CreatedEventSeq, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// CreateSource registers a versioned material aspect identity.
func (s *Store) CreateSource(ctx context.Context, src *Source) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sources(id,case_id,aspect,label) VALUES(?,?,?,?)`,
			src.ID, src.CaseID, src.Aspect, src.Label)
		return err
	})
}

// GetSourceByAspect finds the source identity for an aspect within a case.
func (s *Store) GetSourceByAspect(ctx context.Context, caseID, aspect string) (*Source, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,case_id,aspect,label FROM sources WHERE case_id=? AND aspect=?`, caseID, aspect)
	src := &Source{}
	err := row.Scan(&src.ID, &src.CaseID, &src.Aspect, &src.Label)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return src, err
}

// ListSources lists source identities in stable aspect order.
func (s *Store) ListSources(ctx context.Context, caseID string) ([]*Source, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,case_id,aspect,label FROM sources WHERE case_id=?`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byAspect := map[string]*Source{}
	var aspects []string
	for rows.Next() {
		src := &Source{}
		if err := rows.Scan(&src.ID, &src.CaseID, &src.Aspect, &src.Label); err != nil {
			return nil, err
		}
		byAspect[src.Aspect] = src
		aspects = append(aspects, src.Aspect)
	}
	var out []*Source
	for _, a := range Aspects {
		if src, ok := byAspect[a]; ok {
			out = append(out, src)
		}
	}
	for _, a := range aspects {
		if _, ok := byAspect[a]; !ok {
			out = append(out, byAspect[a])
		}
	}
	return out, rows.Err()
}

// AddSourceVersion appends an immutable version in a transaction.
func AddSourceVersion(tx *sql.Tx, ctx context.Context, sv *SourceVersion) error {
	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0)+1 FROM source_versions WHERE source_id=?`, sv.SourceID).
		Scan(&next); err != nil {
		return err
	}
	sv.Version = next
	_, err := tx.ExecContext(ctx,
		`INSERT INTO source_versions(id,source_id,version,raw_bytes,sha256,media_type)
		 VALUES(?,?,?,?,?,?)`,
		sv.ID, sv.SourceID, sv.Version, sv.RawBytes, sv.SHA256, sv.MediaType)
	return err
}

// GetSourceVersion loads one version's bytes.
func (s *Store) GetSourceVersion(ctx context.Context, sourceID string, version int) (*SourceVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,source_id,version,raw_bytes,sha256,media_type,created_at
		 FROM source_versions WHERE source_id=? AND version=?`, sourceID, version)
	sv := &SourceVersion{}
	err := row.Scan(&sv.ID, &sv.SourceID, &sv.Version, &sv.RawBytes, &sv.SHA256, &sv.MediaType, &sv.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sv, err
}

// ListSourceVersions lists versions in ascending order.
func (s *Store) ListSourceVersions(ctx context.Context, sourceID string) ([]*SourceVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,source_id,version,raw_bytes,sha256,media_type,created_at
		 FROM source_versions WHERE source_id=? ORDER BY version`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SourceVersion
	for rows.Next() {
		sv := &SourceVersion{}
		if err := rows.Scan(&sv.ID, &sv.SourceID, &sv.Version, &sv.RawBytes, &sv.SHA256,
			&sv.MediaType, &sv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// SetBranchAspect pins/updates an aspect version for a branch.
func SetBranchAspect(tx *sql.Tx, ctx context.Context, ba BranchAspect) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO branch_aspects(branch_id,aspect,source_id,version) VALUES(?,?,?,?)
		 ON CONFLICT(branch_id,aspect) DO UPDATE SET source_id=excluded.source_id, version=excluded.version`,
		ba.BranchID, ba.Aspect, ba.SourceID, ba.Version)
	return err
}

// BranchAspects returns the aspect pins of a branch in stable order.
func (s *Store) BranchAspects(ctx context.Context, branchID string) ([]BranchAspect, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT branch_id,aspect,source_id,version FROM branch_aspects WHERE branch_id=?`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byAspect := map[string]BranchAspect{}
	var found []string
	for rows.Next() {
		var ba BranchAspect
		if err := rows.Scan(&ba.BranchID, &ba.Aspect, &ba.SourceID, &ba.Version); err != nil {
			return nil, err
		}
		byAspect[ba.Aspect] = ba
		found = append(found, ba.Aspect)
	}
	var out []BranchAspect
	for _, a := range Aspects {
		if ba, ok := byAspect[a]; ok {
			out = append(out, ba)
		}
	}
	for _, a := range found {
		if _, ok := byAspect[a]; !ok {
			out = append(out, byAspect[a])
		}
	}
	return out, rows.Err()
}

// AppendEvent inserts an append-only event and returns its seq.
func AppendEvent(tx *sql.Tx, ctx context.Context, ev *Event) (int64, error) {
	deps, _ := json.Marshal(ev.DependsOn)
	if len(deps) == 0 {
		deps = []byte("[]")
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO events(event_id,case_id,branch_id,type,payload,depends_on,rev,undone)
		 VALUES(?,?,?,?,?,?,?,?)`,
		ev.EventID, ev.CaseID, ev.BranchID, ev.Type, string(ev.Payload), string(deps), ev.Rev, b2i(ev.Undone))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// MarkEventUndone flags an event as undone (append-only; never deleted).
func MarkEventUndone(tx *sql.Tx, ctx context.Context, seq int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE events SET undone=1 WHERE seq=?`, seq)
	return err
}

// ListEvents returns a branch's events in seq order (the replay timeline).
func (s *Store) ListEvents(ctx context.Context, branchID string) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq,event_id,case_id,branch_id,type,payload,depends_on,rev,undone,created_at
		 FROM events WHERE branch_id=? ORDER BY seq`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		ev := &Event{}
		var payload, deps string
		var undone int
		if err := rows.Scan(&ev.Seq, &ev.EventID, &ev.CaseID, &ev.BranchID, &ev.Type,
			&payload, &deps, &ev.Rev, &undone, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Payload = json.RawMessage(payload)
		_ = json.Unmarshal([]byte(deps), &ev.DependsOn)
		ev.Undone = undone == 1
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ListCaseEvents returns every event of a case ordered globally by seq.
func (s *Store) ListCaseEvents(ctx context.Context, caseID string) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq,event_id,case_id,branch_id,type,payload,depends_on,rev,undone,created_at
		 FROM events WHERE case_id=? ORDER BY seq`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		ev := &Event{}
		var payload, deps string
		var undone int
		if err := rows.Scan(&ev.Seq, &ev.EventID, &ev.CaseID, &ev.BranchID, &ev.Type,
			&payload, &deps, &ev.Rev, &undone, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Payload = json.RawMessage(payload)
		_ = json.Unmarshal([]byte(deps), &ev.DependsOn)
		ev.Undone = undone == 1
		out = append(out, ev)
	}
	return out, rows.Err()
}

// EventAtOrBefore returns the timeline state up to seq (as-of replay helper).
func (s *Store) CaseRevAt(ctx context.Context, caseID string) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx, `SELECT rev FROM cases WHERE id=?`, caseID).Scan(&rev)
	return rev, err
}

// WithTx exposes a transaction for the service layer.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.withTx(ctx, fn)
}

// Tx is re-exported for callers needing the concrete transaction type.
type Tx = sql.Tx

// ExecInTx runs fn using the provided transaction (used by rederive).
func ExecInTx(tx *sql.Tx, ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, query, args...)
}
