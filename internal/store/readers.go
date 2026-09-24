package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// GetMaterial reads one version of a source material.
func (s *Store) GetMaterial(ctx context.Context, id string, version int) (MaterialRow, error) {
	var m MaterialRow
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, version, kind, title, raw_json, sha256, import_batch, COALESCE(supersedes,'')
		 FROM material WHERE id=? AND version=?`, id, version).
		Scan(&m.ID, &m.Version, &m.Kind, &m.Title, &raw, &m.SHA256, &m.ImportBatch, &m.Supersedes)
	if err != nil {
		return m, err
	}
	m.Raw = json.RawMessage(raw)
	return m, nil
}

// LatestMaterialVersion returns the newest committed version of a material.
func (s *Store) LatestMaterialVersion(ctx context.Context, id string) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM material WHERE id=?`, id).Scan(&v)
	return v, err
}

// ListMaterials returns all material versions in stable (id, version) order.
func (s *Store) ListMaterials(ctx context.Context) ([]MaterialRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, version, kind, title, raw_json, sha256, import_batch, COALESCE(supersedes,'')
		 FROM material ORDER BY id, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaterialRow
	for rows.Next() {
		var m MaterialRow
		var raw string
		if err := rows.Scan(&m.ID, &m.Version, &m.Kind, &m.Title, &raw,
			&m.SHA256, &m.ImportBatch, &m.Supersedes); err != nil {
			return nil, err
		}
		m.Raw = json.RawMessage(raw)
		out = append(out, m)
	}
	return out, nil
}

// Diagnostics returns the diagnostics kept for a material version.
func (s *Store) Diagnostics(ctx context.Context, id string, version int) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, severity, code, message, COALESCE(raw_value,'')
		 FROM material_diagnostic WHERE material_id=? AND version=? ORDER BY id`,
		id, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var p, sev, code, msg, raw string
		if err := rows.Scan(&p, &sev, &code, &msg, &raw); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"path": p, "severity": sev, "code": code, "message": msg, "raw_value": raw,
		})
	}
	return out, nil
}

// ListBranches returns branches with ordered material slots.
func (s *Store) ListBranches(ctx context.Context) ([]BranchRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, seq, title, COALESCE(parent_id,''), created_event, open FROM branch ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BranchRow
	for rows.Next() {
		var b BranchRow
		var open int
		if err := rows.Scan(&b.ID, &b.Seq, &b.Title, &b.ParentID, &b.CreatedEvent, &open); err != nil {
			return nil, err
		}
		b.Open = open == 1
		out = append(out, b)
	}
	for i := range out {
		slots, err := s.branchSlots(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Materials = slots
	}
	return out, nil
}

func (s *Store) branchSlots(ctx context.Context, branchID string) ([]MaterialSlot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT bm.material_id, bm.version, bm.slot, m.kind, m.title
		 FROM branch_material bm JOIN material m
		 ON m.id=bm.material_id AND m.version=bm.version
		 WHERE bm.branch_id=? ORDER BY bm.slot`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaterialSlot
	for rows.Next() {
		var sl MaterialSlot
		if err := rows.Scan(&sl.MaterialID, &sl.Version, &sl.Slot, &sl.Kind, &sl.Title); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, nil
}

// GetBranch returns one branch including slots.
func (s *Store) GetBranch(ctx context.Context, id string) (BranchRow, error) {
	var b BranchRow
	var open int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, seq, title, COALESCE(parent_id,''), created_event, open FROM branch WHERE id=?`,
		id).Scan(&b.ID, &b.Seq, &b.Title, &b.ParentID, &b.CreatedEvent, &open)
	if err != nil {
		return b, err
	}
	b.Open = open == 1
	b.Materials, err = s.branchSlots(ctx, id)
	return b, err
}

// LatestModelForBranch returns the newest derived model of a branch.
func (s *Store) LatestModelForBranch(ctx context.Context, branchID string) (ModelRow, error) {
	return s.modelQuery(ctx, `ORDER BY seq DESC LIMIT 1`, branchID)
}

// GetModel reads one derived model.
func (s *Store) GetModel(ctx context.Context, id string) (ModelRow, error) {
	return s.modelQuery(ctx, `WHERE m.id=?`, id)
}

func (s *Store) modelQuery(ctx context.Context, suffix string, arg string) (ModelRow, error) {
	q := `SELECT m.id, m.seq, m.branch_id, m.material_id, m.material_version,
	             m.rules_version, m.derived_json, m.created_event
	      FROM model m ` + suffix
	var m ModelRow
	var derived string
	var query string
	var args []any
	switch suffix {
	case `ORDER BY seq DESC LIMIT 1`:
		query = `SELECT m.id, m.seq, m.branch_id, m.material_id, m.material_version,
		                m.rules_version, m.derived_json, m.created_event
		         FROM model m WHERE m.branch_id=? ORDER BY m.seq DESC LIMIT 1`
		args = []any{arg}
	default:
		query = q
		args = []any{arg}
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&m.ID, &m.Seq, &m.BranchID, &m.MaterialID, &m.MaterialVersion,
		&m.RulesVersion, &derived, &m.CreatedEvent)
	if err != nil {
		return m, err
	}
	m.Derived = json.RawMessage(derived)
	return m, nil
}

// GetRun reads one run.
func (s *Store) GetRun(ctx context.Context, id string) (RunRow, error) {
	var r RunRow
	var result string
	var target, cover sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, seq, branch_id, model_id, rules_version, strategy,
		        fire_budget, state_budget, target_json, result_json, cover_json, created_event
		 FROM run WHERE id=?`, id).Scan(
		&r.ID, &r.Seq, &r.BranchID, &r.ModelID, &r.RulesVersion, &r.Strategy,
		&r.FireBudget, &r.StateBudget, &target, &result, &cover, &r.CreatedEvent)
	if err != nil {
		return r, err
	}
	r.Result = json.RawMessage(result)
	if target.Valid {
		r.Target = json.RawMessage(target.String)
	}
	if cover.Valid {
		r.Cover = json.RawMessage(cover.String)
	}
	return r, nil
}

