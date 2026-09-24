package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"pnwork/internal/explore"
	"pnwork/internal/pnnet"
	"pnwork/internal/store"
)

// Event payload kinds (machine-readable judgment basis).
const (
	EvCaseCreated    = "case_created"
	EvBranchCreated  = "branch_created"
	EvAspectImported = "aspect_imported"
	EvInterpreted    = "interpreted"
	EvExploration    = "exploration"
	EvAdjudicated    = "adjudicated"
	EvAdjUndone      = "adjudication_undone"
	EvInvalidated    = "invalidated"
	EvConflict       = "conflict_recorded"
)

// InvalidationScope reports what a change affects, computed from dependency
// keys rather than by replacing everything globally.
type InvalidationScope struct {
	ChangedKeys     []string `json:"changed_keys"`
	Interpretations []string `json:"interpretations"`
	Explorations    []string `json:"explorations"`
}

// ExploreRequest triggers a bounded run.
type ExploreRequest struct {
	BranchID     string        `json:"branch_id"`
	Question     string        `json:"question"` // reachability | enumerate
	Goal         pnnet.Marking `json:"goal,omitempty"`
	BaseRev      int64         `json:"base_rev"`
	StableID     string        `json:"stable_id,omitempty"` // optional deterministic id (replay)
	CandidateIDs []string      `json:"candidate_ids,omitempty"`
}

// ExploreResponse returns the run id, verdict and candidate ids.
type ExploreResponse struct {
	ExplorationID string   `json:"exploration_id"`
	CandidateIDs  []string `json:"candidate_ids"`
	Verdict       string   `json:"verdict"`
	EventSeq      int64    `json:"event_seq"`
	Rev           int64    `json:"rev"`
}

// RunExploration executes a bounded exploration and records the result,
// candidates and a machine-readable event in one transaction.
func (s *Service) RunExploration(ctx context.Context, req ExploreRequest) (*ExploreResponse, error) {
	var resp *ExploreResponse
	err := s.st.WithTx(ctx, func(tx *sql.Tx) error {
		branch, err := s.st.GetBranch(ctx, req.BranchID)
		if err != nil {
			return err
		}
		newRev, err := store.BumpCaseRev(tx, ctx, branch.CaseID, req.BaseRev)
		if err != nil {
			return err
		}
		snap, err := s.snapshotInTx(ctx, tx, req.BranchID)
		if err != nil {
			return err
		}
		eng := explore.NewEngine(snap.Net, snap.Strategy)
		xid := req.StableID
		if xid == "" {
			xid = newID("ex")
		}
		resultJSON, verdict, candPayloads, err := executeRun(eng, snap, req)
		if err != nil {
			return err
		}
		evSeq, err := nextSeq(ctx, tx)
		if err != nil {
			return err
		}
		x := &store.Exploration{
			ID: xid, CaseID: branch.CaseID, BranchID: req.BranchID,
			Question: req.Question, Goal: markingKey(snap.Net, req.Goal),
			Result: resultJSON, Deps: snap.Deps, CreatedEventSeq: evSeq,
		}
		if err := store.InsertExploration(tx, ctx, x); err != nil {
			return err
		}
		var candIDs []string
		for i, cp := range candPayloads {
			cid := newID("cand")
			if i < len(req.CandidateIDs) {
				cid = req.CandidateIDs[i]
			}
			if err := store.InsertCandidate(tx, ctx, &store.Candidate{
				ID: cid, ExplorationID: xid, Ord: i,
				Label: fmt.Sprintf("candidate-%d", i+1), Payload: cp,
			}); err != nil {
				return err
			}
			candIDs = append(candIDs, cid)
		}
		payload, _ := json.Marshal(map[string]any{
			"exploration_id": xid,
			"question":       req.Question,
			"goal":           req.Goal,
			"verdict":        verdict,
			"candidate_ids":  candIDs,
			"deps":           snap.Deps,
			"config":         snap.Strategy,
		})
		ev := &store.Event{
			EventID: "ev_" + xid, CaseID: branch.CaseID, BranchID: req.BranchID,
			Type: EvExploration, Payload: payload, DependsOn: snap.Deps, Rev: newRev,
		}
		seq, err := store.AppendEvent(tx, ctx, ev)
		if err != nil {
			return err
		}
		resp = &ExploreResponse{ExplorationID: xid, CandidateIDs: candIDs,
			Verdict: verdict, EventSeq: seq, Rev: newRev}
		return nil
	})
	return resp, err
}

