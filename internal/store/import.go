package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrDatabaseNotEmpty is returned when a bundle is imported into a database
// that already contains events. Re-import means replace, which callers do by
// deleting the local database file first.
var ErrDatabaseNotEmpty = errors.New("database is not empty; delete it before re-importing")

type sqlNull = sql.NullInt64

// ImportBundle replaces the database content with the bundle in one
// transaction. Any failure rolls back the whole import (no half records).
// Only empty databases are accepted.
func (s *Store) ImportBundle(ctx context.Context, b *Bundle) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_log`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDatabaseNotEmpty
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, x := range b.Imports {
			if _, err := tx.ExecContext(ctx, `INSERT INTO import_batch
(id, at_event, status, pack_version, note) VALUES (?,?,?,?,?)`,
				x.ID, x.At, x.Status, nullStr(x.PackVersion), nullStr(x.Note)); err != nil {
				return err
			}
		}
		for _, x := range b.Materials {
			if _, err := tx.ExecContext(ctx, `INSERT INTO material
(id, version, kind, title, raw_json, sha256, import_batch, supersedes)
VALUES (?,?,?,?,?,?,?,?)`,
				x.ID, x.Version, x.Kind, x.Title, x.Raw, x.SHA, x.Batch, nullStr(x.Supersedes)); err != nil {
				return err
			}
		}
		for _, x := range b.Diagnostics {
			if _, err := tx.ExecContext(ctx, `INSERT INTO material_diagnostic
(material_id, version, path, severity, code, message, raw_value)
VALUES (?,?,?,?,?,?,?)`,
				x.MaterialID, x.Version, x.Path, x.Severity, x.Code, x.Message, nullStr(x.RawValue)); err != nil {
				return err
			}
		}
		for _, x := range b.Events {
			if _, err := tx.ExecContext(ctx, `INSERT INTO event_log
(seq, at_event, event_type, actor, branch_id, ref_type, ref_id, payload_json, base_rev)
VALUES (?,?,?,?,?,?,?,?,?)`,
				x.Seq, x.At, x.Type, x.Actor, nullStr(x.BranchID), nullStr(x.RefType),
				nullStr(x.RefID), x.Payload, x.BaseRev); err != nil {
				return err
			}
		}
		for _, x := range b.Branches {
			if _, err := tx.ExecContext(ctx, `INSERT INTO branch
(id, seq, title, parent_id, created_event, open) VALUES (?,?,?,?,?,?)`,
				x.ID, x.Seq, x.Title, nullStr(x.ParentID), x.CreatedEvent, x.Open); err != nil {
				return err
			}
		}
		for _, x := range b.BranchMaterials {
			if _, err := tx.ExecContext(ctx, `INSERT INTO branch_material
(branch_id, material_id, version, slot) VALUES (?,?,?,?)`,
				x.BranchID, x.MaterialID, x.Version, x.Slot); err != nil {
				return err
			}
		}
		for _, x := range b.Models {
			if _, err := tx.ExecContext(ctx, `INSERT INTO model
(id, seq, branch_id, material_id, material_version, rules_version, derived_json, created_event)
VALUES (?,?,?,?,?,?,?,?)`,
				x.ID, x.Seq, x.BranchID, x.MaterialID, x.MaterialVersion,
				x.RulesVersion, x.Derived, x.CreatedEvent); err != nil {
				return err
			}
		}
		for _, x := range b.Runs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO run
(id, seq, branch_id, model_id, rules_version, strategy, fire_budget, state_budget,
 target_json, result_json, cover_json, created_event)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				x.ID, x.Seq, x.BranchID, x.ModelID, x.RulesVersion, x.Strategy,
				x.FireBudget, x.StateBudget, nullStr(x.Target), x.Result,
				nullStr(x.Cover), x.CreatedEvent); err != nil {
				return err
			}
		}
		for _, x := range b.Counterexamples {
			decidedEvent := x.DecidedEvent
			var decArg any
			if decidedEvent == 0 {
				decArg = nil
			} else {
				decArg = decidedEvent
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO counterexample
(id, seq, branch_id, run_id, scenario, title, detail_json, verdict, rev, decided_event, decided_by)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				x.ID, x.Seq, x.BranchID, x.RunID, x.Scenario, x.Title, x.Detail,
				x.Verdict, x.Rev, decArg, nullStr(x.DecidedBy)); err != nil {
				return err
			}
		}
		for _, x := range b.Decisions {
			var newRev any
			if x.NewRev == 0 {
				newRev = nil
			} else {
				newRev = x.NewRev
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO decision
(id, counterexample_id, at_event, actor, action, base_rev, new_rev, status, note, payload_json)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
				x.ID, x.CounterexampleID, x.At, x.Actor, x.Action, x.BaseRev,
				newRev, x.Status, nullStr(x.Note), x.Payload); err != nil {
				return err
			}
		}
		for _, x := range b.Dependencies {
			if _, err := tx.ExecContext(ctx, `INSERT INTO dependency
(consumer_type, consumer_id, supplier_type, supplier_id, supplier_version, rel)
VALUES (?,?,?,?,?,?)`,
				x.ConsumerType, x.ConsumerID, x.SupplierType, x.SupplierID,
				x.SupplierVersion, x.Rel); err != nil {
				return err
			}
		}
		for _, x := range b.Invalidations {
			if _, err := tx.ExecContext(ctx, `INSERT INTO invalidation
(seq, at_event, scope_json, reason_code, cause_type, cause_id, cause_version)
VALUES (?,?,?,?,?,?,?)`,
				x.Seq, x.At, x.Scope, x.ReasonCode, x.CauseType, x.CauseID, x.CauseVersion); err != nil {
				return err
			}
		}
		return nil
	})
}
