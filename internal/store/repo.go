package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrConflict is returned when an optimistic adjudication loses the race.
var ErrConflict = errors.New("revision conflict")

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

func nextSeq(tx *sql.Tx, table, seqCol, where string, args ...any) (int64, error) {
	q := fmt.Sprintf("SELECT COALESCE(MAX(%s),0)+1 FROM %s %s", seqCol, table, where)
	var v int64
	if err := tx.QueryRow(q, args...).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// AddMaterial inserts an already-versioned source material within a batch.
func (s *Store) AddMaterial(ctx context.Context, tx *sql.Tx, m MaterialRow, diags []json.RawMessage) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO material
(id, version, kind, title, raw_json, sha256, import_batch, supersedes)
VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, m.Version, m.Kind, m.Title, string(m.Raw), m.SHA256, m.ImportBatch, nullStr(m.Supersedes))
	if err != nil {
		return fmt.Errorf("add material %s: %w", m.ID, err)
	}
	for _, d := range diags {
		var diag struct {
			Path, Severity, Code, Message, RawValue string
		}
		_ = json.Unmarshal(d, &diag)
		_, err := tx.ExecContext(ctx, `INSERT INTO material_diagnostic
(material_id, version, path, severity, code, message, raw_value)
VALUES (?,?,?,?,?,?,?)`,
			m.ID, m.Version, diag.Path, diag.Severity, diag.Code, diag.Message, diag.RawValue)
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateBranch creates a branch with ordered material pins inside a tx.
func (s *Store) CreateBranch(ctx context.Context, tx *sql.Tx, b BranchRow) (int64, error) {
	seq, err := nextSeq(tx, "branch", "seq", "")
	if err != nil {
		return 0, err
	}
	b.Seq = seq
	open := 0
	if b.Open {
		open = 1
	}
	evSeq, err := s.RecordEvent(ctx, tx, Event{
		When: nowISO(), Type: EvBranchCreated, Actor: "system", BranchID: b.ID,
		RefType: "branch", RefID: b.ID,
		Payload: jsonStr(map[string]any{"title": b.Title, "parent_id": b.ParentID}),
	})
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO branch
(id, seq, title, parent_id, created_event, open) VALUES (?,?,?,?,?,?)`,
		b.ID, seq, b.Title, nullStr(b.ParentID), evSeq, open)
	if err != nil {
		return 0, err
	}
	for _, slot := range b.Materials {
		_, err := tx.ExecContext(ctx, `INSERT INTO branch_material
(branch_id, material_id, version, slot) VALUES (?,?,?,?)`,
			b.ID, slot.MaterialID, slot.Version, slot.Slot)
		if err != nil {
			return 0, err
		}
	}
	return evSeq, nil
}

// InsertModel persists a derived interpretation in a tx.
func (s *Store) InsertModel(ctx context.Context, tx *sql.Tx, m ModelRow) (int64, error) {
	seq, err := nextSeq(tx, "model", "seq", "")
	if err != nil {
		return 0, err
	}
	m.Seq = seq
	evSeq, err := s.RecordEvent(ctx, tx, Event{
		When: nowISO(), Type: EvModelInterpreted, Actor: "system", BranchID: m.BranchID,
		RefType: "model", RefID: m.ID,
		Payload: jsonStr(map[string]any{
			"material_id": m.MaterialID, "material_version": m.MaterialVersion,
			"rules_version": m.RulesVersion,
		}),
	})
	if err != nil {
		return 0, err
	}
	m.CreatedEvent = evSeq
	_, err = tx.ExecContext(ctx, `INSERT INTO model
(id, seq, branch_id, material_id, material_version, rules_version, derived_json, created_event)
VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, seq, m.BranchID, m.MaterialID, m.MaterialVersion, m.RulesVersion,
		string(m.Derived), evSeq)
	return evSeq, err
}

// InsertRun persists an exploration run and its truncation/enablement events.
func (s *Store) InsertRun(ctx context.Context, tx *sql.Tx, r RunRow, enablementEvents, truncPayload string) (int64, error) {
	seq, err := nextSeq(tx, "run", "seq", "")
	if err != nil {
		return 0, err
	}
	r.Seq = seq
	evSeq, err := s.RecordEvent(ctx, tx, Event{
		When: nowISO(), Type: EvRunExplored, Actor: "system", BranchID: r.BranchID,
		RefType: "run", RefID: r.ID,
		Payload: jsonStr(map[string]any{
			"model_id": r.ModelID, "strategy": r.Strategy,
			"fire_budget": r.FireBudget, "state_budget": r.StateBudget,
			"rules_version": r.RulesVersion,
		}),
	})
	if err != nil {
		return 0, err
	}
	r.CreatedEvent = evSeq
	if enablementEvents != "" {
		if _, err := s.RecordEvent(ctx, tx, Event{
			When: nowISO(), Type: EvEnablementChecked, Actor: "engine", BranchID: r.BranchID,
			RefType: "run", RefID: r.ID, Payload: enablementEvents,
		}); err != nil {
			return 0, err
		}
	}
	if truncPayload != "" {
		if _, err := s.RecordEvent(ctx, tx, Event{
			When: nowISO(), Type: EvFrontierTruncated, Actor: "engine", BranchID: r.BranchID,
			RefType: "run", RefID: r.ID, Payload: truncPayload,
		}); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run
(id, seq, branch_id, model_id, rules_version, strategy, fire_budget, state_budget,
 target_json, result_json, cover_json, created_event)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, seq, r.BranchID, r.ModelID, r.RulesVersion, r.Strategy,
		r.FireBudget, r.StateBudget, nullStr(string(r.Target)), string(r.Result),
		nullStr(string(r.Cover)), evSeq)
	return evSeq, err
}
