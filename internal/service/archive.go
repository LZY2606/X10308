package service

import (
	"context"
	"encoding/json"
	"fmt"
)

// Archive is the full, order-preserving database export. Every table is copied
// with its primary keys, and events carry their global seq. Re-importing into
// an empty database after deleting the local file therefore restores stable
// identities, ordering and provenance relations exactly.
type Archive struct {
	Format         string              `json:"format"`
	FormatVersion  int                 `json:"format_version"`
	Sources        [][]any             `json:"sources"`
	ParamsVersions [][]any             `json:"params_versions"`
	Branches       [][]any             `json:"branches"`
	Events         [][]any             `json:"events"`
	Runs           [][]any             `json:"runs"`
	Candidates     [][]any             `json:"candidates"`
	Deps           [][3]string         `json:"deps"`
	Invalidations  [][]any             `json:"invalidations"`
	SchemaVersion  int                 `json:"schema_version"`
}

func (s *Service) ExportArchive(ctx context.Context) (*Archive, error) {
	a := &Archive{Format: "tflocal-petri-archive", FormatVersion: 1}
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&a.SchemaVersion); err != nil {
		return nil, err
	}
	// sources
	rows, err := s.DB.QueryContext(ctx, `SELECT source_id,doc_id,revision,kind,filename,raw,sha256,created_seq FROM sources ORDER BY created_seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, doc, rev, kind, fn, hash string
		var raw []byte
		var cs int
		if err := rows.Scan(&id, &doc, &rev, &kind, &fn, &raw, &hash, &cs); err != nil {
			rows.Close()
			return nil, err
		}
		a.Sources = append(a.Sources, []any{id, doc, rev, kind, fn, []byte(raw), hash, cs})
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT params_version,params_json FROM params_versions ORDER BY params_version`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v, j string
		if err := rows.Scan(&v, &j); err != nil {
			rows.Close()
			return nil, err
		}
		a.ParamsVersions = append(a.ParamsVersions, []any{v, j})
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT branch_id,name,source_id,params_version,COALESCE(parent_branch,''),COALESCE(created_from_event,0),note,version_counter,archived FROM branches ORDER BY created_at,branch_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v []any
		var id, name, src, pv, par, note string
		var cfe, ver, arch int
		if err := rows.Scan(&id, &name, &src, &pv, &par, &cfe, &note, &ver, &arch); err != nil {
			rows.Close()
			return nil, err
		}
		v = []any{id, name, src, pv, par, cfe, note, ver, arch}
		a.Branches = append(a.Branches, v)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT seq,event_id,branch_id,type,payload,COALESCE(source_id,''),COALESCE(params_version,'') FROM events ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var seq int
		var eid, br, typ, pl, src, pv string
		if err := rows.Scan(&seq, &eid, &br, &typ, &pl, &src, &pv); err != nil {
			rows.Close()
			return nil, err
		}
		a.Events = append(a.Events, []any{seq, eid, br, typ, pl, src, pv})
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT run_id,branch_id,scenario_id,kind,net_hash,params_version,source_id,result_json,seq FROM runs ORDER BY seq,run_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rid, br, sc, kind, nh, pv, src, rj string
		var seq int
		if err := rows.Scan(&rid, &br, &sc, &kind, &nh, &pv, &src, &rj, &seq); err != nil {
			rows.Close()
			return nil, err
		}
		a.Runs = append(a.Runs, []any{rid, br, sc, kind, nh, pv, src, rj, seq})
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT candidate_id,branch_id,scenario_id,run_id,kind,label,outcome,decision,COALESCE(decided_event_seq,0),stale,COALESCE(stale_reason,''),created_seq FROM candidates ORDER BY created_seq,candidate_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cid, br, sc, rid, kind, label, outcome, dec, sr string
		var dseq, stale, cseq int
		if err := rows.Scan(&cid, &br, &sc, &rid, &kind, &label, &outcome, &dec, &dseq, &stale, &sr, &cseq); err != nil {
			rows.Close()
			return nil, err
		}
		a.Candidates = append(a.Candidates, []any{cid, br, sc, rid, kind, label, outcome, dec, dseq, stale, sr, cseq})
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT candidate_id,dep_type,dep_ref FROM deps ORDER BY candidate_id,dep_type,dep_ref`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x [3]string
		if err := rows.Scan(&x[0], &x[1], &x[2]); err != nil {
			rows.Close()
			return nil, err
		}
		a.Deps = append(a.Deps, x)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT COALESCE(id,0),branch_id,changed_type,changed_ref,candidate_id,reason,seq FROM invalidations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, seq int
		var br, ct, cr, cid, reason string
		if err := rows.Scan(&id, &br, &ct, &cr, &cid, &reason, &seq); err != nil {
			rows.Close()
			return nil, err
		}
		a.Invalidations = append(a.Invalidations, []any{id, br, ct, cr, cid, reason, seq})
	}
	rows.Close()
	return a, nil
}

func (a *Archive) JSON() ([]byte, error) { return json.MarshalIndent(a, "", "  ") }

// IsEmpty reports whether the local database currently has any branches.
func (s *Service) IsEmpty(ctx context.Context) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM branches`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// ImportArchive restores an archive into an empty database inside one
// transaction. Any failure rolls back entirely.
func (s *Service) ImportArchive(ctx context.Context, a *Archive) error {
	if empty, err := s.IsEmpty(ctx); err != nil {
		return err
	} else if !empty {
		return ErrDBNotEmpty
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range a.Sources {
		raw, _ := json.Marshal(r[5])
		// r[5] came through []byte already; normalize through json round-trip
		var b []byte
		_ = json.Unmarshal(raw, &b)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sources(source_id,doc_id,revision,kind,filename,raw,sha256,created_seq) VALUES(?,?,?,?,?,?,?,?)`,
			r[0], r[1], r[2], r[3], r[4], b, r[6], r[7]); err != nil {
			return fmt.Errorf("source %v: %w", r[0], err)
		}
	}
	for _, r := range a.ParamsVersions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO params_versions(params_version,params_json) VALUES(?,?)`, r[0], r[1]); err != nil {
			return err
		}
	}
	for _, r := range a.Branches {
		par := toString(r[4])
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO branches(branch_id,name,source_id,params_version,parent_branch,created_from_event,note,version_counter,archived) VALUES(?,?,?,?,?,?,?,?,?)`,
			r[0], r[1], r[2], r[3], nullableString(par), toInt(r[5]), r[6], toInt(r[7]), toInt(r[8])); err != nil {
			return err
		}
	}
	for _, r := range a.Events {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events(seq,event_id,branch_id,type,payload,source_id,params_version) VALUES(?,?,?,?,?,?,?)`,
			toInt(r[0]), r[1], r[2], r[3], r[4], nullableString(toString(r[5])), nullableString(toString(r[6]))); err != nil {
			return err
		}
	}
	for _, r := range a.Runs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO runs(run_id,branch_id,scenario_id,kind,net_hash,params_version,source_id,result_json,seq) VALUES(?,?,?,?,?,?,?,?,?)`,
			r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], toInt(r[8])); err != nil {
			return err
		}
	}
	for _, r := range a.Candidates {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO candidates(candidate_id,branch_id,scenario_id,run_id,kind,label,outcome,decision,decided_event_seq,stale,stale_reason,created_seq) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], toInt(r[8]), toInt(r[9]), r[10], toInt(r[11])); err != nil {
			return err
		}
	}
	for _, x := range a.Deps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deps(candidate_id,dep_type,dep_ref) VALUES(?,?,?)`, x[0], x[1], x[2]); err != nil {
			return err
		}
	}
	for _, r := range a.Invalidations {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO invalidations(id,branch_id,changed_type,changed_ref,candidate_id,reason,seq) VALUES(?,?,?,?,?,?,?)`,
			toInt(r[0]), r[1], r[2], r[3], r[4], r[5], toInt(r[6])); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
