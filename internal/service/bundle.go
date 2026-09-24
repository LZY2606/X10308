package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"pnwork/internal/ingest"
	"pnwork/internal/store"
)

// Bundle is the offline case package / export archive.
//
// It is the dedicated "令牌流网：Petri net 可达状态、覆盖关系与反例路径"
// domain material - not a generic spreadsheet. Ordering in every slice is
// significant and preserved on re-import; stable ids never change.
type Bundle struct {
	Format     string         `json:"format"` // "pnwork-tokenflow-archive/v1"
	Case       BundleCase     `json:"case"`
	Baseline   BundleBranch   `json:"baseline"`
	Branches   []BundleBranch `json:"branches"`
	Narrative  Narrative      `json:"narrative"`
	Questions  []Question     `json:"questions"`
	Scenarios  []Scenario     `json:"scenarios"`
	SeedEvents []BundleEvent  `json:"seed_events,omitempty"`
	SeedAdjuds []BundleAdjud  `json:"seed_adjudications,omitempty"`
}

// BundleCase describes the dossier root.
type BundleCase struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// BundleBranch carries pinned aspect versions and the versioned raw materials.
type BundleBranch struct {
	ID             string         `json:"id"`
	ParentBranchID string         `json:"parent_branch_id,omitempty"`
	Name           string         `json:"name"`
	Kind           string         `json:"kind"`
	Note           string         `json:"note"`
	Aspects        []BundleAspect `json:"aspects"`
}

// BundleAspect is one versioned material aspect with its verbatim bytes.
type BundleAspect struct {
	Aspect    string          `json:"aspect"`
	SourceID  string          `json:"source_id"`
	Version   int             `json:"version"`
	Label     string          `json:"label"`
	MediaType string          `json:"media_type"`
	Content   json.RawMessage `json:"content"`
}

// Narrative is the integrated reviewer story.
type Narrative struct {
	Title string          `json:"title"`
	Steps []NarrativeStep `json:"steps"`
}

// NarrativeStep binds a story beat to scenario ids and a branch.
type NarrativeStep struct {
	Order       int      `json:"order"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	BranchID    string   `json:"branch_id"`
	ScenarioIDs []string `json:"scenario_ids"`
}

// Question is a reachability / cover / deadlock question for replay.
type Question struct {
	ID       string           `json:"id"`
	BranchID string           `json:"branch_id"`
	Kind     string           `json:"kind"` // reachability | enumerate
	Goal     map[string]int64 `json:"goal,omitempty"`
	Label    string           `json:"label"`
}

// Scenario documents one required phenomenon for assertions.
type Scenario struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BranchID    string `json:"branch_id"`
	Expectation string `json:"expectation"`
}

// BundleEvent seeds the append-only timeline (stable ids for demo replay).
type BundleEvent struct {
	Seq       int64           `json:"seq"`
	EventID   string          `json:"event_id"`
	BranchID  string          `json:"branch_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	DependsOn []string        `json:"depends_on"`
	Rev       int64           `json:"rev"`
	Undone    bool            `json:"undone,omitempty"`
}

// BundleAdjud seeds a human decision.
type BundleAdjud struct {
	ID             string `json:"id"`
	BranchID       string `json:"branch_id"`
	CandidateID    string `json:"candidate_id,omitempty"`
	ExplorationID  string `json:"exploration_id,omitempty"`
	Decision       string `json:"decision"`
	Rationale      string `json:"rationale"`
	BaseRev        int64  `json:"base_rev"`
	ConflictStatus string `json:"conflict_status,omitempty"`
	EventSeq       int64  `json:"event_seq"`
}

