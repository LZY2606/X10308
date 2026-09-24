// Package pn implements the domain engine for token-flow Petri nets:
// transition enablement (inputs + inhibitor arcs + capacities), marking
// canonicalization, bounded reach-space exploration with budget truncation,
// and Karp-Miller style coverability analysis.
package pn

import (
	"fmt"
	"sort"
	"strings"
)

// ArcKind is the directed arc role.
type ArcKind string

const (
	ArcInput     ArcKind = "input"     // transition -> place? see Arc.From/To
	ArcOutput    ArcKind = "output"    // see Validate for direction rules
	ArcInhibitor ArcKind = "inhibitor" // place -> transition; enabled iff marking(place) < weight
)

// Place is a state node. Capacity is nil for an unbounded place (tokens).
type Place struct {
	ID       string
	Name     string
	Capacity *int64
	// Unit is the physical/process unit carried by tokens, e.g. "token".
	Unit string
	// Canvas coordinate basis (px, origin top-left) for layout preservation.
	X, Y float64
}

// Transition is an event node on the same px coordinate basis.
type Transition struct {
	ID   string
	Name string
	X, Y float64
}

// Arc is a weighted directed connection. Direction is determined by
// From/To node identities and Kind. Inhibitor arcs are place -> transition.
type Arc struct {
	ID     string
	Kind   ArcKind
	From   string
	To     string
	Weight int64
}

// Net is the immutable normalized model used by the engine.
type Net struct {
	Places      []Place
	Transitions []Transition
	Arcs        []Arc

	placeIndex map[string]int
	transIndex map[string]int
	// incoming[transID], outgoing[transID], inhibitors[transID]
	incoming   map[string][]Arc
	outgoing   map[string][]Arc
	inhibitors map[string][]Arc
}

// DisableReason explains why a transition cannot fire from a marking.
// A transition is enabled only when there are no input, inhibitor or
// capacity reasons.
type DisableReason struct {
	Transition string `json:"transition"`
	Code       string `json:"code"`
	Detail     string `json:"detail"`
	Subject    string `json:"subject"` // stable dependency subject id
}

const (
	ReasonInputShort = "input_short" // not enough input tokens
	ReasonInhibited  = "inhibited"   // inhibitor place has >= weight tokens
	ReasonCapacity   = "capacity"    // output place would exceed capacity
)

// Marking assigns token counts to places; missing places mean zero.
type Marking map[string]int64

func (n *Net) index() {
	n.placeIndex = make(map[string]int, len(n.Places))
	n.transIndex = make(map[string]int, len(n.Transitions))
	for i, p := range n.Places {
		n.placeIndex[p.ID] = i
	}
	for i, t := range n.Transitions {
		n.transIndex[t.ID] = i
	}
	n.incoming = make(map[string][]Arc, len(n.Transitions))
	n.outgoing = make(map[string][]Arc, len(n.Transitions))
	n.inhibitors = make(map[string][]Arc, len(n.Transitions))
	for _, a := range n.Arcs {
		switch a.Kind {
		case ArcInhibitor:
			n.inhibitors[a.To] = append(n.inhibitors[a.To], a)
		default:
			if _, isP := n.placeIndex[a.From]; isP {
				n.incoming[a.To] = append(n.incoming[a.To], a)
			} else {
				n.outgoing[a.From] = append(n.outgoing[a.From], a)
			}
		}
	}
	for k := range n.incoming {
		sort.SliceStable(n.incoming[k], func(i, j int) bool { return n.incoming[k][i].ID < n.incoming[k][j].ID })
	}
	for k := range n.outgoing {
		sort.SliceStable(n.outgoing[k], func(i, j int) bool { return n.outgoing[k][i].ID < n.outgoing[k][j].ID })
	}
	for k := range n.inhibitors {
		sort.SliceStable(n.inhibitors[k], func(i, j int) bool { return n.inhibitors[k][i].ID < n.inhibitors[k][j].ID })
	}
}

