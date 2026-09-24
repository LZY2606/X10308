package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"tflocal/internal/domain"
)

// ReviseParams creates a child branch whose source is unchanged but whose
// exploration strategy / truncation budget / parser version is new.
// Invalidation scope is computed from dependency edges: every candidate of the
// parent branch depends on the changed params version and is reported stale
// on the new branch lineage. Old conclusions remain readable.
func (s *Service) ReviseParams(ctx context.Context, parentBranch string, params domain.Params, reason string) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var parentSrc, parentPV, parentName, docTitle string
	err = tx.QueryRowContext(ctx,
		`SELECT b.source_id,b.params_version,b.name,s.doc_id
		 FROM branches b JOIN sources s ON s.source_id=b.source_id
		 WHERE b.branch_id=?`, parentBranch).Scan(&parentSrc, &parentPV, &parentName, &docTitle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: branch %s", ErrNotFound, parentBranch)
		}
		return "", err
	}
	pv := paramsHash(params)
	pb, _ := json.Marshal(params)
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO params_versions(params_version,params_json) VALUES(?,?)`, pv, string(pb)); err != nil {
		return "", err
	}
	branchID := parentBranch + "_prm"
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO branches(branch_id,name,source_id,params_version,parent_branch,note)
		 VALUES(?,?,?,?,?,?)`,
		branchID, "参数修订 / "+reason, parentSrc, pv, parentBranch, reason); err != nil {
		return "", err
	}
	seq, err := nextSeq(ctx, tx)
	if err != nil {
		return "", err
	}
	if err := appendEvent(ctx, tx, seq, "ev_"+branchID+"_create", branchID, "branch_created", map[string]any{
		"branch_id": branchID, "parent_branch": parentBranch, "source_id": parentSrc,
		"params_version": pv, "params_change": true, "reason": reason,
	}, parentSrc, pv); err != nil {
		return "", err
	}
	seq++

	// rerun all scenarios from unchanged raw source
	doc, err := loadDocFromSource(ctx, tx, parentSrc)
	if err != nil {
		return "", err
	}
	if err := rerunAll(ctx, tx, &seq, branchID, parentSrc, pv, params, doc, nil); err != nil {
		return "", err
	}
	// dependency-driven invalidation: params changed => every parent candidate
	affected, err := invalidateDeps(ctx, tx, &seq, branchID, parentBranch, "params", pv,
		func(depType string) bool { return depType == "params" }, "exploration/truncation params changed: "+reason)
	if err != nil {
		return "", err
	}
	if err := appendEvent(ctx, tx, seq, "ev_"+branchID+"_invalidate", branchID, "invalidation_scope", map[string]any{
		"changed_type": "params", "changed_ref": pv,
		"affected_candidates": affected, "scope_rule": "candidates depending on changed params version",
	}, parentSrc, pv); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return branchID, nil
}

// ReviseSource imports a document that changes ONE direct source input
// (node identity / arc weight / initial token / capacity / inhibitor). Only
// candidates whose derived net hash changes fall inside the invalidation
// scope; candidates of unaffected scenarios are reused as-is on the child.
func (s *Service) ReviseSource(ctx context.Context, parentBranch string, raw []byte, reason string) (string, error) {
	doc, err := ParseDocument(raw)
	if err != nil {
		return "", err
	}
	interps := map[string]*domain.Net{}
	hashes := map[string]string{}
	for _, sc := range doc.Scenarios {
		n, ierr := domain.Interpret(sc)
		if ierr != nil {
			return "", fmt.Errorf("scenario %s: %w", sc.ID, ierr)
		}
		interps[sc.ID] = n
		hashes[sc.ID] = NetHash(sc, n)
	}
	rawHash := sha12(raw)
	sourceID := fmt.Sprintf("src_%s_%s_%s", doc.DocID, doc.Revision, rawHash)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var parentSrc, parentPV, parentParams string
	err = tx.QueryRowContext(ctx,
		`SELECT source_id,params_version FROM branches WHERE branch_id=?`, parentBranch,
	).Scan(&parentSrc, &parentPV)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: branch %s", ErrNotFound, parentBranch)
		}
		return "", err
	}
	// load parent net hashes to compute changed scope
	parentHashes := map[string]string{}
	prows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT scenario_id, net_hash FROM runs WHERE branch_id=? AND kind='exact'`, parentBranch)
	if err != nil {
		return "", err
	}
	for prows.Next() {
		var sc, nh string
		if err := prows.Scan(&sc, &nh); err != nil {
			prows.Close()
			return "", err
		}
		parentHashes[sc] = nh
	}
	prows.Close()
	var params domain.Params
	if err := tx.QueryRowContext(ctx, `SELECT params_json FROM params_versions WHERE params_version=?`, parentPV).Scan(&parentParams); err != nil {
		return "", err
	}
	_ = json.Unmarshal([]byte(parentParams), &params)

	// count changed scenarios for the "one direct source" claim
	changed := []string{}
	for id, h := range hashes {
		if parentHashes[id] != "" && parentHashes[id] != h {
			changed = append(changed, id)
		}
	}
	sort.Strings(changed)

	seq, err := nextSeq(ctx, tx)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sources(source_id,doc_id,revision,kind,filename,raw,sha256,created_seq) VALUES(?,?,?,?,?,?,?,?)`,
		sourceID, doc.DocID, doc.Revision, "source_revision", "revision-"+doc.Revision+".json", raw, rawHash, seq); err != nil {
		return "", err
	}
	branchID := parentBranch + "_src_" + doc.Revision
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO branches(branch_id,name,source_id,params_version,parent_branch,note)
		 VALUES(?,?,?,?,?,?)`,
		branchID, "局部源数据修订 / "+reason, sourceID, parentPV, parentBranch, reason); err != nil {
		return "", err
	}
	if err := appendEvent(ctx, tx, seq, "ev_"+branchID+"_create", branchID, "branch_created", map[string]any{
		"branch_id": branchID, "parent_branch": parentBranch, "source_id": sourceID,
		"source_change": true, "reason": reason, "changed_scenarios": changed,
		"scope_rule": "only scenarios with changed derived net hash",
	}, sourceID, parentPV); err != nil {
		return "", err
	}
	seq++
	if err := rerunAll(ctx, tx, &seq, branchID, sourceID, parentPV, params, doc, nil); err != nil {
		return "", err
	}
	affected, err := invalidateDeps(ctx, tx, &seq, branchID, parentBranch, "source", sourceID,
		func(depType string) bool { return depType == "source" || depType == "net" },
		"one direct source input changed: "+reason+" -> scenarios "+fmt.Sprint(changed))
	if err != nil {
		return "", err
	}
	if err := appendEvent(ctx, tx, seq, "ev_"+branchID+"_scope", branchID, "invalidation_scope", map[string]any{
		"changed_type": "source", "changed_ref": sourceID, "changed_scenarios": changed,
		"affected_candidates": affected,
		"scope_rule": "candidates with changed net hash only; unrelated scenarios reused",
	}, sourceID, parentPV); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return branchID, nil
}

func loadDocFromSource(ctx context.Context, tx *sql.Tx, sourceID string) (*domain.Document, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT raw FROM sources WHERE source_id=?`, sourceID).Scan(&raw); err != nil {
		return nil, err
	}
	return ParseDocument(raw)
}

