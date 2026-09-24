package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"pnwork/internal/explore"
	"pnwork/internal/pnnet"
	"pnwork/internal/store"
)

// rederiveInTx rebuilds interpretations, explorations and candidates from the
// pinned source versions and the seeded event timeline. It is deterministic,
// so the same archive yields the same ids, ordering and provenance after the
// local database is deleted and the bundle re-imported.
func (s *Service) rederiveInTx(ctx context.Context, tx *sql.Tx, b *Bundle, ordered []BundleBranch) error {
	for _, bb := range ordered {
		if bb.ID == "" {
			continue
		}
		snap, err := s.SnapshotForBranch(ctx, bb.ID)
		if err != nil {
			return err
		}
		// Interpreted artifacts (one per aspect), tied to the creating event.
		events := branchSeedEvents(b, bb.ID)
		firstSeq := int64(0)
		if len(events) > 0 {
			firstSeq = events[0].Seq
		}
		for _, aspect := range store.Aspects {
			sha := snap.InterpSHA[aspect]
			ver := snap.AspectVers[aspect]
			if err := store.UpsertInterpretation(tx, ctx, &store.Interpretation{
				ID:              "int_" + bb.ID + "_" + aspect,
				CaseID:          b.Case.ID,
				BranchID:        bb.ID,
				Kind:            aspectKind(aspect),
				InputRef:        DepKey(aspect, ver),
				SHA256:          sha,
				Payload:         json.RawMessage(`{"aspect":"` + aspect + `"}`),
				Diagnostics:     json.RawMessage("[]"),
				CreatedEventSeq: firstSeq,
			}); err != nil {
				return err
			}
		}

		eng := explore.NewEngine(snap.Net, snap.Strategy)
		for _, ev := range events {
			if ev.Type != EvExploration {
				continue
			}
			var pl struct {
				ExplorationID string           `json:"exploration_id"`
				Question      string           `json:"question"`
				Goal          map[string]int64 `json:"goal"`
				CandidateIDs  []string         `json:"candidate_ids"`
			}
			if err := json.Unmarshal(ev.Payload, &pl); err != nil {
				return err
			}
			req := ExploreRequest{
				BranchID: bb.ID, Question: pl.Question,
				Goal: pnnet.Marking(pl.Goal), StableID: pl.ExplorationID,
				CandidateIDs: pl.CandidateIDs,
			}
			resultJSON, _, candPayloads, err := executeRun(eng, snap, req)
			if err != nil {
				return err
			}
			x := &store.Exploration{
				ID: pl.ExplorationID, CaseID: b.Case.ID, BranchID: bb.ID,
				Question: pl.Question, Goal: markingKey(snap.Net, req.Goal),
				Result: resultJSON, Deps: snap.Deps, CreatedEventSeq: ev.Seq,
			}
			if err := store.InsertExploration(tx, ctx, x); err != nil {
				return err
			}
			for i, cp := range candPayloads {
				cid := fmt.Sprintf("cand_%s_%d", pl.ExplorationID, i+1)
				if i < len(pl.CandidateIDs) {
					cid = pl.CandidateIDs[i]
				}
				if err := store.InsertCandidate(tx, ctx, &store.Candidate{
					ID: cid, ExplorationID: pl.ExplorationID, Ord: i,
					Label: fmt.Sprintf("candidate-%d", i+1), Payload: cp,
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func branchSeedEvents(b *Bundle, branchID string) []BundleEvent {
	var out []BundleEvent
	for _, ev := range b.SeedEvents {
		if ev.BranchID == branchID {
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func aspectKind(aspect string) string {
	switch aspect {
	case "model", "identity", "weights", "capacity":
		return "net"
	case "initial_tokens":
		return "marking"
	case "strategy":
		return "strategy"
	case "budget":
		return "budget"
	}
	return aspect
}

// ForkRequest creates a new interpretation branch from an existing one.
type ForkRequest struct {
	CaseID   string `json:"case_id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Note     string `json:"note"`
	StableID string `json:"stable_id,omitempty"`
	BaseRev  int64  `json:"base_rev"`
}

// ForkBranch copies aspect pins into a new branch and appends a branch event.
// The old branch, its decisions and referenced raw bytes stay readable.
func (s *Service) ForkBranch(ctx context.Context, req ForkRequest) (string, int64, error) {
	branchID := req.StableID
	if branchID == "" {
		branchID = newID("br")
	}
	var newRev int64
	err := s.st.WithTx(ctx, func(tx *sql.Tx) error {
		nr, err := store.BumpCaseRev(tx, ctx, req.CaseID, req.BaseRev)
		if err != nil {
			return err
		}
		newRev = nr
		pins, err := s.st.BranchAspects(ctx, req.ParentID)
		if err != nil {
			return err
		}
		var seq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM events`).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO branches(id,case_id,parent_branch_id,name,kind,note,created_event_seq)
			 VALUES(?,?,?,?,?,?,?)`,
			branchID, req.CaseID, req.ParentID, req.Name, req.Kind, req.Note, seq); err != nil {
			return err
		}
		for _, p := range pins {
			if err := store.SetBranchAspect(tx, ctx, store.BranchAspect{
				BranchID: branchID, Aspect: p.Aspect, SourceID: p.SourceID, Version: p.Version,
			}); err != nil {
				return err
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"branch_id": branchID, "parent_branch_id": req.ParentID,
			"kind": req.Kind, "name": req.Name, "pins": pins,
		})
		_, err = store.AppendEvent(tx, ctx, &store.Event{
			EventID: "ev_" + branchID, CaseID: req.CaseID, BranchID: branchID,
			Type: EvBranchCreated, Payload: payload,
			DependsOn: []string{RuleDep()}, Rev: newRev,
		})
		return err
	})
	return branchID, newRev, err
}

// AspectUpdate appends a new version to one source aspect, repins the branch,
// and computes the scoped invalidation. Only derived results depending on that
// aspect are marked stale; older conclusions remain in history.
type AspectUpdate struct {
	CaseID    string `json:"case_id"`
	BranchID  string `json:"branch_id"`
	Aspect    string `json:"aspect"`
	Content   []byte `json:"content"`
	MediaType string `json:"media_type"`
	Label     string `json:"label"`
	Note      string `json:"note"`
	BaseRev   int64  `json:"base_rev"`
	Validate  func([]byte) error
}

// AspectUpdateResult reports the new version and invalidation scope.
type AspectUpdateResult struct {
	SourceID string            `json:"source_id"`
	Version  int               `json:"version"`
	EventSeq int64             `json:"event_seq"`
	Rev      int64             `json:"rev"`
	Scope    InvalidationScope `json:"scope"`
}

// UpdateAspect performs a single-aspect versioned change.
func (s *Service) UpdateAspect(ctx context.Context, u AspectUpdate) (*AspectUpdateResult, error) {
	if !validAspect(u.Aspect) {
		return nil, fmt.Errorf("unknown aspect %q", u.Aspect)
	}
	if u.Validate != nil {
		if err := u.Validate(u.Content); err != nil {
			return nil, err // whole import rolls back; no half record
		}
	}
	out := &AspectUpdateResult{}
	err := s.st.WithTx(ctx, func(tx *sql.Tx) error {
		newRev, err := store.BumpCaseRev(tx, ctx, u.CaseID, u.BaseRev)
		if err != nil {
			return err
		}
		out.Rev = newRev
		src, err := s.st.GetSourceByAspect(ctx, u.CaseID, u.Aspect)
		if err != nil {
			return err
		}
		out.SourceID = src.ID
		sv := &store.SourceVersion{
			SourceID: src.ID, RawBytes: u.Content,
			SHA256: digestOf(u.Content), MediaType: mediaOr(u.MediaType),
		}
		if err := store.AddSourceVersion(tx, ctx, sv); err != nil {
			return err
		}
		out.Version = sv.Version
		if err := store.SetBranchAspect(tx, ctx, store.BranchAspect{
			BranchID: u.BranchID, SourceID: src.ID, Version: sv.Version, Aspect: u.Aspect,
		}); err != nil {
			return err
		}
		var seq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM events`).Scan(&seq); err != nil {
			return err
		}
		out.EventSeq = seq
		changedKey := DepKey(u.Aspect, sv.Version)
		intIDs, err := store.InvalidateInterpretationsByAspect(tx, ctx, u.CaseID, u.BranchID,
			changedKey, seq)
		if err != nil {
			return err
		}
		exIDs, err := store.InvalidateExplorations(tx, ctx, u.CaseID, u.BranchID,
			[]string{changedKey, RuleDep()}, seq)
		if err != nil {
			return err
		}
		out.Scope = InvalidationScope{
			ChangedKeys:     []string{changedKey},
			Interpretations: intIDs,
			Explorations:    exIDs,
		}
		payload, _ := json.Marshal(map[string]any{
			"aspect": u.Aspect, "source_id": src.ID, "version": sv.Version,
			"sha256": sv.SHA256, "scope": out.Scope, "note": u.Note,
		})
		_, err = store.AppendEvent(tx, ctx, &store.Event{
			EventID: fmt.Sprintf("ev_aspect_%s_v%d", u.Aspect, sv.Version),
			CaseID:  u.CaseID, BranchID: u.BranchID, Type: EvAspectImported,
			Payload: payload, DependsOn: []string{RuleDep(), changedKey}, Rev: newRev,
		})
		return err
	})
	return out, err
}

func validAspect(a string) bool {
	for _, x := range store.Aspects {
		if x == a {
			return true
		}
	}
	return false
}

func mediaOr(m string) string {
	if m == "" {
		return "application/json"
	}
	return m
}

// CreateCaseTx creates the case row (helper used inside a transaction).
func (s *Service) CreateCaseTx(tx *sql.Tx, ctx context.Context, c *store.Case) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO cases(id,slug,title,description,rule_version,rev) VALUES(?,?,?,?,?,1)`,
		c.ID, c.Slug, c.Title, c.Description, c.RuleVersion)
	return err
}

var _ = sql.TxOptions{}
