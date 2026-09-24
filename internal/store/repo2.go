package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// InsertCounterexample archives a finding in "pending" verdict. Both later
// accepted and rejected counterexamples remain readable.
func (s *Store) InsertCounterexample(ctx context.Context, tx *sql.Tx, c CounterexampleRow) (int64, error) {
	seq, err := nextSeq(tx, "counterexample", "seq", "")
	if err != nil {
		return 0, err
	}
	c.Seq = seq
	if c.Verdict == "" {
		c.Verdict = "pending"
	}
	evSeq, err := s.RecordEvent(ctx, tx, Event{
		When: nowISO(), Type: EvCounterexample, Actor: "engine", BranchID: c.BranchID,
		RefType: "counterexample", RefID: c.ID,
		Payload: jsonStr(map[string]any{
			"run_id": c.RunID, "scenario": c.Scenario, "title": c.Title,
		}),
	})
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO counterexample
(id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev, decided_event, decided_by)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, seq, c.BranchID, c.RunID, c.Scenario, c.Title, string(c.Detail),
		c.Verdict, 1, nil, "")
	return evSeq, err
}

// Decide applies accept/reject with optimistic revision control. On a lost
// race it records a decision.conflict event and returns ErrConflict together
// with the current row so the UI can show both sides of the difference.
func (s *Store) Decide(ctx context.Context, id, actor, action string, baseRev int, note string, payload map[string]any) (CounterexampleRow, CounterexampleRow, error) {
	var updated, current CounterexampleRow
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		row, err := scanCounterexample(tx.QueryRowContext(ctx,
			`SELECT id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev,
			        COALESCE(decided_event, 0), COALESCE(decided_by,'')
			 FROM counterexample WHERE id = ?`, id))
		if err != nil {
			return err
		}
		current = row
		if row.Rev != baseRev {
			pj, _ := json.Marshal(map[string]any{
				"id": id, "actor": actor, "attempted_action": action,
				"base_rev": baseRev, "current_rev": row.Rev,
				"current_verdict": row.Verdict, "current_by": row.DecidedBy, "note": note,
			})
			_, _ = s.RecordEvent(ctx, tx, Event{
				When: nowISO(), Type: EvDecisionConflict, Actor: actor, BranchID: row.BranchID,
				RefType: "counterexample", RefID: id, Payload: string(pj), BaseRev: &baseRev,
			})
			_, _ = tx.ExecContext(ctx, `INSERT INTO decision
(counterexample_id, at_event, actor, action, base_rev, new_rev, status, note, payload_json)
VALUES (?,?,?,?,?,?,?,?)`,
				id, nowISO(), actor, action, baseRev, nil, "conflict", note, string(pj))
			return ErrConflict
		}
		newRev := baseRev + 1
		evSeq, err := s.RecordEvent(ctx, tx, Event{
			When: nowISO(), Type: EvDecisionApplied, Actor: actor, BranchID: row.BranchID,
			RefType: "counterexample", RefID: id,
			Payload: jsonStr(map[string]any{
				"action": action, "old_rev": baseRev, "new_rev": newRev,
				"note": note, "payload": payload,
			}),
			BaseRev: &baseRev,
		})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE counterexample
SET verdict=?, rev=?, decided_event=?, decided_by=? WHERE id=? AND rev=?`,
			action, newRev, evSeq, actor, id, baseRev)
		if err != nil {
			return err
		}
		pj, _ := json.Marshal(payload)
		_, err = tx.ExecContext(ctx, `INSERT INTO decision
(counterexample_id, at_event, actor, action, base_rev, new_rev, status, note, payload_json)
VALUES (?,?,?,?,?,?,?,?,?)`,
			id, nowISO(), actor, action, baseRev, newRev, "applied", note, string(pj))
		if err != nil {
			return err
		}
		row.Verdict, row.Rev, row.DecidedEvent, row.DecidedBy = action, newRev, &evSeq, actor
		updated = row
		return nil
	})
	return updated, current, err
}

// UndoDecision reverses the most recent applied decision on a counterexample
// and restores the previous branch state (verdict pending, rev increments).
func (s *Store) UndoDecision(ctx context.Context, id, actor string) (CounterexampleRow, error) {
	var out CounterexampleRow
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		row, err := scanCounterexample(tx.QueryRowContext(ctx,
			`SELECT id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev,
			        COALESCE(decided_event, 0), COALESCE(decided_by,'')
			 FROM counterexample WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if row.Verdict == "pending" {
			return fmt.Errorf("counterexample %s has no applied decision to undo", id)
		}
		newRev := row.Rev + 1
		evSeq, err := s.RecordEvent(ctx, tx, Event{
			When: nowISO(), Type: EvDecisionUndone, Actor: actor, BranchID: row.BranchID,
			RefType: "counterexample", RefID: id,
			Payload: jsonStr(map[string]any{"old_rev": row.Rev, "new_rev": newRev,
				"restored_verdict": "pending"}),
			BaseRev: &row.Rev,
		})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE counterexample
SET verdict='pending', rev=?, decided_event=?, decided_by=? WHERE id=?`,
			newRev, evSeq, actor, id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO decision
(counterexample_id, at_event, actor, action, base_rev, new_rev, status, note, payload_json)
VALUES (?,?,?,?,?,?,?,?)`,
			id, nowISO(), actor, "undo", row.Rev, newRev, "undone",
			"restored previous branch state", "{}")
		if err != nil {
			return err
		}
		pending := int64(0)
		row.Verdict, row.Rev, row.DecidedEvent, row.DecidedBy = "pending", newRev, &pending, ""
		out = row
		return nil
	})
	return out, err
}