// Build validates raw normalized pieces and returns an indexed Net.
func Build(places []Place, transitions []Transition, arcs []Arc) (*Net, error) {
	n := &Net{Places: places, Transitions: transitions, Arcs: arcs}
	n.index()
	seen := map[string]bool{}
	for _, p := range places {
		if p.ID == "" {
			return nil, fmt.Errorf("place with empty id")
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate node id %q", p.ID)
		}
		seen[p.ID] = true
		if p.Capacity != nil && *p.Capacity < 0 {
			return nil, fmt.Errorf("place %q has negative capacity", p.ID)
		}
	}
	for _, t := range transitions {
		if t.ID == "" {
			return nil, fmt.Errorf("transition with empty id")
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("duplicate node id %q", t.ID)
		}
		seen[t.ID] = true
	}
	for _, a := range arcs {
		if a.ID == "" {
			return nil, fmt.Errorf("arc with empty id")
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("duplicate arc id %q", a.ID)
		}
		seen[a.ID] = true
		if a.Weight < 1 {
			return nil, fmt.Errorf("arc %q weight must be >= 1, got %d", a.ID, a.Weight)
		}
		_, fromP := n.placeIndex[a.From]
		_, toP := n.placeIndex[a.To]
		_, fromT := n.transIndex[a.From]
		_, toT := n.transIndex[a.To]
		if !fromP && !fromT {
			return nil, fmt.Errorf("arc %q source %q unknown", a.ID, a.From)
		}
		if !toP && !toT {
			return nil, fmt.Errorf("arc %q target %q unknown", a.ID, a.To)
		}
		switch a.Kind {
		case ArcInput:
			if !(fromP && toT) {
				return nil, fmt.Errorf("input arc %q must be place -> transition", a.ID)
			}
		case ArcOutput:
			if !(fromT && toP) {
				return nil, fmt.Errorf("output arc %q must be transition -> place", a.ID)
			}
		case ArcInhibitor:
			if !(fromP && toT) {
				return nil, fmt.Errorf("inhibitor arc %q must be place -> transition", a.ID)
			}
		default:
			return nil, fmt.Errorf("arc %q unknown kind %q", a.ID, a.Kind)
		}
	}
	return n, nil
}

// PlaceAt returns the place definition.
func (n *Net) PlaceAt(id string) (Place, bool) {
	i, ok := n.placeIndex[id]
	if !ok {
		return Place{}, false
	}
	return n.Places[i], true
}

// MarkingKey is the canonical serialization of a marking. Markings that
// differ in token count of ANY place produce different keys; token counts
// are never merged across places.
func (n *Net) MarkingKey(m Marking) string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(&b, "%s=%d", id, m[id])
	}
	return b.String()
}

// MarkingValid reports whether every finite token count is >= 0 and the
// raw marking does not reference unknown places.
func (n *Net) MarkingValid(m Marking) error {
	for id, v := range m {
		if _, ok := n.placeIndex[id]; !ok {
			return fmt.Errorf("unknown place %q in marking", id)
		}
		if v < 0 {
			return fmt.Errorf("negative token count at %q", id)
		}
	}
	return nil
}

// DisableReasons evaluates inputs, inhibitor arcs and capacities together
// and returns every reason the transition cannot fire.
func (n *Net) DisableReasons(tID string, m Marking) []DisableReason {
	var reasons []DisableReason
	for _, a := range n.incoming[tID] {
		if m[a.From] < a.Weight {
			reasons = append(reasons, DisableReason{
				Transition: tID, Code: ReasonInputShort, Subject: a.ID,
				Detail: fmt.Sprintf("place %s has %d tokens, arc %s requires %d", a.From, m[a.From], a.ID, a.Weight),
			})
		}
	}
	for _, a := range n.inhibitors[tID] {
		if m[a.From] >= a.Weight {
			reasons = append(reasons, DisableReason{
				Transition: tID, Code: ReasonInhibited, Subject: a.ID,
				Detail: fmt.Sprintf("inhibitor arc %s from %s blocks at %d >= %d tokens", a.ID, a.From, m[a.From], a.Weight),
			})
		}
	}
	// Capacity is evaluated against the post-firing marking; self-loops
	// (place both inputs and outputs the token) therefore stay legal.
	if len(reasons) == 0 {
		post := n.fireDelta(tID, m)
		for _, a := range n.outgoing[tID] {
			p, ok := n.PlaceAt(a.To)
			if !ok || p.Capacity == nil {
				continue
			}
			if post[a.To] > *p.Capacity {
				reasons = append(reasons, DisableReason{
					Transition: tID, Code: ReasonCapacity, Subject: "cap:" + a.To,
					Detail: fmt.Sprintf("place %s would hold %d > capacity %d", a.To, post[a.To], *p.Capacity),
				})
			}
		}
	}
	return reasons
}

// fireDelta returns the marking after subtracting inputs and adding outputs.
func (n *Net) fireDelta(tID string, m Marking) Marking {
	next := make(Marking, len(m))
	for k, v := range m {
		next[k] = v
	}
	for _, a := range n.incoming[tID] {
		next[a.From] -= a.Weight
	}
	for _, a := range n.outgoing[tID] {
		next[a.To] += a.Weight
	}
	return next
}

// Enabled reports whether the transition is enabled (inputs, inhibitor
// arcs and capacities all satisfied).
func (n *Net) Enabled(tID string, m Marking) bool {
	return len(n.DisableReasons(tID, m)) == 0
}

// Fire returns the successor marking, or false when not enabled.
func (n *Net) Fire(tID string, m Marking) (Marking, bool) {
	if !n.Enabled(tID, m) {
		return nil, false
	}
	return n.fireDelta(tID, m), true
}

// EnabledTransitions lists enabled transition ids in stable (id) order.
func (n *Net) EnabledTransitions(m Marking) []string {
	var out []string
	for _, t := range n.Transitions {
		if n.Enabled(t.ID, m) {
			out = append(out, t.ID)
		}
	}
	sort.Strings(out)
	return out
}

// CloneMarking returns a detached copy.
func CloneMarking(m Marking) Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
