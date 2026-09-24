package store

import (
	"context"
	"encoding/json"
)

// Bundle is the portable, fully offline export of the workspace. Identities,
// ordering and provenance links are literal rows, so deleting the local
// database and re-importing restores the exact same graph.
type Bundle struct {
	Format          string             `json:"format"`
	FormatVersion   int                `json:"format_version"`
	ExportedAt      string             `json:"exported_at"`
	Events          []BundleEvent      `json:"events"`
	Imports         []BundleImport     `json:"imports"`
	Materials       []BundleMaterial   `json:"materials"`
	Diagnostics     []BundleDiag       `json:"diagnostics"`
	Branches        []BundleBranch     `json:"branches"`
	BranchMaterials []BundleSlot       `json:"branch_materials"`
	Models          []BundleModel      `json:"models"`
	Runs            []BundleRun        `json:"runs"`
	Counterexamples []BundleCEx        `json:"counterexamples"`
	Decisions       []BundleDecision   `json:"decisions"`
	Dependencies    []BundleDependency `json:"dependencies"`
	Invalidations   []BundleInvalid    `json:"invalidations"`
}

type BundleEvent struct {
	Seq        int64  `json:"seq"`
	At         string `json:"at"`
	Type       string `json:"type"`
	Actor      string `json:"actor"`
	BranchID   string `json:"branch_id"`
	RefType    string `json:"ref_type"`
	RefID      string `json:"ref_id"`
	Payload    string `json:"payload_json"`
	BaseRev    *int   `json:"base_rev,omitempty"`
}
type BundleImport struct {
	ID, At, Status, PackVersion, Note string
}
type BundleMaterial struct {
	ID, Version, Kind, Title, Raw, SHA, Batch, Supersedes string
}
type BundleDiag struct {
	MaterialID string `json:"material_id"`
	Version    int    `json:"version"`
	Path       string `json:"path"`
	Severity   string `json:"severity"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RawValue   string `json:"raw_value"`
}
type BundleBranch struct {
	ID string `json:"id"`
	Seq int64 `json:"seq"`
	Title, ParentID string `json:"title"`
	CreatedEvent int64 `json:"created_event"`
	Open int `json:"open"`
}
type BundleSlot struct{ BranchID, MaterialID string; Version, Slot int }
type BundleModel struct {
	ID string `json:"id"`
	Seq int64 `json:"seq"`
	BranchID, MaterialID string `json:"branch_id"`
	MaterialVersion int `json:"material_version"`
	RulesVersion, Derived string
	CreatedEvent int64 `json:"created_event"`
}
type BundleRun struct {
	ID string `json:"id"`
	Seq int64 `json:"seq"`
	BranchID, ModelID, RulesVersion, Strategy string
	FireBudget, StateBudget int64
	Target, Result, Cover string
	CreatedEvent int64 `json:"created_event"`
}
type BundleCEx struct {
	ID string `json:"id"`
	Seq int64 `json:"seq"`
	BranchID, RunID, Scenario, Title, Detail, Verdict string
	Rev int
	DecidedEvent int64
	DecidedBy string
}
type BundleDecision struct {
	ID int64
	CounterexampleID, At, Actor, Action string
	BaseRev, NewRev int
	Status, Note, Payload string
}
type BundleDependency struct {
	ConsumerType, ConsumerID, SupplierType, SupplierID string
	SupplierVersion int
	Rel string
}
type BundleInvalid struct {
	Seq int64
	At, Scope, ReasonCode, CauseType, CauseID string
	CauseVersion int
}

// Export serializes the whole database as one bundle.
func (s *Store) Export(ctx context.Context) (*Bundle, error) {
	b := &Bundle{Format: "tokenflownet-archive", FormatVersion: 1, ExportedAt: nowISO()}
	erows, err := s.db.QueryContext(ctx,
		`SELECT seq, at_event, event_type, actor, COALESCE(branch_id,''),
		        COALESCE(ref_type,''), COALESCE(ref_id,''), payload_json, base_rev
		 FROM event_log ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var e BundleEvent
		var base sqlNull
		if err := erows.Scan(&e.Seq, &e.At, &e.Type, &e.Actor, &e.BranchID,
			&e.RefType, &e.RefID, &e.Payload, &base); err != nil {
			erows.Close()
			return nil, err
		}
		if base.Valid {
			v := int(base.Int64)
			e.BaseRev = &v
		}
		b.Events = append(b.Events, e)
	}
	erows.Close()

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at_event, status, COALESCE(pack_version,''), COALESCE(note,'')
		 FROM import_batch ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleImport
		if err := rows.Scan(&x.ID, &x.At, &x.Status, &x.PackVersion, &x.Note); err != nil {
			rows.Close()
			return nil, err
		}
		b.Imports = append(b.Imports, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT id, version, kind, title, raw_json, sha256, import_batch, COALESCE(supersedes,'')
		 FROM material ORDER BY id, version`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleMaterial
		if err := rows.Scan(&x.ID, &x.Version, &x.Kind, &x.Title, &x.Raw,
			&x.SHA, &x.Batch, &x.Supersedes); err != nil {
			rows.Close()
			return nil, err
		}
		b.Materials = append(b.Materials, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT material_id, version, path, severity, code, message, COALESCE(raw_value,'')
		 FROM material_diagnostic ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleDiag
		if err := rows.Scan(&x.MaterialID, &x.Version, &x.Path, &x.Severity,
			&x.Code, &x.Message, &x.RawValue); err != nil {
			rows.Close()
			return nil, err
		}
		b.Diagnostics = append(b.Diagnostics, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT id, seq, title, COALESCE(parent_id,''), created_event, open FROM branch ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleBranch
		if err := rows.Scan(&x.ID, &x.Seq, &x.Title, &x.ParentID, &x.CreatedEvent, &x.Open); err != nil {
			rows.Close()
			return nil, err
		}
		b.Branches = append(b.Branches, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT branch_id, material_id, version, slot FROM branch_material
		 ORDER BY branch_id, slot`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleSlot
		if err := rows.Scan(&x.BranchID, &x.MaterialID, &x.Version, &x.Slot); err != nil {
			rows.Close()
			return nil, err
		}
		b.BranchMaterials = append(b.BranchMaterials, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT id, seq, branch_id, material_id, material_version, rules_version,
		        derived_json, created_event FROM model ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleModel
		if err := rows.Scan(&x.ID, &x.Seq, &x.BranchID, &x.MaterialID,
			&x.MaterialVersion, &x.RulesVersion, &x.Derived, &x.CreatedEvent); err != nil {
			rows.Close()
			return nil, err
		}
		b.Models = append(b.Models, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT id, seq, branch_id, model_id, rules_version, strategy, fire_budget,
		        state_budget, COALESCE(target_json,''), result_json, COALESCE(cover_json,''),
		        created_event FROM run ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleRun
		if err := rows.Scan(&x.ID, &x.Seq, &x.BranchID, &x.ModelID, &x.RulesVersion,
			&x.Strategy, &x.FireBudget, &x.StateBudget, &x.Target, &x.Result,
			&x.Cover, &x.CreatedEvent); err != nil {
			rows.Close()
			return nil, err
		}
		b.Runs = append(b.Runs, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev,
		        COALESCE(decided_event,0), COALESCE(decided_by,'')
		 FROM counterexample ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleCEx
		if err := rows.Scan(&x.ID, &x.Seq, &x.BranchID, &x.RunID, &x.Scenario,
			&x.Title, &x.Detail, &x.Verdict, &x.Rev, &x.DecidedEvent, &x.DecidedBy); err != nil {
			rows.Close()
			return nil, err
		}
		b.Counterexamples = append(b.Counterexamples, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT id, counterexample_id, at_event, actor, action, base_rev,
		        COALESCE(new_rev,0), status, COALESCE(note,''), payload_json
		 FROM decision ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleDecision
		if err := rows.Scan(&x.ID, &x.CounterexampleID, &x.At, &x.Actor, &x.Action,
			&x.BaseRev, &x.NewRev, &x.Status, &x.Note, &x.Payload); err != nil {
			rows.Close()
			return nil, err
		}
		b.Decisions = append(b.Decisions, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT consumer_type, consumer_id, supplier_type, supplier_id, supplier_version, rel
		 FROM dependency ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleDependency
		if err := rows.Scan(&x.ConsumerType, &x.ConsumerID, &x.SupplierType,
			&x.SupplierID, &x.SupplierVersion, &x.Rel); err != nil {
			rows.Close()
			return nil, err
		}
		b.Dependencies = append(b.Dependencies, x)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx,
		`SELECT seq, at_event, scope_json, reason_code, cause_type, cause_id, cause_version
		 FROM invalidation ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x BundleInvalid
		if err := rows.Scan(&x.Seq, &x.At, &x.Scope, &x.ReasonCode,
			&x.CauseType, &x.CauseID, &x.CauseVersion); err != nil {
			rows.Close()
			return nil, err
		}
		b.Invalidations = append(b.Invalidations, x)
	}
	rows.Close()
	return b, nil
}

// ExportJSON is a convenience marshaler.
func (s *Store) ExportJSON(ctx context.Context) ([]byte, error) {
	b, err := s.Export(ctx)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(b, "", "  ")
}