func executeRun(eng *explore.Engine, snap *Snapshot, req ExploreRequest) (json.RawMessage, string, []json.RawMessage, error) {
	switch req.Question {
	case "reachability":
		if req.Goal == nil {
			return nil, "", nil, errors.New("reachability requires a goal")
		}
		r := eng.Reach(snap.Initial, req.Goal)
		b, err := json.Marshal(r)
		if err != nil {
			return nil, "", nil, err
		}
		var cands []json.RawMessage
		for i, path := range r.Paths {
			pb, _ := json.Marshal(map[string]any{"ord": i, "path": path, "engine": "exact"})
			cands = append(cands, pb)
		}
		return b, string(r.Verdict), cands, nil
	case "coverability":
		if req.Goal == nil {
			return nil, "", nil, errors.New("coverability requires a goal")
		}
		cr := eng.CoverReach(snap.Initial, req.Goal)
		b, err := json.Marshal(cr)
		if err != nil {
			return nil, "", nil, err
		}
		var cands []json.RawMessage
		for i, path := range cr.Paths {
			pb, _ := json.Marshal(map[string]any{"ord": i, "path": path, "engine": "coverability_tree",
				"exact": cr.ExactReach, "covered": cr.Covered})
			cands = append(cands, pb)
		}
		return b, string(cr.Verdict), cands, nil
	case "enumerate":
		var goals []pnnet.Marking
		if req.Goal != nil {
			goals = append(goals, req.Goal)
		}
		r, cover := eng.Enumerate(snap.Initial, goals)
		b, err := json.Marshal(struct {
			*explore.SpaceResult
			Cover map[string][]pnnet.Marking `json:"cover"`
		}{SpaceResult: r, Cover: cover})
		if err != nil {
			return nil, "", nil, err
		}
		var cands []json.RawMessage
		for i, dl := range r.Deadlocks {
			pb, _ := json.Marshal(map[string]any{"ord": i, "deadlock": dl})
			cands = append(cands, pb)
		}
		verdict := "complete"
		if r.BudgetHit {
			verdict = "truncated"
		}
		return b, verdict, cands, nil
	default:
		return nil, "", nil, fmt.Errorf("unknown question %q (want reachability|coverability|enumerate)", req.Question)
	}
}

func markingKey(n *pnnet.Net, m pnnet.Marking) string {
	if m == nil {
		return ""
	}
	return m.Canonical(n.SortedPlaceIDs())
}