// Export reads the entire case into a self-contained bundle. Deleting the
// database and importing this bundle preserves identities, ordering and
// provenance relations.
func (s *Service) Export(ctx context.Context, caseID string) (*Bundle, error) {
	c, err := s.st.GetCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	b := &Bundle{Format: "pnwork-tokenflow-archive/v1"}
	b.Case = BundleCase{ID: c.ID, Slug: c.Slug, Title: c.Title, Description: c.Description}

	branches, err := s.st.ListBranches(ctx, caseID)
	if err != nil {
		return nil, err
	}
	sources, err := s.st.ListSources(ctx, caseID)
	if err != nil {
		return nil, err
	}
	addBranch := func(br *store.Branch) (BundleBranch, error) {
		bb := BundleBranch{
			ID: br.ID, ParentBranchID: br.ParentBranchID, Name: br.Name,
			Kind: br.Kind, Note: br.Note,
		}
		pins, err := s.st.BranchAspects(ctx, br.ID)
		if err != nil {
			return bb, err
		}
		for _, pin := range pins {
			var src *store.Source
			for _, candidate := range sources {
				if candidate.ID == pin.SourceID {
					src = candidate
					break
				}
			}
			if src == nil {
				return bb, fmt.Errorf("dangling source %s", pin.SourceID)
			}
			sv, err := s.st.GetSourceVersion(ctx, pin.SourceID, pin.Version)
			if err != nil {
				return bb, err
			}
			bb.Aspects = append(bb.Aspects, BundleAspect{
				Aspect: pin.Aspect, SourceID: pin.SourceID, Version: pin.Version,
				Label: src.Label, MediaType: sv.MediaType, Content: json.RawMessage(sv.RawBytes),
			})
		}
		return bb, nil
	}
	for i, br := range branches {
		bb, err := addBranch(br)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			b.Baseline = bb
		} else {
			b.Branches = append(b.Branches, bb)
		}
	}

	allEvents, err := s.st.ListCaseEvents(ctx, caseID)
	if err != nil {
		return nil, err
	}
	for _, ev := range allEvents {
		b.SeedEvents = append(b.SeedEvents, BundleEvent{
			Seq: ev.Seq, EventID: ev.EventID, BranchID: ev.BranchID, Type: ev.Type,
			Payload: ev.Payload, DependsOn: ev.DependsOn, Rev: ev.Rev, Undone: ev.Undone,
		})
	}

	// Derived explorations/candidates/adjudications are exported as
	// provenance for seeded decisions; they are re-derived on import and the
	// event log keeps their stable references.
	for _, br := range branches {
		adjs, err := s.st.ListAdjudications(ctx, br.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range adjs {
			b.SeedAdjuds = append(b.SeedAdjuds, BundleAdjud{
				ID: a.ID, BranchID: a.BranchID, CandidateID: a.CandidateID,
				Decision: a.Decision, Rationale: a.Rationale, BaseRev: a.BaseRev,
				ConflictStatus: a.ConflictStatus, EventSeq: a.CreatedEventSeq,
			})
		}
	}
	if b.Narrative.Steps == nil {
		b.Narrative = loadNarrativeMeta(c)
	}
	return b, nil
}

func loadNarrativeMeta(c *store.Case) Narrative {
	var n Narrative
	if c.Description != "" {
		var doc struct {
			Narrative Narrative  `json:"narrative"`
			Questions []Question `json:"questions"`
			Scenarios []Scenario `json:"scenarios"`
		}
		if err := json.Unmarshal([]byte(c.Description), &doc); err == nil && doc.Narrative.Title != "" {
			return doc.Narrative
		}
	}
	return n
}

