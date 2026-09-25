// Package petri contains the normalized domain model for token-flow Petri nets
// and the firing semantics: transition enablement considers input arcs,
// inhibitor arcs and place capacities at the same time.
package petri

import (
	"fmt"
	"sort"
)

// ArcKind enumerates the arc roles supported by the normalized model.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

// Place is a normalized place declaration.
type Place struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity int64  `json:"capacity"` // -1 means unbounded (omega place capacity)
}

// Transition is a normalized transition declaration.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Arc is a directed weighted arc. Inhibitor arcs are read-only guards.
type Arc struct {
	ID      string  `json:"id"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Weight  int64   `json:"weight"`
	Kind    ArcKind `json:"kind"`
	RefsRaw string  `json:"refs_raw,omitempty"`
}

// Net is a normalized Petri net. Place order is significant for canonical keys.
type Net struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Places      []Place                `json:"places"`
	Transitions []Transition           `json:"transitions"`
	Arcs        []Arc                  `json:"arcs"`
	Meta        map[string]interface{} `json:"meta,omitempty"`
}

// Marking assigns a non-negative integer token count to every place.
type Marking map[string]int64

// PlaceIDs returns place identities in the canonical net order.
func (n *Net) PlaceIDs() []string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	return ids
}

// Index returns lookup structures built once per analysis.
type Index struct {
	Place      map[string]int64
	Capacity   map[string]int64
	Transition map[string]bool
	Input      map[string][]Arc // transition -> incoming normal arcs
	Output     map[string][]Arc // transition -> outgoing normal arcs
	Inhibitor  map[string][]Arc // transition -> incoming inhibitor arcs
}

// BuildIndex validates references and indexes the net.
func (n *Net) BuildIndex() (*Index, error) {
	idx := &Index{
		Place:      map[string]int64{},
		Capacity:   map[string]int64{},
		Transition: map[string]bool{},
		Input:      map[string][]Arc{},
		Output:     map[string][]Arc{},
		Inhibitor:  map[string][]Arc{},
	}
	seen := map[string]bool{}
	for _, p := range n.Places {
		if p.ID == "" {
			return nil, fmt.Errorf("place with empty id")
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate place id %q", p.ID)
		}
		seen[p.ID] = true
		idx.Place[p.ID] = 0
		idx.Capacity[p.ID] = p.Capacity
	}
	tseen := map[string]bool{}
	for _, t := range n.Transitions {
		if t.ID == "" {
			return nil, fmt.Errorf("transition with empty id")
		}
		if tseen[t.ID] {
			return nil, fmt.Errorf("duplicate transition id %q", t.ID)
		}
		tseen[t.ID] = true
		idx.Transition[t.ID] = true
	}
	for _, a := range n.Arcs {
		if a.Weight <= 0 {
			return nil, fmt.Errorf("arc %q has non-positive weight %d", a.ID, a.Weight)
		}
		switch a.Kind {
		case ArcNormal:
			if !tseen[a.From] && !seen[a.From] {
				return nil, fmt.Errorf("arc %q source %q is not a node", a.ID, a.From)
			}
			if !tseen[a.To] && !seen[a.To] {
				return nil, fmt.Errorf("arc %q target %q is not a node", a.ID, a.To)
			}
			if tseen[a.From] && seen[a.To] {
				idx.Input[a.From] = append(idx.Input[a.From], a)
			} else if seen[a.From] && tseen[a.To] {
				idx.Output[a.To] = append(idx.Output[a.To], a)
			} else {
				return nil, fmt.Errorf("arc %q must connect a place and a transition", a.ID)
			}
		case ArcInhibitor:
			// inhibitor arcs run place -> transition and carry no token flow
			if !seen[a.From] || !tseen[a.To] {
				return nil, fmt.Errorf("inhibitor arc %q must run place -> transition", a.ID)
			}
			idx.Inhibitor[a.To] = append(idx.Inhibitor[a.To], a)
		default:
			return nil, fmt.Errorf("arc %q has unknown kind %q", a.ID, a.Kind)
		}
	}
	for t := range idx.Input {
		sort.SliceStable(idx.Input[t], func(i, j int) bool { return idx.Input[t][i].From < idx.Input[t][j].From })
	}
	for t := range idx.Output {
		sort.SliceStable(idx.Output[t], func(i, j int) bool { return idx.Output[t][i].To < idx.Output[t][j].To })
	}
	for t := range idx.Inhibitor {
		sort.SliceStable(idx.Inhibitor[t], func(i, j int) bool { return idx.Inhibitor[t][i].From < idx.Inhibitor[t][j].From })
	}
	return idx, nil
}

// Enabled reports whether a transition is enabled. A transition is enabled
// only when every input place holds enough tokens, every inhibitor place
// holds fewer tokens than the inhibitor weight, and firing would not exceed
// any declared place capacity.
func (n *Net) Enabled(idx *Index, m Marking, transitionID string) (bool, string) {
	if !idx.Transition[transitionID] {
		return false, "unknown-transition"
	}
	for _, a := range idx.Input[transitionID] {
		if m[a.From] < a.Weight {
			return false, "input-insufficient"
		}
	}
	for _, a := range idx.Inhibitor[transitionID] {
		if m[a.From] >= a.Weight {
			return false, "inhibitor-blocked"
		}
	}
	next := m
	// evaluate capacity without mutating the caller's marking
	for _, a := range idx.Output[transitionID] {
		if cap, ok := idx.Capacity[a.To]; ok && cap >= 0 {
			delta := a.Weight
			// self-loops: a place can be both input and output; net effect first.
			in := int64(0)
			for _, inA := range idx.Input[transitionID] {
				if inA.From == a.To {
					in += inA.Weight
				}
			}
			if next[a.To]-in+delta > cap {
				return false, "capacity-exceeded"
			}
		}
	}
	return true, ""
}

// EnabledTransitions lists enabled transition ids in deterministic order.
func (n *Net) EnabledTransitions(idx *Index, m Marking) []string {
	var out []string
	for _, t := range n.Transitions {
		if ok, _ := n.Enabled(idx, m, t.ID); ok {
			out = append(out, t.ID)
		}
	}
	return out
}

// Fire computes the successor marking. Self-loops are ordinary normal arcs,
// so a consume+produce on the same place is handled naturally.
func (n *Net) Fire(idx *Index, m Marking, transitionID string) (Marking, error) {
	if ok, reason := n.Enabled(idx, m, transitionID); !ok {
		return nil, fmt.Errorf("transition %q not enabled: %s", transitionID, reason)
	}
	out := make(Marking, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, a := range idx.Input[transitionID] {
		out[a.From] -= a.Weight
	}
	for _, a := range idx.Output[transitionID] {
		out[a.To] += a.Weight
	}
	return out, nil
}

// CanonicalKey serializes a marking over the net's stable place order.
// Markings that differ in any place's token count produce different keys,
// so canonicalization never merges distinct markings.
func (n *Net) CanonicalKey(m Marking) string {
	var b []byte
	for i, p := range n.Places {
		if i > 0 {
			b = append(b, '|')
		}
		b = append(b, p.ID...)
		b = append(b, '=')
		b = append(b, []byte(fmtInt(m[p.ID]))...)
	}
	return string(b)
}

func fmtInt(v int64) string { return fmt.Sprintf("%d", v) }

// Clone returns a defensive copy of a marking.
func (m Marking) Clone() Marking {
	out := make(Marking, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