// rerunAll reinterprets and records every scenario. changedScenarios may be
// nil (params change => all scenarios rerun). Runs/candidates get branch
// scoped deterministic ids, so results never overwrite the parent branch.
func rerunAll(ctx context.Context, tx *sql.Tx, seq *int, branchID, sourceID, pv string, params domain.Params, doc *domain.Document, _ map[string]bool) error {
	scenarios := append([]*domain.Scenario(nil), doc.Scenarios...)
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].ID < scenarios[j].ID })
	for _, sc := range scenarios {
		n, err := domain.Interpret(sc)
		if err != nil {
			return err
		}
		if _, err := recordScenario(ctx, tx, seq, branchID, sourceID, pv, params, sc, n); err != nil {
			return err
		}
	}
	return nil
}

// invalidateDeps marks parent-branch candidates that fall inside the change
// scope. It reads dependency edges rather than scanning every conclusion.
func invalidateDeps(ctx context.Context, tx *sql.Tx, seq *int, newBranch, parentBranch, changedType, changedRef string,
	scope func(depType string) bool, reason string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT c.candidate_id FROM candidates c JOIN deps d ON d.candidate_id=c.candidate_id
		 WHERE c.branch_id=? AND d.dep_type IN ('source','params','net','rule')`, parentBranch)
	if err != nil {
		return nil, err
	}
	type dep struct {
		cand string
		refs map[string][]string
	}
	var cands []dep
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return nil, err
		}
		d2, err := tx.QueryContext(ctx, `SELECT dep_type,dep_ref FROM deps WHERE candidate_id=?`, cid)
		if err != nil {
			rows.Close()
			return nil, err
		}
		dd := dep{cand: cid, refs: map[string][]string{}}
		for d2.Next() {
			var t, r string
			if err := d2.Scan(&t, &r); err != nil {
				d2.Close()
				rows.Close()
				return nil, err
			}
			dd.refs[t] = append(dd.refs[t], r)
		}
		d2.Close()
		cands = append(cands, dd)
	}
	rows.Close()

	// For source changes only invalidate when the candidate's scenario net
	// actually changed; for params changes invalidate every candidate.
	var affected []string
	for _, c := range cands {
		hit := false
		for t := range c.refs {
			if scope(t) {
				if changedType == "source" && t == "params" {
					continue
				}
				hit = true
			}
		}
		// source + net scoping: the candidate's scenario must be one that changed
		if changedType == "source" {
			scRow := tx.QueryRowContext(ctx, `SELECT scenario_id FROM candidates WHERE candidate_id=?`, c.cand)
			var scenarioID string
			if err := scRow.Scan(&scenarioID); err != nil {
				return nil, err
			}
			// determine if this scenario changed by comparing new branch net hash
			var newNH, oldNH string
			e1 := tx.QueryRowContext(ctx, `SELECT net_hash FROM runs WHERE branch_id=? AND scenario_id=? AND kind='exact' LIMIT 1`, newBranch, scenarioID).Scan(&newNH)
			e2 := tx.QueryRowContext(ctx, `SELECT net_hash FROM runs WHERE branch_id=? AND scenario_id=? AND kind='exact' LIMIT 1`, parentBranch, scenarioID).Scan(&oldNH)
			if e1 != nil || e2 != nil || newNH == oldNH {
				hit = false
			}
		}
		if !hit {
			continue
		}
		affected = append(affected, c.cand)
	}
	sort.Strings(affected)
	for _, cid := range affected {
		if _, err := tx.ExecContext(ctx,
			`UPDATE candidates SET stale=1, stale_reason=? WHERE candidate_id=?`, reason, cid); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO invalidations(branch_id,changed_type,changed_ref,candidate_id,reason,seq) VALUES(?,?,?,?,?,?)`,
			newBranch, changedType, changedRef, cid, reason, *seq); err != nil {
			return nil, err
		}
	}
	return affected, nil
}
