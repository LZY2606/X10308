// Package explore implements the state-space rules for token-flow nets:
//
//	rule set enablement-1.0.0
//	  - a transition is enabled only when input arcs, inhibitor arcs AND
//	    output capacities all permit firing simultaneously;
//	  - canonical state keys list every net place, so markings that differ by
//	    any token count never merge;
//	  - when the truncation budget is exhausted the verdict is unknown and the
//	    unexplored frontier is retained verbatim.
package explore

import (
	"fmt"
	"sort"

	"tokenflow/internal/domain"
)

type Blocker struct {
	Kind    string `json:"kind"` // input | inhibitor | capacity
	ObjectID string `json:"object_id"`
	PlaceID string `json:"place_id"`
	Message string `json:"message"`
}

// Enabled reports whether t can fire in m under enablement-1.0.0. All three
// reasons are evaluated; every failing reason is returned rather than the
// first, so deadlock explanations cite inputs, inhibitors and capacities.
func Enabled(n *domain.Net, m domain.Marking, tID string) (bool, []Blocker) {
	var blockers []Blocker
	ins, outs, inhib := Adjacency(n, tID)
	for _, a := range ins {
		have := m[a.From]
		if have < a.Weight.Value {
			blockers = append(blockers, Blocker{
				Kind: "input", ObjectID: a.ID, PlaceID: a.From,
				Message: fmt.Sprintf("place %s holds %d, arc %s requires %d", a.From, have, a.ID, a.Weight.Value),
			})
		}
	}
	for _, a := range inhib {
		if have := m[a.From]; have > 0 {
			blockers = append(blockers, Blocker{
				Kind: "inhibitor", ObjectID: a.ID, PlaceID: a.From,
				Message: fmt.Sprintf("inhibitor arc %s: place %s holds %d tokens, requires 0", a.ID, a.From, have),
			})
		}
	}
	// Capacity is checked against the post-fire marking.
	next := m.Clone()
	for _, a := range ins {
		next[a.From] -= a.Weight.Value
	}
	for _, a := range outs {
		next[a.To] += a.Weight.Value
	}
	for _, a := range outs {
		if c, ok := n.Capacity[a.To]; ok && c.Convertible && !c.Unbounded {
			if next[a.To] > c.Value {
				blockers = append(blockers, Blocker{
					Kind: "capacity", ObjectID: a.ID, PlaceID: a.To,
					Message: fmt.Sprintf("firing would take place %s to %d, exceeding capacity %d", a.To, next[a.To], c.Value),
				})
			}
		}
	}
	sortBlockers(blockers)
	return len(blockers) == 0, blockers
}

type ArcRef struct {
	ID     string
	From   string
	To     string
	Weight int64
}

// Adjacency splits arcs of t into normal inputs/outputs and inhibitor inputs.
// Normal arcs with non-convertible weights are ignored by execution (they
// remain visible source data with diagnostics).
func Adjacency(n *domain.Net, tID string) (ins, outs []ArcRef, inhib []ArcRef) {
	for _, a := range n.Arcs {
		if a.Kind == domain.ArcInhibitor {
			if a.To == tID {
				inhib = append(inhib, ArcRef{ID: a.ID, From: a.From, To: a.To, Weight: 0})
			}
			continue
		}
		if !a.Weight.Convertible {
			continue
		}
		if a.To == tID {
			ins = append(ins, ArcRef{ID: a.ID, From: a.From, To: a.To, Weight: a.Weight.Value})
		}
		if a.From == tID {
			outs = append(outs, ArcRef{ID: a.ID, From: a.From, To: a.To, Weight: a.Weight.Value})
		}
	}
	sort.Slice(ins, func(i, j int) bool { return ins[i].ID < ins[j].ID })
	sort.Slice(outs, func(i, j int) bool { return outs[i].ID < outs[j].ID })
	sort.Slice(inhib, func(i, j int) bool { return inhib[i].ID < inhib[j].ID })
	return
}

// Fire assumes enablement; deterministic token consumption then production.
func Fire(n *domain.Net, m domain.Marking, tID string) domain.Marking {
	ins, outs, _ := Adjacency(n, tID)
	next := m.Clone()
	for _, a := range ins {
		next[a.From] -= a.Weight
	}
	for _, a := range outs {
		next[a.To] += a.Weight
	}
	return next
}

func TransitionOrder(n *domain.Net) []string {
	ids := make([]string, 0, len(n.Transitions))
	for _, t := range n.Transitions {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return ids
}

func sortBlockers(b []Blocker) {
	sort.Slice(b, func(i, j int) bool {
		if b[i].Kind != b[j].Kind {
			return b[i].Kind < b[j].Kind
		}
		return b[i].ObjectID < b[j].ObjectID
	})
}
