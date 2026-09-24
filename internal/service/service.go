package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"tflocal/internal/domain"
	"tflocal/internal/engine"
)

var ErrConflict = errors.New("version conflict")
var ErrNotFound = errors.New("not found")
var ErrDBNotEmpty = errors.New("database not empty; archive import requires an empty database")

type Service struct {
	DB *sql.DB
}

func New(db *sql.DB) *Service { return &Service{DB: db} }

// ---- low level ----------------------------------------------------------

func nextSeq(ctx context.Context, tx *sql.Tx) (int, error) {
	var v sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64) + 1, nil
}

func appendEvent(ctx context.Context, tx *sql.Tx, seq int, eventID, branchID, typ string, payload any, sourceID, paramsVersion string) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events(seq,event_id,branch_id,type,payload,source_id,params_version) VALUES(?,?,?,?,?,?,?)`,
		seq, eventID, branchID, typ, string(b), nullable(sourceID), nullable(paramsVersion))
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ParseDocument parses raw bytes without touching persistence.
func ParseDocument(raw []byte) (*domain.Document, error) {
	var doc domain.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid document JSON: %w", err)
	}
	if doc.DocID == "" {
		return nil, errors.New("document.doc_id is required")
	}
	if doc.Revision == "" {
		return nil, errors.New("document.revision is required")
	}
	if len(doc.Scenarios) == 0 {
		return nil, errors.New("document must contain at least one scenario")
	}
	return &doc, nil
}

// ImportSummary reports what an import produced.
type ImportSummary struct {
	BranchID  string
	SourceID  string
	ParamsVer string
	Scenarios []ScenarioSummary
	Reused    bool
}

type ScenarioSummary struct {
	ScenarioID  string
	NetHash     string
	ExactRunID  string
	CoverRunID  string
	Outcome     string
	Unbounded   bool
	Truncated   bool
	Deadlocks   int
	Diagnostics []*domain.Diagnosis
}

// ImportDocument validates and imports a full source document inside one
// transaction. Any validation failure rolls back the whole import: no half
// records are left behind.
func (s *Service) ImportDocument(ctx context.Context, raw []byte, filename, kind string) (*ImportSummary, error) {
	doc, err := ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	// Interpret every scenario BEFORE opening write work, so a failure in the
	// last scenario cannot leave partial rows.
	interps := map[string]*domain.Net{}
	for _, sc := range doc.Scenarios {
		n, ierr := domain.Interpret(sc)
		if ierr != nil {
			return nil, fmt.Errorf("scenario %s: %w", sc.ID, ierr)
		}
		interps[sc.ID] = n
	}
	rawHash := sha12(raw)
	sourceID := fmt.Sprintf("src_%s_%s_%s", doc.DocID, doc.Revision, rawHash)
	branchID := "br_" + doc.DocID + "_" + doc.Revision
	pv := paramsHash(doc.Params)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Idempotent re-import: identical bytes under the same identity.
	var existingSrc string
	err = tx.QueryRowContext(ctx, `SELECT raw FROM sources WHERE source_id=?`, sourceID).Scan(&existingSrc)
	if err == nil {
		var bExists int
		_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM branches WHERE branch_id=?`, branchID).Scan(&bExists)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if bExists > 0 {
			return s.summarize(ctx, branchID, sourceID, pv, true)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	} else {
		// same doc id+revision but a different source_id hash would mean
		// different bytes under same identity: forbid silent overwrite.
		var clash string
		if e2 := tx.QueryRowContext(ctx,
			`SELECT source_id FROM sources WHERE doc_id=? AND revision=?`, doc.DocID, doc.Revision).Scan(&clash); e2 == nil {
			return nil, fmt.Errorf("immutable source %s/%s already exists with different bytes", doc.DocID, doc.Revision)
		}
	}

	seq, err := nextSeq(ctx, tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sources(source_id,doc_id,revision,kind,filename,raw,sha256,created_seq) VALUES(?,?,?,?,?,?,?,?)`,
		sourceID, doc.DocID, doc.Revision, kind, filename, raw, rawHash, seq); err != nil {
		return nil, err
	}
	pb, _ := json.Marshal(doc.Params)
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO params_versions(params_version,params_json) VALUES(?,?)`, pv, string(pb)); err != nil {
		return nil, err
	}
	if err := appendEvent(ctx, tx, seq, "ev_"+sourceID, branchID, "source_imported", map[string]any{
		"source_id": sourceID, "doc_id": doc.DocID, "revision": doc.Revision,
		"filename": filename, "sha256": rawHash, "kind": kind,
		"components": []string{"model", "node_identity", "arc_weight", "initial_token", "capacity", "exploration_strategy", "truncation_budget"},
	}, sourceID, pv); err != nil {
		return nil, err
	}
	seq++
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO branches(branch_id,name,source_id,params_version,created_from_event,version_counter,note) VALUES(?,?,?,?,?,?,?)`,
		branchID, "基线 / "+doc.Title, sourceID, pv, seq-1, 0, doc.Title); err != nil {
		return nil, err
	}
	if err := appendEvent(ctx, tx, seq, "ev_"+branchID+"_create", branchID, "branch_created", map[string]any{
		"branch_id": branchID, "source_id": sourceID, "params_version": pv,
		"rule_version": doc.Params.ParserVersion,
	}, sourceID, pv); err != nil {
		return nil, err
	}
	seq++

	summary := &ImportSummary{BranchID: branchID, SourceID: sourceID, ParamsVer: pv}
	scenarios := append([]*domain.Scenario(nil), doc.Scenarios...)
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].ID < scenarios[j].ID })
	for _, sc := range scenarios {
		n := interps[sc.ID]
		ss, err := recordScenario(ctx, tx, &seq, branchID, sourceID, pv, doc.Params, sc, n)
		if err != nil {
			return nil, err
		}
		summary.Scenarios = append(summary.Scenarios, ss)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	summary.Reused = false
	return summary, nil
}

// recordScenario interprets one scenario, runs both explorations (exact +
// coverability), stores runs, proposes candidates and records dependency edges.
func recordScenario(ctx context.Context, tx *sql.Tx, seq *int, branchID, sourceID, pv string, params domain.Params, sc *domain.Scenario, n *domain.Net) (ScenarioSummary, error) {
	nh := NetHash(sc, n)
	exact := engine.ExploreExact(n, params, sc.BudgetOverride)
	cov := engine.Coverability(n, params, sc.BudgetOverride)
	ss := ScenarioSummary{ScenarioID: sc.ID, NetHash: nh, Outcome: string(exact.Outcome),
		Unbounded: cov.Unbounded, Truncated: exact.Truncated || cov.Truncated,
		Deadlocks: len(exact.DeadlockIDs), Diagnostics: n.ParseDiagnostics}

	exactRunID := "run_exact_" + nh + "_" + pv
	coverRunID := "run_cover_" + nh + "_" + pv
	eb, _ := json.Marshal(exact)
	cb, _ := json.Marshal(cov)
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO runs(run_id,branch_id,scenario_id,kind,net_hash,params_version,source_id,result_json,seq)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		exactRunID, branchID, sc.ID, "exact", nh, pv, sourceID, string(eb), *seq); err != nil {
		return ss, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO runs(run_id,branch_id,scenario_id,kind,net_hash,params_version,source_id,result_json,seq)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		coverRunID, branchID, sc.ID, "cover", nh, pv, sourceID, string(cb), *seq); err != nil {
		return ss, err
	}
	ss.ExactRunID = exactRunID
	ss.CoverRunID = coverRunID

	if err := appendEvent(ctx, tx, *seq, "ev_"+exactRunID, branchID, "exploration_recorded", map[string]any{
		"scenario_id": sc.ID, "run_id": exactRunID, "kind": "exact",
		"outcome": string(exact.Outcome), "steps_used": exact.Fired, "budget": exact.Budget,
		"truncated": exact.Truncated, "retained_frontier": exact.Frontier,
		"net_hash": nh,
		"rules": map[string]any{
			"enablement":      "enabled_iff_input_ok_and_inhibitor_ok_and_capacity_ok",
			"canonicalization": "markings_with_different_tokens_are_never_merged",
			"budget_exhausted": "verdict_unknown_and_frontier_retained",
		},
	}, sourceID, pv); err != nil {
		return ss, err
	}
	*seq++
	if err := appendEvent(ctx, tx, *seq, "ev_"+coverRunID, branchID, "exploration_recorded", map[string]any{
		"scenario_id": sc.ID, "run_id": coverRunID, "kind": "cover_karp_miller",
		"unbounded": cov.Unbounded, "cover_pairs": len(cov.CoverPairs),
		"truncated": cov.Truncated, "retained_frontier": cov.Frontier, "net_hash": nh,
	}, sourceID, pv); err != nil {
		return ss, err
	}
	*seq++

	// Reachability candidate (possibly ambiguous: all legal candidates kept).
	reachID := "cand_" + branchID + "_" + sc.ID + "_reach"
	cands := []struct {
		id, kind, label, outcome, runID string
		extra                           map[string]any
	}{
		{reachID, "reachability", "目标 marking 可达性", string(exact.Outcome), exactRunID, map[string]any{
			"goal_state_id": exact.GoalStateID, "goal_path": exact.GoalPath}},
	}
	for i, d := range exact.DeadlockIDs {
		cands = append(cands, struct {
			id, kind, label, outcome, runID string
			extra                           map[string]any
		}{
			fmt.Sprintf("cand_%s_%s_deadlock_%d", branchID, sc.ID, i), "deadlock",
			"死锁候选 " + d, "deadlock", exactRunID, map[string]any{"state_id": d},
		})
	}
	if exact.Truncated {
		cands = append(cands, struct {
			id, kind, label, outcome, runID string
			extra                           map[string]any
		}{
			"cand_" + branchID + "_" + sc.ID + "_trunc", "truncation",
			"预算截断 frontier", "unknown", exactRunID, map[string]any{"frontier": exact.Frontier},
		})
	}
	for i, cn := range cov.GoalCoveredBy {
		cands = append(cands, struct {
			id, kind, label, outcome, runID string
			extra                           map[string]any
		}{
			fmt.Sprintf("cand_%s_%s_cover_%d", branchID, sc.ID, i), "coverability",
			"覆盖关系（目标被 ω marking 覆盖）", "covered", coverRunID, map[string]any{"state_id": cn},
		})
	}
	for _, c := range cands {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO candidates(candidate_id,branch_id,scenario_id,run_id,kind,label,outcome,created_seq)
			 VALUES(?,?,?,?,?,?,?,?)`,
			c.id, branchID, sc.ID, c.runID, c.kind, c.label, c.outcome, *seq); err != nil {
			return ss, err
		}
		deps := [][2]string{
			{"source", sourceID}, {"params", pv}, {"net", nh}, {"scenario", sc.ID}, {"rule", params.ParserVersion},
		}
		for _, d := range deps {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO deps(candidate_id,dep_type,dep_ref) VALUES(?,?,?)`, c.id, d[0], d[1]); err != nil {
				return ss, err
			}
		}
		payload := map[string]any{
			"candidate_id": c.id, "scenario_id": sc.ID, "kind": c.kind, "label": c.label,
			"outcome": c.outcome, "run_id": c.runID, "legal_candidates_preserved": true,
		}
		for k, v := range c.extra {
			payload[k] = v
		}
		if err := appendEvent(ctx, tx, *seq, "ev_"+c.id, branchID, "candidate_proposed", payload, sourceID, pv); err != nil {
			return ss, err
		}
		*seq++
	}
	return ss, nil
}

func (s *Service) summarize(ctx context.Context, branchID, sourceID, pv string, reused bool) (*ImportSummary, error) {
	sum := &ImportSummary{BranchID: branchID, SourceID: sourceID, ParamsVer: pv, Reused: reused}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT scenario_id, net_hash FROM runs WHERE branch_id=? ORDER BY scenario_id`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sc, nh string
		if err := rows.Scan(&sc, &nh); err != nil {
			return nil, err
		}
		sum.Scenarios = append(sum.Scenarios, ScenarioSummary{ScenarioID: sc, NetHash: nh})
	}
	return sum, nil
}