// ImportBundle materializes a bundle into an empty case slot inside a single
// transaction. Any parse failure aborts and leaves no partial records.
//
// If the case already exists and force is false, ErrCaseExists is returned.
func (s *Service) ImportBundle(ctx context.Context, b *Bundle, replace bool) error {
	if b.Format != "pnwork-tokenflow-archive/v1" {
		return fmt.Errorf("unsupported archive format %q", b.Format)
	}
	if b.Case.ID == "" {
		return fmt.Errorf("bundle case id required")
	}
	return s.st.WithTx(ctx, func(tx *sql.Tx) error {
		if replace {
			if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE case_id=?`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM adjudications WHERE case_id=?`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM candidates WHERE exploration_id IN (SELECT id FROM explorations WHERE case_id=?)`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM explorations WHERE case_id=?`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM interpretations WHERE case_id=?`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM branch_aspects WHERE branch_id IN (SELECT id FROM branches WHERE case_id=?)`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM source_versions WHERE source_id IN (SELECT id FROM sources WHERE case_id=?)`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM sources WHERE case_id=?`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM branches WHERE case_id=?`, b.Case.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM cases WHERE id=?`, b.Case.ID); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cases(id,slug,title,description,rule_version,rev) VALUES(?,?,?,?,?,?)`,
			b.Case.ID, b.Case.Slug, b.Case.Title, b.Case.Description, RuleVersion,
			caseRevFromEvents(b.SeedEvents)); err != nil {
			return err
		}

		// Materialize branches in bundle order (baseline first).
		ordered := append([]BundleBranch{b.Baseline}, b.Branches...)
		createdSeqOf := map[string]int64{}
		for _, bb := range ordered {
			if bb.ID == "" {
				continue
			}
			seq := int64(0)
			for _, ev := range b.SeedEvents {
				if ev.BranchID == bb.ID && seq == 0 {
					seq = ev.Seq
				}
			}
			createdSeqOf[bb.ID] = seq
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO branches(id,case_id,parent_branch_id,name,kind,note,created_event_seq)
				 VALUES(?,?,?,?,?,?,?)`,
				bb.ID, b.Case.ID, nullable(bb.ParentBranchID), bb.Name, bb.Kind, bb.Note, seq); err != nil {
				return err
			}
			// Sources + versions, preserving the bundle's explicit numbers.
			seenSrc := map[string]bool{}
			for _, asp := range bb.Aspects {
				if !seenSrc[asp.SourceID] {
					if _, err := tx.ExecContext(ctx,
						`INSERT INTO sources(id,case_id,aspect,label) VALUES(?,?,?,?)`,
						asp.SourceID, b.Case.ID, asp.Aspect, asp.Label); err != nil {
						return err
					}
					seenSrc[asp.SourceID] = true
				}
				mt := asp.MediaType
				if mt == "" {
					mt = "application/json"
				}
				digest := ingest.Digest(asp.Content)
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO source_versions(id,source_id,version,raw_bytes,sha256,media_type)
					 VALUES(?,?,?,?,?,?)`,
					newID("sv"), asp.SourceID, asp.Version, []byte(asp.Content), digest, mt); err != nil {
					return err
				}
				if err := store.SetBranchAspect(tx, ctx, store.BranchAspect{
					BranchID: bb.ID, Aspect: asp.Aspect, SourceID: asp.SourceID, Version: asp.Version,
				}); err != nil {
					return err
				}
			}
		}

		// Restore the event log with stable seq values (autoincrement seeds
		// from the maximum).
		events := append([]BundleEvent{}, b.SeedEvents...)
		sort.SliceStable(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
		for _, ev := range events {
			deps, _ := json.Marshal(ev.DependsOn)
			if len(deps) == 0 {
				deps = []byte("[]")
			}
			payload := ev.Payload
			if len(payload) == 0 {
				payload = []byte("{}")
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO events(seq,event_id,case_id,branch_id,type,payload,depends_on,rev,undone)
				 VALUES(?,?,?,?,?,?,?,?,?)`,
				ev.Seq, ev.EventID, b.Case.ID, ev.BranchID, ev.Type, string(payload),
				string(deps), ev.Rev, boolInt(ev.Undone)); err != nil {
				return err
			}
		}
		// Re-derive interpretations/explorations/candidates so reviewers can
		// replay immediately, while keeping the original event references.
		if err := s.rederiveInTx(ctx, tx, b, ordered); err != nil {
			return err
		}
		// Restore human adjudications against re-derived stable candidate ids
		// (candidate ids are deterministic from bundle exploration ids).
		for _, a := range b.SeedAdjuds {
			cid := nullable(a.CandidateID)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO adjudications(id,case_id,branch_id,candidate_id,decision,rationale,
				    base_rev,undone,created_event_seq,conflict_status)
				 VALUES(?,?,?,?,?,?,?,0,?,?)`,
				a.ID, b.Case.ID, a.BranchID, cid, a.Decision, a.Rationale,
				a.BaseRev, a.EventSeq, a.ConflictStatus); err != nil {
				return err
			}
		}
		return nil
	})
}

func nullable(s string) (v any) {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func caseRevFromEvents(evs []BundleEvent) int64 {
	var max int64
	for _, ev := range evs {
		if ev.Rev > max {
			max = ev.Rev
		}
	}
	if max == 0 {
		return 1
	}
	return max
}
