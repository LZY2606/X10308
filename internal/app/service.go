// Package app wires the petri engine, the SQLite store and the replayable
// counterexample case pack together behind a small service API.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"tokenflownet/internal/petri"
	"tokenflownet/internal/store"
)

// Service is the application facade used by the HTTP layer.
type Service struct {
	st *store.Store
}

// New constructs a service over an open store.
func New(st *store.Store) *Service { return &Service{st: st} }

// Store exposes the persistence layer (export/import, tests).
func (s *Service) Store() *store.Store { return s.st }

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func iso() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

// ImportMaterial commits one new material version transactionally.
func (s *Service) ImportMaterial(ctx context.Context, kind, id string, version int, title, supersedes string, raw any) (store.MaterialRow, error) {
	rawBytes, err := json.Marshal(raw)
	if err != nil {
		return store.MaterialRow{}, err
	}
	batchID := fmt.Sprintf("imp-%s-%d", id, version)
	m := store.MaterialRow{
		ID: id, Version: version, Kind: kind, Title: title,
		Raw: rawBytes, SHA256: hash(rawBytes), ImportBatch: batchID, Supersedes: supersedes,
	}
	err = s.st.WithTx(ctx, func(tx txIface) error {
		if _, err := tx.Exec(`INSERT INTO import_batch
(id, at_event, status, pack_version, note) VALUES (?,?,?,?,?)`,
			batchID, iso(), "committed", "", "material import"); err != nil {
			return err
		}
		if err := s.st.AddMaterialTx(ctx, tx, m, nil); err != nil {
			return err
		}
		_, err := tx.Event(store.Event{
			When: iso(), Type: store.EvImportCommitted, Actor: "importer",
			RefType: "material", RefID: fmt.Sprintf("%s@%d", id, version),
			Payload: string(rawBytes),
		})
		return err
	})
	return m, err
}

// CreateBranch pins ordered material versions to a new branch.
func (s *Service) CreateBranch(ctx context.Context, id, title, parent string, pins map[string]matPin) (store.BranchRow, error) {
	b := store.BranchRow{ID: id, Title: title, ParentID: parent, Open: true}
	slot := 0
	for _, kind := range []string{store.MatNet, store.MatMarking, store.MatRules, store.MatStrategy, store.MatDecision} {
		pin, ok := pins[kind]
		if !ok {
			continue
		}
		slot++
		b.Materials = append(b.Materials, store.MaterialSlot{
			MaterialID: pin.ID, Version: pin.Version, Slot: slot, Kind: kind,
		})
	}
	err := s.st.WithTx(ctx, func(tx txIface) error {
		_, err := s.st.CreateBranchTx(ctx, tx, b)
		return err
	})
	return b, err
}

type matPin struct {
	ID      string
	Version int
}

// InterpretedView is the derived model plus its raw net and initial marking.
type InterpretedView struct {
	Model        *petri.Model        `json:"model"`
	RawNet       *petri.RawModel     `json:"raw_net"`
	Initial      petri.Marking       `json:"initial"`
	ModelRow     store.ModelRow      `json:"model_row"`
	Diagnostics  []petri.Diagnostic  `json:"diagnostics"`
}

// InterpretBranch interprets the pinned net+marking versions of a branch and
// persists the derived layer (never rewriting the raw layer).
func (s *Service) InterpretBranch(ctx context.Context, branchID string) (*InterpretedView, error) {
	branch, err := s.st.GetBranch(ctx, branchID)
	if err != nil {
		return nil, err
	}
	var netPin, markPin store.MaterialSlot
	for _, sl := range branch.Materials {
		switch sl.Kind {
		case store.MatNet:
			netPin = sl
		case store.MatMarking:
			markPin = sl
		}
	}
	netMat, err := s.st.GetMaterial(ctx, netPin.MaterialID, netPin.Version)
	if err != nil {
		return nil, err
	}
	markMat, err := s.st.GetMaterial(ctx, markPin.MaterialID, markPin.Version)
	if err != nil {
		return nil, err
	}
	var rawNet petri.RawModel
	if err := json.Unmarshal(netMat.Raw, &rawNet); err != nil {
		return nil, fmt.Errorf("net material %s@%d: %w", netMat.ID, netMat.Version, err)
	}
	var markDoc struct {
		Entries []petri.RawMark `json:"entries"`
	}
	if err := json.Unmarshal(markMat.Raw, &markDoc); err != nil {
		return nil, fmt.Errorf("marking material %s@%d: %w", markMat.ID, markMat.Version, err)
	}
	rawNet.Initial = markDoc.Entries

	model := petri.Interpret(&rawNet)
	initial := model.InitialMarking(&rawNet)

	modelID := fmt.Sprintf("mdl-%s-n%d-m%d", branchID, netPin.Version, markPin.Version)
	derived, _ := json.Marshal(map[string]any{
		"model": model, "initial": initial,
	})
	var createdEvent int64
	err = s.st.WithTx(ctx, func(tx txIface) error {
		ev, err := s.st.InsertModelTx(ctx, tx, store.ModelRow{
			ID: modelID, BranchID: branchID,
			MaterialID: netMat.ID, MaterialVersion: netPin.Version,
			RulesVersion: model.RulesVersion, Derived: derived,
		})
		if err != nil {
			return err
		}
		createdEvent = ev
		if err := s.st.AddDependencyTx(ctx, tx, "model", modelID, "material", netMat.ID, netPin.Version, "interprets"); err != nil {
			return err
		}
		if err := s.st.AddDependencyTx(ctx, tx, "model", modelID, "material", markMat.ID, markPin.Version, "initial_marking"); err != nil {
			return err
		}
		if err := s.st.AddDependencyTx(ctx, tx, "model", modelID, "material", "rules-enablement",
			rulesVersion(model.RulesVersion), "enablement_rules"); err != nil {
			return err
		}
		for _, d := range model.Diagnostics {
			_, err := tx.Event(store.Event{
				When: iso(), Type: store.EvDiagnosticKept, Actor: "interpreter",
				BranchID: branchID, RefType: "model", RefID: modelID,
				Payload: mustJSON(d),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mr, _ := s.st.GetModel(ctx, modelID)
	return &InterpretedView{
		Model: model, RawNet: &rawNet, Initial: initial,
		ModelRow: mr, Diagnostics: model.Diagnostics,
	}, nil
}

func rulesVersion(v string) int {
	switch v {
	case "rules-enablement-v2":
		return 2
	default:
		return 1
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