// LatestRunForBranch returns the newest run on a branch.
func (s *Store) LatestRunForBranch(ctx context.Context, branchID string) (RunRow, error) {
	id := ""
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM run WHERE branch_id=? ORDER BY seq DESC LIMIT 1`, branchID).Scan(&id)
	if err != nil {
		return RunRow{}, err
	}
	return s.GetRun(ctx, id)
}

// ListCounterexamples returns archived findings ordered by stable seq.
func (s *Store) ListCounterexamples(ctx context.Context, branchID string) ([]CounterexampleRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev,
		        COALESCE(decided_event,0), COALESCE(decided_by,'')
		 FROM counterexample WHERE branch_id=? ORDER BY seq`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCExRows(rows)
}

// AllCounterexamples returns findings across branches.
func (s *Store) AllCounterexamples(ctx context.Context) ([]CounterexampleRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev,
		        COALESCE(decided_event,0), COALESCE(decided_by,'')
		 FROM counterexample ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCExRows(rows)
}

func scanCExRows(rows *sql.Rows) ([]CounterexampleRow, error) {
	var out []CounterexampleRow
	for rows.Next() {
		var c CounterexampleRow
		var detail string
		var decidedEvent int64
		if err := rows.Scan(&c.ID, &c.Seq, &c.BranchID, &c.RunID, &c.Scenario,
			&c.Title, &detail, &c.Verdict, &c.Rev, &decidedEvent, &c.DecidedBy); err != nil {
			return nil, err
		}
		c.Detail = json.RawMessage(detail)
		if decidedEvent > 0 {
			c.DecidedEvent = &decidedEvent
		}
		out = append(out, c)
	}
	return out, nil
}

// Events returns the append-only ledger in chronological order.
func (s *Store) Events(ctx context.Context, branchID string) ([]EventRow, error) {
	q := `SELECT seq, at_event, event_type, actor, COALESCE(branch_id,''),
	             COALESCE(ref_type,''), COALESCE(ref_id,''), payload_json, COALESCE(base_rev,0)
	      FROM event_log`
	args := []any{}
	if branchID != "" {
		q += ` WHERE branch_id=?`
		args = append(args, branchID)
	}
	q += ` ORDER BY seq`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var payload string
		var baseRev int
		if err := rows.Scan(&e.Seq, &e.When, &e.Type, &e.Actor, &e.BranchID,
			&e.RefType, &e.RefID, &payload, &baseRev); err != nil {
			return nil, err
		}
		e.PayloadV = json.RawMessage(payload)
		if baseRev > 0 {
			e.BaseRev = &baseRev
		}
		out = append(out, e)
	}
	return out, nil
}

// EventAt returns the ledger state up to (and including) a sequence.
func (s *Store) EventAt(ctx context.Context, seq int64) (EventRow, error) {
	var e EventRow
	var payload string
	var baseRev int
	err := s.db.QueryRowContext(ctx,
		`SELECT seq, at_event, event_type, actor, COALESCE(branch_id,''),
		        COALESCE(ref_type,''), COALESCE(ref_id,''), payload_json, COALESCE(base_rev,0)
		 FROM event_log WHERE seq=?`, seq).Scan(
		&e.Seq, &e.When, &e.Type, &e.Actor, &e.BranchID,
		&e.RefType, &e.RefID, &payload, &baseRev)
	if err != nil {
		return e, fmt.Errorf("event %d: %w", seq, err)
	}
	e.PayloadV = json.RawMessage(payload)
	return e, nil
}

// Decisions lists the decision audit trail.
func (s *Store) Decisions(ctx context.Context, counterexampleID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id, d.counterexample_id, d.at_event, d.actor, d.action,
		        d.base_rev, COALESCE(d.new_rev,0), d.status, COALESCE(d.note,''), d.payload_json
		 FROM decision d WHERE d.counterexample_id=? ORDER BY d.id`, counterexampleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var cid, at, actor, action, status, note, payload string
		var baseRev, newRev int
		if err := rows.Scan(&id, &cid, &at, &actor, &action, &baseRev, &newRev, &status, &note, &payload); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "counterexample_id": cid, "at": at, "actor": actor,
			"action": action, "base_rev": baseRev, "new_rev": newRev,
			"status": status, "note": note, "payload": json.RawMessage(payload),
		})
	}
	return out, nil
}
