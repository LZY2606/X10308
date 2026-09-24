package service

import (
	"context"
	"encoding/json"
	"sort"
)

// HistoricalView is the visible state of a branch when replayed up to an
// event sequence. Reviewing history never overwrites it with newest results.
type HistoricalView struct {
	AsOfSeq       int64                    `json:"as_of_seq"`
	AspectVers    map[string]int           `json:"aspect_versions"`
	Pins          map[string]string        `json:"pins"`
	Explorations  []HistoricalExploration  `json:"explorations"`
	Adjudications []HistoricalAdjudication `json:"adjudications"`
	Events        []HistoricalEvent        `json:"events"`
}

// HistoricalEvent is one visible timeline entry at the replay point.
type HistoricalEvent struct {
	Seq     int64           `json:"seq"`
	EventID string          `json:"event_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Undone  bool            `json:"undone"`
	Rev     int64           `json:"rev"`
}

// HistoricalExploration is a run visible at the replay point, with its verdict.
type HistoricalExploration struct {
	ID      string          `json:"id"`
	Seq     int64           `json:"seq"`
	Valid   bool            `json:"valid"`
	Result  json.RawMessage `json:"result"`
	Payload json.RawMessage `json:"payload"`
}

// HistoricalAdjudication is a decision visible at the replay point.
type HistoricalAdjudication struct {
	ID          string `json:"id"`
	Seq         int64  `json:"seq"`
	Decision    string `json:"decision"`
	CandidateID string `json:"candidate_id"`
	Undone      bool   `json:"undone"`
}

// Replay reconstructs the branch view as it was up to asOfSeq (inclusive).
//
// Replay walks the append-only event log: branch creation establishes pins,
// aspect updates advance one version, explorations and adjudications become
// visible in order, and undo events hide the matching decision. Current
// recomputation never replaces these historical conclusions.
func (s *Service) Replay(ctx context.Context, branchID string, asOfSeq int64) (*HistoricalView, error) {
	events, err := s.st.ListEvents(ctx, branchID)
	if err != nil {
		return nil, err
	}
	view := &HistoricalView{AsOfSeq: asOfSeq, AspectVers: map[string]int{}, Pins: map[string]string{}}

	// Establish starting pins from the branch's baseline (pins are copied at
	// fork; for replay we track mutations from the first branch_created event).
	pins, err := s.st.BranchAspects(ctx, branchID)
	if err != nil {
		return nil, err
	}
	current := map[string]int{}
	for _, p := range pins {
		// Walk source version history backwards to find the version active at
		// asOfSeq: the highest version created at/before that event. Because
		// versions are appended in order, start from 1 unless later changed.
		current[p.Aspect] = 1
	}

	// Undo set: decisions hidden when their undo appears.
	undoneAdj := map[string]bool{}
	undoneByEvent := map[int64]bool{}

	for _, ev := range events {
		if ev.Seq > asOfSeq {
			break
		}
		view.Events = append(view.Events, HistoricalEvent{
			Seq: ev.Seq, EventID: ev.EventID, Type: ev.Type,
			Payload: ev.Payload, Undone: ev.Undone, Rev: ev.Rev,
		})
		switch ev.Type {
		case EvBranchCreated:
			var pl struct {
				Pins []struct {
					Aspect  string `json:"aspect"`
					Version int    `json:"version"`
				} `json:"pins"`
			}
			_ = json.Unmarshal(ev.Payload, &pl)
			for _, p := range pl.Pins {
				current[p.Aspect] = p.Version
			}
		case EvAspectImported:
			var pl struct {
				Aspect  string `json:"aspect"`
				Version int    `json:"version"`
			}
			_ = json.Unmarshal(ev.Payload, &pl)
			current[pl.Aspect] = pl.Version
		case EvExploration:
			var pl struct {
				ExplorationID string `json:"exploration_id"`
			}
			_ = json.Unmarshal(ev.Payload, &pl)
			x, err := s.st.GetExploration(ctx, pl.ExplorationID)
			if err == nil {
				// Historical validity: if invalidation event is later than
				// asOfSeq the run was still valid at the replay point.
				valid := x.Valid
				if x.InvalidatedByEventSeq != nil && *x.InvalidatedByEventSeq > asOfSeq {
					valid = true
				}
				if x.CreatedEventSeq > asOfSeq {
					valid = valid // created before cutoff by definition
				}
				view.Explorations = append(view.Explorations, HistoricalExploration{
					ID: x.ID, Seq: ev.Seq, Valid: valid,
					Result: x.Result, Payload: ev.Payload,
				})
			}
		case EvAdjudicated:
			var pl struct {
				AdjudicationID string `json:"adjudication_id"`
				CandidateID    string `json:"candidate_id"`
				Decision       string `json:"decision"`
			}
			_ = json.Unmarshal(ev.Payload, &pl)
			if !undoneAdj[pl.AdjudicationID] && !undoneByEvent[ev.Seq] {
				view.Adjudications = append(view.Adjudications, HistoricalAdjudication{
					ID: pl.AdjudicationID, Seq: ev.Seq, Decision: pl.Decision,
					CandidateID: pl.CandidateID,
				})
			}
		case EvAdjUndone:
			var pl struct {
				AdjudicationID string `json:"adjudication_id"`
			}
			_ = json.Unmarshal(ev.Payload, &pl)
			undoneAdj[pl.AdjudicationID] = true
			filtered := view.Adjudications[:0]
			for _, a := range view.Adjudications {
				if a.ID != pl.AdjudicationID {
					filtered = append(filtered, a)
				}
			}
			view.Adjudications = filtered
		}
		if ev.Undone {
			undoneByEvent[ev.Seq] = true
		}
	}
	for a, v := range current {
		view.AspectVers[a] = v
		view.Pins[a] = DepKey(a, v)
	}
	sort.Slice(view.Explorations, func(i, j int) bool {
		return view.Explorations[i].Seq < view.Explorations[j].Seq
	})
	sort.Slice(view.Adjudications, func(i, j int) bool {
		return view.Adjudications[i].Seq < view.Adjudications[j].Seq
	})
	return view, nil
}