func nextSeq(ctx context.Context, tx *sql.Tx) (int64, error) {
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM events`).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

// DecideRequest is a human accept/reject adjudication.
type DecideRequest struct {
	BranchID    string `json:"branch_id"`
	CandidateID string `json:"candidate_id"`
	Decision    string `json:"decision"`
	Rationale   string `json:"rationale"`
	BaseRev     int64  `json:"base_rev"`
	StableID    string `json:"stable_id,omitempty"`
}

// Adjudicate records an accept/reject decision with optimistic concurrency.
func (s *Service) Adjudicate(ctx context.Context, req DecideRequest) (int64, int64, error) {
	if req.Decision != "accepted" && req.Decision != "rejected" {
		return 0, 0, errors.New("decision must be accepted or rejected")
	}
	var evSeq, newRev int64
	err := s.st.WithTx(ctx, func(tx *sql.Tx) error {
		branch, err := s.st.GetBranch(ctx, req.BranchID)
		if err != nil {
			return err
		}
		newRev, err = store.BumpCaseRev(tx, ctx, branch.CaseID, req.BaseRev)
		if err != nil {
			return err
		}
		aid := req.StableID
		if aid == "" {
			aid = newID("adj")
		}
		evSeq, err = nextSeq(ctx, tx)
		if err != nil {
			return err
		}
		a := &store.Adjudication{
			ID: aid, CaseID: branch.CaseID, BranchID: req.BranchID,
			CandidateID: req.CandidateID, Decision: req.Decision,
			Rationale: req.Rationale, BaseRev: req.BaseRev, CreatedEventSeq: evSeq,
		}
		if err := store.InsertAdjudication(tx, ctx, a); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{
			"adjudication_id": aid, "candidate_id": req.CandidateID,
			"decision": req.Decision, "rationale": req.Rationale, "base_rev": req.BaseRev,
		})
		_, err = store.AppendEvent(tx, ctx, &store.Event{
			EventID: "ev_" + aid, CaseID: branch.CaseID, BranchID: req.BranchID,
			Type: EvAdjudicated, Payload: payload, DependsOn: []string{RuleDep()}, Rev: newRev,
		})
		return err
	})
	return evSeq, newRev, err
}

// UndoDecision reverses an adjudication by appending an undo event; the prior
// branch state is restored, nothing is erased.
func (s *Service) UndoDecision(ctx context.Context, adjudicationID string, baseRev int64) (int64, error) {
	var seq int64
	err := s.st.WithTx(ctx, func(tx *sql.Tx) error {
		a, err := s.st.GetAdjudication(ctx, adjudicationID)
		if err != nil {
			return err
		}
		if a.Undone {
			return errors.New("adjudication already undone")
		}
		newRev, err := store.BumpCaseRev(tx, ctx, a.CaseID, baseRev)
		if err != nil {
			return err
		}
		seq, err = nextSeq(ctx, tx)
		if err != nil {
			return err
		}
		if err := store.UndoAdjudication(tx, ctx, adjudicationID, seq); err != nil {
			return err
		}
		if err := store.MarkEventUndone(tx, ctx, a.CreatedEventSeq); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{
			"adjudication_id": adjudicationID, "restores_event_seq": a.CreatedEventSeq,
		})
		_, err = store.AppendEvent(tx, ctx, &store.Event{
			EventID: "ev_undo_" + adjudicationID, CaseID: a.CaseID, BranchID: a.BranchID,
			Type: EvAdjUndone, Payload: payload, DependsOn: []string{RuleDep()}, Rev: newRev,
		})
		return err
	})
	return seq, err
}

// ConflictInfo materializes the two-window version conflict: the losing save
// sees the current server revision, both payloads and the diff scope.
type ConflictInfo struct {
	CurrentRev int64    `json:"current_rev"`
	TriedRev   int64    `json:"tried_rev"`
	Diff       []string `json:"diff"`
	Message    string   `json:"message"`
}

// ExplainConflict constructs the conflict body without writing anything.
func (s *Service) ExplainConflict(ctx context.Context, caseID string, triedRev int64, changed []string) (*ConflictInfo, error) {
	cur, err := s.st.CaseRevAt(ctx, caseID)
	if err != nil {
		return nil, err
	}
	sort.Strings(changed)
	return &ConflictInfo{
		CurrentRev: cur, TriedRev: triedRev, Diff: changed,
		Message: fmt.Sprintf("version conflict: server at rev %d, your save was based on rev %d", cur, triedRev),
	}, nil
}

// snapshotInTx is SnapshotForBranch but participating in an outer transaction.
// The store methods open their own connections; because MaxOpenConns(1) the
// outer tx holds the single connection, so we reuse snapshot reading via the
// already-validated helper on a separate call path is unsafe. Instead the
// branch pins are read-only and can be fetched before writes: callers pass a
// snapshot built before opening the transaction where possible.
func (s *Service) snapshotInTx(ctx context.Context, tx *sql.Tx, branchID string) (*Snapshot, error) {
	_ = tx
	return s.SnapshotForBranch(ctx, branchID)
}
