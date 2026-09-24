package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// UpsertInterpretation inserts a derived artifact (or returns the existing one
// for the same branch/kind/input_ref, since identical inputs are immutable).
func UpsertInterpretation(tx *sql.Tx, ctx context.Context, it *Interpretation) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO interpretations
		 (id,case_id,branch_id,kind,input_ref,sha256,payload,diagnostics,valid,created_event_seq)
		 VALUES(?,?,?,?,?,?,?,?,1,?)
		 ON CONFLICT(branch_id,kind,input_ref) DO NOTHING`,
		it.ID, it.CaseID, it.BranchID, it.Kind, it.InputRef, it.SHA256,
		string(it.Payload), string(it.Diagnostics), it.CreatedEventSeq)
	return err
}

// GetInterpretation loads a derived artifact by branch/kind/input.
func (s *Store) GetInterpretation(ctx context.Context, branchID, kind, inputRef string) (*Interpretation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,case_id,branch_id,kind,input_ref,sha256,payload,diagnostics,
		        valid,COALESCE(invalidated_by_event_seq,0),created_event_seq
		 FROM interpretations WHERE branch_id=? AND kind=? AND input_ref=?`,
		branchID, kind, inputRef)
	return scanInterpretation(row)
}

// ListInterpretations lists a branch's derived artifacts.
func (s *Store) ListInterpretations(ctx context.Context, branchID string) ([]*Interpretation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,case_id,branch_id,kind,input_ref,sha256,payload,diagnostics,
		        valid,COALESCE(invalidated_by_event_seq,0),created_event_seq
		 FROM interpretations WHERE branch_id=? ORDER BY created_event_seq,kind`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Interpretation
	for rows.Next() {
		it, err := scanInterpretation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInterpretation(r rowScanner) (*Interpretation, error) {
	it := &Interpretation{}
	var payload, diagnostics string
	var invalidSeq int64
	var valid int
	if err := r.Scan(&it.ID, &it.CaseID, &it.BranchID, &it.Kind, &it.InputRef, &it.SHA256,
		&payload, &diagnostics, &valid, &invalidSeq, &it.CreatedEventSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	it.Payload = json.RawMessage(payload)
	it.Diagnostics = json.RawMessage(diagnostics)
	it.Valid = valid == 1
	if invalidSeq > 0 {
		it.InvalidatedByEventSeq = &invalidSeq
	}
	return it, nil
}

// InvalidateInterpretationsByAspect marks derived artifacts stale when the
// aspect version they depend on changes. Scoped, not global: only rows whose
// deps mention the changed aspect are touched. Returns affected ids.
func InvalidateInterpretationsByAspect(tx *sql.Tx, ctx context.Context, caseID, branchID, depKey string, eventSeq int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM interpretations
		 WHERE case_id=? AND branch_id=? AND valid=1
		   AND kind IN ('net','marking','strategy','budget')
		   AND input_ref=?`, caseID, branchID, depKey)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE interpretations SET valid=0, invalidated_by_event_seq=? WHERE id=?`, eventSeq, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// InsertExploration stores a run with its dependency keys.
func InsertExploration(tx *sql.Tx, ctx context.Context, x *Exploration) error {
	deps, _ := json.Marshal(x.Deps)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO explorations(id,case_id,branch_id,question,goal,result,deps,valid,created_event_seq)
		 VALUES(?,?,?,?,?,?,?,1,?)`,
		x.ID, x.CaseID, x.BranchID, x.Question, x.Goal, string(x.Result), string(deps), x.CreatedEventSeq)
	return err
}

func scanExploration(r rowScanner) (*Exploration, error) {
	x := &Exploration{}
	var result, deps string
	var invalidSeq int64
	var valid int
	if err := r.Scan(&x.ID, &x.CaseID, &x.BranchID, &x.Question, &x.Goal,
		&result, &deps, &valid, &invalidSeq, &x.CreatedEventSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	x.Result = json.RawMessage(result)
	_ = json.Unmarshal([]byte(deps), &x.Deps)
	x.Valid = valid == 1
	if invalidSeq > 0 {
		x.InvalidatedByEventSeq = &invalidSeq
	}
	return x, nil
}

// GetExploration loads one run.
func (s *Store) GetExploration(ctx context.Context, id string) (*Exploration, error) {
	return scanExploration(s.db.QueryRowContext(ctx,
		`SELECT id,case_id,branch_id,question,goal,result,deps,valid,
		        COALESCE(invalidated_by_event_seq,0),created_event_seq
		 FROM explorations WHERE id=?`, id))
}

// ListExplorations lists a branch's runs, newest first.
func (s *Store) ListExplorations(ctx context.Context, branchID string) ([]*Exploration, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,case_id,branch_id,question,goal,result,deps,valid,
		        COALESCE(invalidated_by_event_seq,0),created_event_seq
		 FROM explorations WHERE branch_id=? ORDER BY created_event_seq DESC,id`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Exploration
	for rows.Next() {
		x, err := scanExploration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// InvalidateExplorations marks runs stale when any dependency key changes.
// Scoped: only runs whose deps intersect changedKeys are touched.
func InvalidateExplorations(tx *sql.Tx, ctx context.Context, caseID, branchID string, changedKeys []string, eventSeq int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id,deps FROM explorations WHERE case_id=? AND branch_id=? AND valid=1`,
		caseID, branchID)
	if err != nil {
		return nil, err
	}
	type pair struct {
		id   string
		deps []string
	}
	var pairs []pair
	changed := map[string]bool{}
	for _, k := range changedKeys {
		changed[k] = true
	}
	for rows.Next() {
		var id, depsJSON string
		if err := rows.Scan(&id, &depsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		var deps []string
		_ = json.Unmarshal([]byte(depsJSON), &deps)
		hit := false
		for _, d := range deps {
			if changed[d] {
				hit = true
				break
			}
		}
		if hit {
			pairs = append(pairs, pair{id, deps})
		}
	}
	rows.Close()
	var ids []string
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE explorations SET valid=0, invalidated_by_event_seq=? WHERE id=?`, eventSeq, p.id); err != nil {
			return nil, err
		}
		ids = append(ids, p.id)
	}
	return ids, nil
}

// InsertCandidate stores one candidate in stable ordinal order.
func InsertCandidate(tx *sql.Tx, ctx context.Context, c *Candidate) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO candidates(id,exploration_id,ord,label,payload) VALUES(?,?,?,?,?)`,
		c.ID, c.ExplorationID, c.Ord, c.Label, string(c.Payload))
	return err
}

