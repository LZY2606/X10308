// Package petri contains the domain model for token-flow nets:
// Petri nets with weighted arcs, place capacities and inhibitor arcs,
// plus reachability / coverability / deadlock state-space exploration.
//
// Every domain quantity carries its unit or coordinate basis. Token counts
// use the "token" unit; node coordinates use a 0..1000 canvas coordinate
// basis. Raw, unnormalized representations live in the storage layer; this
// package only sees normalized values.
package petri

import (
	"fmt"
	"sort"
)

// UnitToken is the unit carried by every token count.
const UnitToken = "token"

// BasisCanvas is the coordinate basis of node coordinates (0..1000 square).
const BasisCanvas = "canvas-0..1000"

// Place is a Petri net place.
type Place struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Capacity int64  `json:"capacity"` // >=0 means a finite capacity in tokens; -1 means unbounded (omega place)
	X        int64  `json:"x"`        // canvas coordinate basis
	Y        int64  `json:"y"`
}

// Transition is a Petri net transition.
type Transition struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	X     int64  `json:"x"`
	Y     int64  `json:"y"`
}

// Arc is a directed weighted arc between a place and a transition.
// Kind is "in" (place -> transition, normal input) or "out" (transition -> place).
type Arc struct {
	ID     string `json:"id"`
	Place  string `json:"place"`
	Trans  string `json:"trans"`
	Kind   string `json:"kind"`
	Weight int64  `json:"weight"` // tokens moved per firing
}

// Inhibitor is an inhibitor arc (place -> transition, rendered with a hollow
// circle head). It never moves tokens: the transition is disabled while the
// place holds at least Threshold tokens.
type Inhibitor struct {
	ID        string `json:"id"`
	Place     string `json:"place"`
	Trans     string `json:"trans"`
	Threshold int64  `json:"threshold"` // tokens
}

// Net is a single structural graph: the places, transitions, weighted arcs
// and inhibitor arcs. Two models may share the same graph and differ in
// initial marking; Marking equality never collapses that distinction.
type Net struct {
	Places     []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs       []Arc        `json:"arcs"`
	Inhibitors []Inhibitor  `json:"inhibitors"`

	place map[string]*Place
	trans map[string]*Transition
}

// Index builds the lookup indexes used by the engine.
func (n *Net) Index() {
	n.place = make(map[string]*Place, len(n.Places))
	n.trans = make(map[string]*Transition, len(n.Transitions))
	for i := range n.Places {
		n.place[n.Places[i].ID] = &n.Places[i]
	}
	for i := range n.Transitions {
		n.trans[n.Transitions[i].ID] = &n.Transitions[i]
	}
}

// PlaceByID returns the place and whether it exists.
func (n *Net) PlaceByID(id string) (Place, bool) {
	if n.place == nil {
		n.Index()
	}
	p, ok := n.place[id]
	if !ok {
		return Place{}, false
	}
	return *p, true
}

// TransByID returns the transition and whether it exists.
func (n *Net) TransByID(id string) (Transition, bool) {
	if n.trans == nil {
		n.Index()
	}
	t, ok := n.trans[id]
	if !ok {
		return Transition{}, false
	}
	return *t, true
}

// Validate checks structural integrity of the net.
func (n *Net) Validate() error {
	n.Index()
	if n.place == nil {
		n.Index()
	}
	for _, a := range n.Arc {
		if _, ok := n.place[a.Place]; !ok {
			return fmt.Errorf("arc %s references unknown place %s", a.ID, a.Place)
		}
		if _, ok := n.trans[a.Trans]; !ok {
			return fmt.Errorf("arc %s references unknown transition %s", a.ID, a.Trans)
		}
		if a.Kind != "in" && a.Kind != "out" {
			return fmt.Errorf("arc %s has invalid kind %q", a.ID, a.Kind)
		}
		if a.Weight < 1 {
			return fmt.Errorf("arc %s weight must be >= 1 token, got %d", a.ID, a.Weight)
		}
	}
	for _, ih := range n.Inhibitors {
		if _, ok := n.place[ih.Place]; !ok {
			return fmt.Errorf("inhibitor %s references unknown place %s", ih.ID, ih.Place)
		}
		if _, ok := n.trans[ih.Trans]; !ok {
			return fmt.Errorf("inhibitor %s references unknown transition %s", ih.ID, ih.Trans)
		}
		if ih.Threshold < 1 {
			return fmt.Errorf("inhibitor %s threshold must be >= 1 token, got %d", ih.ID, ih.Threshold)
		}
	}
	return nil
}

// SortedPlaceIDs returns place ids in stable (insertion) order.
func (n *Net) SortedPlaceIDs() []string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// Marking is a vector of token counts, one per place id, in the "token" unit.
// A Marking is a value type keyed by place id; two markings with different
// token counts are distinct states and canonicalization never merges them.
type Marking map[string]int64

// Clone returns a copy of the marking.
func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Equal reports exact marking equality (place ids and token counts).
func (m Marking) Equal(other Marking) bool {
	if len(m) != len(other) {
		return false
	}
	for k, v := range m {
		if ov, ok := other[k]; !ok || ov != v {
			return false
		}
	}
	for k := range other {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// Covers reports m >= other place-by-place (m covers other).
func (m Marking) Covers(other Marking) bool {
	for k, v := range other {
		if m[k] < v {
			return false
		}
	}
	return true
}

// StrictlyCovers reports m covers other but is not equal to it.
func (m Marking) StrictlyCovers(other Marking) bool {
	return m.Covers(other) && !m.Equal(other)
}
