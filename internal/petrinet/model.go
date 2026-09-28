// Package petrinet contains the domain model and token-flow semantics for
// Petri nets: weighted arcs, capacities, inhibitor arcs, enablement, firing,
// canonical marking identity, coverability, and budgeted state-space
// exploration.
//
// Design rules (see RULES_VERSION in internal/rules):
//
//	Enablement considers inputs, inhibitor arcs and capacities together.
//	Canonicalization never merghes markings whose token counts differ.
//	Exhausting the budget returns UNKNOWN and preserves the frontier.
package petrinet

import (
	"fmt"
	"sort"
	"strings"
)

// Marking maps a place stable ID to its token count. A missing key means zero.
type Marking map[string]int64

// Net is a resolved Petri net. Node IDs are authored stable identities and are
// never renumbered by the system.
type Net struct {
	ID string

	PlaceIDs      []string
	TransitionIDs []string

	// Place metadata keyed by stable place id.
	Capacity map[string]int64 // -1 means unbounded capacity
	PlaceOf  map[string]Place

	Transitions map[string]Transition

	// Arcs keyed by stable arc id.
	Arcs map[string]*Arc
}

// Place carries authored metadata. Coordinate basis and units are kept on the
// raw import record; Resolution records any value that could not be converted.
type Place struct {
	ID       string
	Name     string
	Capacity int64 // -1 unbounded
}

// Transition is a node with stable identity. Enabled/fired semantics only use
// incident arcs.
type Transition struct {
	ID   string
	Name string
}

// ArcKind is one of the three supported arc kinds.
type ArcKind string

const (
	ArcInput     ArcKind = "input"     // place -> transition (weight consumed)
	ArcOutput    ArcKind = "output"    // transition -> place (weight produced)
	ArcInhibitor ArcKind = "inhibitor" // place -> transition (enabled only when tokens < weight)
)

// Arc is a weighted directed edge. A self-loop is an input and output arc that
// share the same single place and transition.
type Arc struct {
	ID     string
	Kind   ArcKind
	Source string // place id for input/inhibitor, transition id for output
	Target string // transition id for input/inhibitor, place id for output
	Weight int64
}

// NewNet builds an empty, well-formed container.
func NewNet(id string) *Net {
	return &Net{
		ID:          id,
		Capacity:    map[string]int64{},
		PlaceOf:     map[string]Place{},
		Transitions: map[string]Transition{},
		Arcs:        map[string]*Arc{},
	}
}

// HasPlace reports whether p is a known place.
func (n *Net) HasPlace(p string) bool {
	_, ok := n.PlaceOf[p]
	return ok
}

// HasTransition reports whether t is a known transition.
func (n *Net) HasTransition(t string) bool {
	_, ok := n.Transitions[t]
	return ok
}

// Tokens returns the token count of place p in m (zero when absent).
func (m Marking) Tokens(p string) int64 { return m[p] }

// Canonical renders a marking as a stable, comparable string keyed by the
// sorted place IDs. Zero and absent places are rendered identically, but two
// markings that differ in token count always produce different keys.
func (m Marking) Canonical(placeIDs []string) string {
	var b strings.Builder
	for i, p := range placeIDs {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(p)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%d", m[p]))
	}
	return b.String()
}

// Clone returns an independent copy.
func (m Marking) Clone() Marking {
	out := make(Marking, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Equal reports exact token-count equality over all places.
func (m Marking) Equal(other Marking, placeIDs []string) bool {
	for _, p := range placeIDs {
		if m[p] != other[p] {
			return false
		}
	}
	return true
}

// Covers reports m >= other componentwise: every place has at least as many
// tokens. Omega markings are handled by Explore, not here.
func (m Marking) Covers(other Marking, placeIDs []string) bool {
	for _, p := range placeIDs {
		if m[p] < other[p] {
			return false
		}
	}
	return true
}

// sortedArcIDs returns arc ids in stable order.
func (n *Net) sortedArcIDs() []string {
	ids := make([]string, 0, len(n.Arcs))
	for id := range n.Arcs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