// ListCandidates returns candidates of a run in ordinal order.
func (s *Store) ListCandidates(ctx context.Context, explorationID string) ([]*Candidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,exploration_id,ord,label,payload FROM candidates WHERE exploration_id=? ORDER BY ord`,
		explorationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Candidate
	for rows.Next() {
		c := &Candidate{}
		var payload string
		if err := rows.Scan(&c.ID, &c.ExplorationID, &c.Ord, &c.Label, &payload); err != nil {
			return nil, err
		}
		c.Payload = json.RawMessage(payload)
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCandidate loads one candidate.
func (s *Store) GetCandidate(ctx context.Context, id string) (*Candidate, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,exploration_id,ord,label,payload FROM candidates WHERE id=?`, id)
	c := &Candidate{}
	var payload string
	if err := row.Scan(&c.ID, &c.ExplorationID, &c.Ord, &c.Label, &payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Payload = json.RawMessage(payload)
	return c, nil
}

// InsertAdjudication stores a human decision.
func InsertAdjudication(tx *sql.Tx, ctx context.Context, a *Adjudication) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO adjudications(id,case_id,branch_id,candidate_id,decision,rationale,
		    base_rev,undone,created_event_seq,conflict_status)
		 VALUES(?,?,?,?,?,?,?,0,?,?)`,
		a.ID, a.CaseID, a.BranchID, nullStr(a.CandidateID), a.Decision, a.Rationale,
		a.BaseRev, a.CreatedEventSeq, a.ConflictStatus)
	return err
}

// UndoAdjudication flags the decision undone (audit trail retained).
func UndoAdjudication(tx *sql.Tx, ctx context.Context, id string, undoSeq int64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE adjudications SET undone=1, undo_event_seq=? WHERE id=?`, undoSeq, id)
	return err
}

// GetAdjudication loads one decision.
func (s *Store) GetAdjudication(ctx context.Context, id string) (*Adjudication, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,case_id,branch_id,COALESCE(candidate_id,''),decision,rationale,base_rev,
		        undone,COALESCE(undo_event_seq,0),created_event_seq,COALESCE(conflict_status,''),created_at
		 FROM adjudications WHERE id=?`, id)
	a := &Adjudication{}
	var undone, undoSeq int64
	if err := row.Scan(&a.ID, &a.CaseID, &a.BranchID, &a.CandidateID, &a.Decision, &a.Rationale,
		&a.BaseRev, &undone, &undoSeq, &a.CreatedEventSeq, &a.ConflictStatus, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.Undone = undone == 1
	if undoSeq > 0 {
		a.UndoEventSeq = &undoSeq
	}
	return a, nil
}

// ListAdjudications lists a branch's decisions, newest first.
func (s *Store) ListAdjudications(ctx context.Context, branchID string) ([]*Adjudication, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,case_id,branch_id,COALESCE(candidate_id,''),decision,rationale,base_rev,
		        undone,COALESCE(undo_event_seq,0),created_event_seq,COALESCE(conflict_status,''),created_at
		 FROM adjudications WHERE branch_id=? ORDER BY created_event_seq,id`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Adjudication
	for rows.Next() {
		a := &Adjudication{}
		var undone, undoSeq int64
		if err := rows.Scan(&a.ID, &a.CaseID, &a.BranchID, &a.CandidateID, &a.Decision, &a.Rationale,
			&a.BaseRev, &undone, &undoSeq, &a.CreatedEventSeq, &a.ConflictStatus, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Undone = undone == 1
		if undoSeq > 0 {
			a.UndoEventSeq = &undoSeq
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
