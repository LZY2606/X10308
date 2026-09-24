// Package engine implements Petri net transition enablement, exact bounded
// reachability exploration with a truncation budget, and coverability graph
// construction (Karp-Miller omega construction).
package engine

import (
	"fmt"
	"sort"

	"tflocal/internal/domain"
)

// Omega marks an unbounded place component in a coverability marking.
const Omega int64 = -1

// OmegaMarking is a coverability marking; component value Omega means "any
// number of tokens" at that place.
type OmegaMarking map[string]int64

func (m OmegaMarking) Canon() string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		if m[k] == 0 {
			continue
		}
		if m[k] == Omega {
			out += k + "=w;"
		} else {
			out += fmt.Sprintf("%s=%d;", k, m[k])
		}
	}
	return out
}

// Clone returns a copy.
func (m OmegaMarking) Clone() OmegaMarking {
	c := make(OmegaMarking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Covers reports whether the omega marking covers a concrete marking:
// every concrete component is either matched by omega or dominated numerically.
func (m OmegaMarking) Covers(c domain.Marking) bool {
	for p, v := range c {
		mv, ok := m[p]
		if !ok || mv == 0 {
			if v != 0 {
				return false
			}
			continue
		}
		if mv == Omega {
			continue
		}
		if mv < int64(v) {
			return false
		}
	}
	for p, v := range m {
		if v != 0 && v != Omega {
			if c[p] > int(v) {
				return false
			}
		}
	}
	return true
}

// Enablement explains the three enablement conditions for one transition at
// one marking: input arcs, inhibitor arcs, and output capacity.
type Enablement struct {
	TransitionID string `json:"transition_id"`
	InputOK      bool   `json:"input_ok"`
	InhibitorOK  bool   `json:"inhibitor_ok"`
	CapacityOK   bool   `json:"capacity_ok"`
	Enabled      bool   `json:"enabled"`
	Details      []string `json:"details,omitempty"`
}

// Enable evaluates a transition. Inhibitors with weight w block when the
// place holds >= w tokens. Capacity is evaluated on the post-firing marking.
func Enable(n *domain.Net, tID string, m domain.Marking, inhibitorsEnabled bool) Enablement {
	e := Enablement{TransitionID: tID, InputOK: true, InhibitorOK: true, CapacityOK: true}
	for _, a := range n.Inputs[tID] {
		have := m[a.From]
		if have < a.Weight {
			e.InputOK = false
			e.Details = append(e.Details, fmt.Sprintf("input %s has %d < %d", a.From, have, a.Weight))
		}
	}
	if inhibitorsEnabled {
		for _, a := range n.Inhibitors[tID] {
			have := m[a.From]
			if have >= a.Weight {
				e.InhibitorOK = false
				e.Details = append(e.Details, fmt.Sprintf("inhibitor %s has %d >= %d", a.From, have, a.Weight))
			}
		}
	}
	// tentatively fire for capacity evaluation (only if inputs available)
	next := tentative(n, tID, m)
	for _, a := range n.Outputs[tID] {
		if n.Bounded[a.To] {
			if got := next[a.To]; got > n.Capacity[a.To] {
				e.CapacityOK = false
				e.Details = append(e.Details, fmt.Sprintf("capacity %s would reach %d > %d", a.To, got, n.Capacity[a.To]))
			}
		}
	}
	e.Enabled = e.InputOK && e.InhibitorOK && e.CapacityOK
	return e
}

func tentative(n *domain.Net, tID string, m domain.Marking) domain.Marking {
	c := m.Clone()
	for _, a := range n.Inputs[tID] {
		c[a.From] -= a.Weight
	}
	for _, a := range n.Outputs[tID] {
		c[a.To] += a.Weight
	}
	return c
}

// Fire assumes enablement has been verified.
func Fire(n *domain.Net, tID string, m domain.Marking) domain.Marking {
	return tentative(n, tID, m)
}

func sortedTransitions(n *domain.Net) []string {
	ids := make([]string, 0, len(n.Transitions))
	for id := range n.Transitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
