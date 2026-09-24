package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
)

// Export is the portable archive of the whole workspace. Identity, order
// and provenance survive a delete-and-reimport round trip.
type Export struct {
	Format          string           `json:"format"` // "petriws-export"
	SchemaVersion   int              `json:"schema_version"`
	Sources         []Source         `json:"sources"`
	ModelVersions   []ModelVersion   `json:"model_versions"`
	Branches        []Branch         `json:"branches"`
	Runs            []Run            `json:"runs"`
	Candidates      []Candidate      `json:"candidates"`
	Events          []Event          `json:"events"`
	Dependencies    []Dependency     `json:"dependencies"`
	Diagnostics     []DiagnosticRow  `json:"diagnostics"`
	Counterexamples []Counterexample `json:"counterexamples"`
}

// Export writes the full workspace as deterministic JSON.
func (s *Store) Export(w io.Writer) error {
	ex := &Export{Format: "petriws-export", SchemaVersion: schemaVersion}
	var err error
	if ex.Sources, err = s.ListSources(); err != nil {
		return err
	}
	if ex.ModelVersions, err = s.ListModelVersions(); err != nil {
		return err
	}
	if ex.Branches, err = s.ListBranches(); err != nil {
		return err
	}
	if ex.Runs, err = s.ListRuns(""); err != nil {
		return err
	}
	if ex.Candidates, err = s.ListCandidates(""); err != nil {
		return err
	}
	if ex.Events, err = s.ListEvents("", 0); err != nil {
		return err
	}
	if ex.Diagnostics, err = s.ListDiagnostics(""); err != nil {
		return err
	}
	if ex.Counterexamples, err = s.ListCounterexamples(); err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT derived_kind,derived_id,source_id,version_seq FROM dependencies ORDER BY derived_kind,derived_id,source_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.DerivedKind, &d.DerivedID, &d.SourceID, &d.VersionSeq); err != nil {
			return err
		}
		ex.Dependencies = append(ex.Dependencies, d)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ex)
}

// Import replaces the workspace with an exported archive. It only runs on
// an empty database and executes in one transaction: a failed import
// leaves no half-written records.
func (s *Store) Import(r io.Reader) error {
	var ex Export
	if err := json.NewDecoder(r).Decode(&ex); err != nil {
		return fmt.Errorf("decode export: %w", err)
	}
	if ex.Format != "petriws-export" {
		return fmt.Errorf("not a petriws export")
	}
	var n int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sources)+(SELECT COUNT(*) FROM events)`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("import requires an empty database")
	}
	return s.InTx(func(tx *sql.Tx) error {
		for i := range ex.Sources {
			if err := CreateSource(tx, &ex.Sources[i]); err != nil {
				return fmt.Errorf("source %s: %w", ex.Sources[i].ID, err)
			}
		}
		for i := range ex.ModelVersions {
			if err := CreateModelVersion(tx, &ex.ModelVersions[i]); err != nil {
				return fmt.Errorf("model version %s: %w", ex.ModelVersions[i].ID, err)
			}
		}
		for i := range ex.Branches {
			if err := CreateBranch(tx, &ex.Branches[i]); err != nil {
				return fmt.Errorf("branch %s: %w", ex.Branches[i].ID, err)
			}
		}
		for i := range ex.Runs {
			if err := CreateRun(tx, &ex.Runs[i]); err != nil {
				return fmt.Errorf("run %s: %w", ex.Runs[i].ID, err)
			}
		}
		for i := range ex.Candidates {
			if err := CreateCandidate(tx, &ex.Candidates[i]); err != nil {
				return fmt.Errorf("candidate %s: %w", ex.Candidates[i].ID, err)
			}
		}
		for i := range ex.Events {
			e := ex.Events[i]
			if _, err := tx.Exec(`INSERT INTO events(seq,id,branch_id,kind,payload_json,model_version_id,rule_version,created_at)
				VALUES(?,?,?,?,?,?,?,?)`,
				e.Seq, e.ID, e.BranchID, e.Kind, e.PayloadJSON, e.ModelVersionID, e.RuleVersion, e.CreatedAt); err != nil {
				return fmt.Errorf("event %s: %w", e.ID, err)
			}
		}
		for i := range ex.Dependencies {
			if err := AddDependency(tx, &ex.Dependencies[i]); err != nil {
				return fmt.Errorf("dependency: %w", err)
			}
		}
		for i := range ex.Diagnostics {
			if err := CreateDiagnostic(tx, &ex.Diagnostics[i]); err != nil {
				return fmt.Errorf("diagnostic %s: %w", ex.Diagnostics[i].ID, err)
			}
		}
		for i := range ex.Counterexamples {
			if err := CreateCounterexample(tx, &ex.Counterexamples[i]); err != nil {
				return fmt.Errorf("counterexample %s: %w", ex.Counterexamples[i].ID, err)
			}
		}
		return nil
	})
}
