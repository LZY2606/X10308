package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"tflocal/internal/domain"
	"tflocal/internal/engine"
)

type BranchInfo struct {
	BranchID      string `json:"branch_id"`
	Name          string `json:"name"`
	SourceID      string `json:"source_id"`
	ParamsVersion string `json:"params_version"`
	ParentBranch  string `json:"parent_branch,omitempty"`
	Note          string `json:"note"`
	Version       int    `json:"version"`
	EventCount    int    `json:"event_count"`
}

func (s *Service) ListBranches(ctx context.Context) ([]BranchInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT b.branch_id,b.name,b.source_id,b.params_version,COALESCE(b.parent_branch,''),b.note,b.version_counter,
		       (SELECT COUNT(*) FROM events e WHERE e.branch_id=b.branch_id)
		FROM branches b WHERE b.archived=0 ORDER BY b.created_at,b.branch_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BranchInfo
	for rows.Next() {
		var b BranchInfo
		if err := rows.Scan(&b.BranchID, &b.Name, &b.SourceID, &b.ParamsVersion, &b.ParentBranch, &b.Note, &b.Version, &b.EventCount); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

type CandidateView struct {
	CandidateID string `json:"candidate_id"`
	ScenarioID  string `json:"candidate_id_s"`
	Scenario    string `json:"scenario_id"`
	RunID       string `json:"run_id"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Outcome     string `json:"outcome"`
	Decision    string `json:"decision"`
	Stale       bool   `json:"stale"`
	StaleReason string `json:"stale_reason"`
	CreatedSeq  int    `json:"created_seq"`
	Deps        map[string][]string `json:"deps"`
}

func (s *Service) ListCandidates(ctx context.Context, branchID string) ([]CandidateView, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT candidate_id,scenario_id,run_id,kind,label,outcome,decision,stale,COALESCE(stale_reason,''),created_seq
		FROM candidates WHERE branch_id=? ORDER BY created_seq,candidate_id`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CandidateView
	for rows.Next() {
		var c CandidateView
		var stale int
		if err := rows.Scan(&c.CandidateID, &c.Scenario, &c.RunID, &c.Kind, &c.Label, &c.Outcome, &c.Decision, &stale, &c.StaleReason, &c.CreatedSeq); err != nil {
			return nil, err
		}
		c.ScenarioID = c.Scenario
		c.Stale = stale == 1
		c.Deps = map[string][]string{}
		drows, err := s.DB.QueryContext(ctx, `SELECT dep_type,dep_ref FROM deps WHERE candidate_id=? ORDER BY dep_type,dep_ref`, c.CandidateID)
		if err != nil {
			return nil, err
		}
		for drows.Next() {
			var t, r string
			if err := drows.Scan(&t, &r); err != nil {
				drows.Close()
				return nil, err
			}
			c.Deps[t] = append(c.Deps[t], r)
		}
		drows.Close()
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) BranchDoc(ctx context.Context, branchID string) (*domain.Document, []byte, string, error) {
	var sourceID string
	if err := s.DB.QueryRowContext(ctx, `SELECT source_id FROM branches WHERE branch_id=?`, branchID).Scan(&sourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, "", fmt.Errorf("%w: branch %s", ErrNotFound, branchID)
		}
		return nil, nil, "", err
	}
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT raw FROM sources WHERE source_id=?`, sourceID).Scan(&raw); err != nil {
		return nil, nil, "", err
	}
	doc, err := ParseDocument(raw)
	if err != nil {
		return nil, nil, "", err
	}
	return doc, raw, sourceID, nil
}

func (s *Service) Params(ctx context.Context, branchID string) (domain.Params, string, error) {
	var pv, pjson string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT b.params_version,p.params_json FROM branches b JOIN params_versions p ON p.params_version=b.params_version WHERE b.branch_id=?`,
		branchID).Scan(&pv, &pjson); err != nil {
		return domain.Params{}, "", err
	}
	var p domain.Params
	_ = json.Unmarshal([]byte(pjson), &p)
	return p, pv, nil
}

// RunResult loads the stored exact exploration result of one scenario.
func (s *Service) RunResult(ctx context.Context, branchID, scenarioID, kind string) (*engine.Result, string, error) {
	var rj string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT result_json FROM runs WHERE branch_id=? AND scenario_id=? AND kind=? ORDER BY seq LIMIT 1`,
		branchID, scenarioID, kind).Scan(&rj); err != nil {
		return nil, "", err
	}
	var r engine.Result
	if err := json.Unmarshal([]byte(rj), &r); err != nil {
		return nil, "", err
	}
	return &r, rj, nil
}

func (s *Service) CoverResult(ctx context.Context, branchID, scenarioID string) (*engine.CovResult, error) {
	var rj string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT result_json FROM runs WHERE branch_id=? AND scenario_id=? AND kind='cover' ORDER BY seq LIMIT 1`,
		branchID, scenarioID).Scan(&rj); err != nil {
		return nil, err
	}
	var r engine.CovResult
	if err := json.Unmarshal([]byte(rj), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) Timeline(ctx context.Context, branchID string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT seq,event_id,type,payload,COALESCE(source_id,''),COALESCE(params_version,'')
		 FROM events WHERE branch_id=? ORDER BY seq`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var seq int
		var eid, typ, payload, src, pv string
		if err := rows.Scan(&seq, &eid, &typ, &payload, &src, &pv); err != nil {
			return nil, err
		}
		var p any
		_ = json.Unmarshal([]byte(payload), &p)
		out = append(out, map[string]any{"seq": seq, "event_id": eid, "type": typ, "payload": p,
			"source_id": src, "params_version": pv})
	}
	return out, nil
}

type InvalidationView struct {
	Seq         int    `json:"seq"`
	BranchID    string `json:"branch_id"`
	ChangedType string `json:"changed_type"`
	ChangedRef  string `json:"changed_ref"`
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

func (s *Service) Invalidations(ctx context.Context, branchID string) ([]InvalidationView, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT seq,branch_id,changed_type,changed_ref,candidate_id,reason FROM invalidations WHERE branch_id=? ORDER BY id`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvalidationView
	for rows.Next() {
		var v InvalidationView
		if err := rows.Scan(&v.Seq, &v.BranchID, &v.ChangedType, &v.ChangedRef, &v.CandidateID, &v.Reason); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) MaxSeq(ctx context.Context) (int, error) {
	var v sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}