// AddDependency records one provenance edge (consumer depends on supplier).
func (s *Store) AddDependency(ctx context.Context, tx *sql.Tx, consumerType, consumerID, supplierType, supplierID string, supplierVersion int, rel string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO dependency
(consumer_type, consumer_id, supplier_type, supplier_id, supplier_version, rel)
VALUES (?,?,?,?,?,?)`,
		consumerType, consumerID, supplierType, supplierID, supplierVersion, rel)
	return err
}

// InvalidationScope computes the transitive blast radius of a changed
// supplier. Old conclusions are not replaced or deleted; callers record an
// invalidation row marking exactly the affected derived objects.
func (s *Store) InvalidationScope(ctx context.Context, supplierType, supplierID string) ([]string, error) {
	edges := map[string][]string{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT consumer_type || ':' || consumer_id, supplier_type || ':' || supplier_id FROM dependency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var consumer, supplier string
		if err := rows.Scan(&consumer, &supplier); err != nil {
			return nil, err
		}
		edges[supplier] = append(edges[supplier], consumer)
	}
	root := supplierType + ":" + supplierID
	seen := map[string]bool{}
	var stack []string
	stack = append(stack, edges[root]...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, edges[cur]...)
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out, nil
}

// RecordInvalidation persists a scoped invalidation event.
func (s *Store) RecordInvalidation(ctx context.Context, tx *sql.Tx, in InvalidationRow) (int64, error) {
	seq, err := nextSeq(tx, "invalidation", "seq", "")
	if err != nil {
		return 0, err
	}
	in.Seq = seq
	scope, _ := json.Marshal(in.Scope)
	evSeq, err := s.RecordEvent(ctx, tx, Event{
		When: in.At, Type: EvInvalidation, Actor: "system",
		RefType: in.CauseType, RefID: in.CauseID,
		Payload: jsonStr(map[string]any{
			"scope": json.RawMessage(scope), "reason_code": in.ReasonCode,
			"cause_version": in.CauseVersion,
		}),
	})
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO invalidation
(seq, at_event, scope_json, reason_code, cause_type, cause_id, cause_version)
VALUES (?,?,?,?,?,?,?)`,
		seq, in.At, string(scope), in.ReasonCode, in.CauseType, in.CauseID, in.CauseVersion)
	return evSeq, err
}
