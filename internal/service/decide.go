package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"tflocal/internal/domain"
)

// Decide applies accept/reject/parameter-adjust to one candidate on a branch.
// expectedVersion is the branch version_counter the reviewer saw; a mismatch
// means another window committed first and returns ErrConflict plus both diffs.
type ConflictDetail struct {
	CurrentDecision string `json:"current_decision"`
	IncomingDecision string `json:"incoming_decision"`
	CurrentEventSeq  int    `json:"current_event_seq"`
	Message          string `json:"message"`
}

func (c ConflictDetail) Error() string { return c.Message }

func (s *Service) Decide(ctx context.Context, branchID, candidateID, decision, reviewer string, expectedVersion int) (int, error) {
	if decision != "accepted" && decision != "rejected" && decision != "adjusted" {
		return 0, fmt.Errorf("decision must be accepted|rejected|adjusted, got %q", decision)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var ver int
	if err := tx.QueryRowContext(ctx, `SELECT version_counter FROM branches WHERE branch_id=?`, branchID).Scan(&ver); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: branch %s", ErrNotFound, branchID)
		}
		return 0, err
	}
	if ver != expectedVersion {
		var cur string
		var curSeq sql.NullInt64
		_ = tx.QueryRowContext(ctx, `SELECT decision,decided_event_seq FROM candidates WHERE candidate_id=?`, candidateID).Scan(&cur, &curSeq)
		return ver, ConflictDetail{CurrentDecision: cur, IncomingDecision: decision,
			CurrentEventSeq: int(curSeq.Int64),
			Message: fmt.Sprintf("version conflict: branch now at v%d but client expected v%d", ver, expectedVersion)}
	}
	var scid, kind, label, outcome string
	if err := tx.QueryRowContext(ctx,
		`SELECT scenario_id,kind,label,outcome FROM candidates WHERE candidate_id=? AND branch_id=?`,
		candidateID, branchID).Scan(&scid, &kind, &label, &outcome); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: candidate %s", ErrNotFound, candidateID)
		}
		return 0, err
	}
	seq, err := nextSeq(ctx, tx)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE candidates SET decision=?, decided_event_seq=? WHERE candidate_id=?`, decision, seq, candidateID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE branches SET version_counter=version_counter+1 WHERE branch_id=?`, branchID); err != nil {
		return 0, err
	}
	var sourceID, pv string
	_ = tx.QueryRowContext(ctx, `SELECT source_id,params_version FROM branches WHERE branch_id=?`, branchID).Scan(&sourceID, &pv)
	if err := appendEvent(ctx, tx, seq, "ev_"+candidateID+"_d_"+fmt.Sprint(seq), branchID, "candidate_decided", map[string]any{
		"candidate_id": candidateID, "scenario_id": scid, "kind": kind, "label": label,
		"prior_outcome": outcome, "decision": decision, "reviewer": reviewer,
		"branch_version_before": expectedVersion, "branch_version_after": expectedVersion + 1,
		"reversible": true,
	}, sourceID, pv); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return ver + 1, nil
}

// UndoDecision appends a reversal event and restores the candidate to open.
// Any decision can be undone; history is never rewritten.
func (s *Service) UndoDecision(ctx context.Context, branchID, candidateID, reviewer string) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var ver int
	if err := tx.QueryRowContext(ctx, `SELECT version_counter FROM branches WHERE branch_id=?`, branchID).Scan(&ver); err != nil {
		return 0, err
	}
	var decision string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM candidates WHERE candidate_id=? AND branch_id=?`, candidateID, branchID).Scan(&decision); err != nil {
		return 0, err
	}
	if decision == "open" {
		return ver, nil
	}
	seq, err := nextSeq(ctx, tx)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE candidates SET decision='open', decided_event_seq=NULL WHERE candidate_id=?`, candidateID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE branches SET version_counter=version_counter+1 WHERE branch_id=?`, branchID); err != nil {
		return 0, err
	}
	var sourceID, pv string
	_ = tx.QueryRowContext(ctx, `SELECT source_id,params_version FROM branches WHERE branch_id=?`, branchID).Scan(&sourceID, &pv)
	if err := appendEvent(ctx, tx, seq, "ev_"+candidateID+"_u_"+fmt.Sprint(seq), branchID, "candidate_decision_undone", map[string]any{
		"candidate_id": candidateID, "reviewer": reviewer, "undone_decision": decision,
		"restored_to": "open", "branch_version_after": ver + 1,
	}, sourceID, pv); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return ver + 1, nil
}

// AsOf reconstructs the visible conclusions of a branch at a given event seq.
// It replays decision/undo events instead of reading the latest row state, so
// navigating to an old event restores that moment's view.
func (s *Service) AsOf(ctx context.Context, branchID string, asOfSeq int) (map[string]string, []map[string]any, error) {
	decisions := map[string]string{}
	var timeline []map[string]any
	rows, err := s.DB.QueryContext(ctx,
		`SELECT seq,event_id,type,payload,source_id,params_version FROM events WHERE branch_id=? AND seq<=? ORDER BY seq`,
		branchID, asOfSeq)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var eid, typ, payload string
		var src, pv sql.NullString
		if err := rows.Scan(&seq, &eid, &typ, &payload, &src, &pv); err != nil {
			return nil, nil, err
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(payload), &p)
		timeline = append(timeline, map[string]any{"seq": seq, "event_id": eid, "type": typ,
			"payload": p, "source_id": src.String, "params_version": pv.String})
		switch typ {
		case "candidate_decided":
			if cid, ok := p["candidate_id"].(string); ok {
				if d, ok := p["decision"].(string); ok {
					decisions[cid] = d
				}
			}
		case "candidate_decision_undone":
			if cid, ok := p["candidate_id"].(string); ok {
				decisions[cid] = "open"
			}
		}
	}
	return decisions, timeline, nil
}

// ParseParams helper exported for handlers.
func ParseParams(raw json.RawMessage) (domain.Params, error) {
	var p domain.Params
	err := json.Unmarshal(raw, &p)
	return p, err
}
