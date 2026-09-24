// Package petrinet defines the domain model for weighted place/transition
// Petri nets: places with capacities, weighted input/output arcs,
// inhibitor arcs, markings and firing semantics.
//
// All domain quantities carry a declared unit or coordinate basis; the
// import layer keeps the original textual representation while this layer
// works on the normalized integer token counts.
package petrinet

import (
	"fmt"
	"sort"
	"strings"
)

// ArcKind enumerates the directed relationships a node may have with a
// transition.
type ArcKind string

const (
	ArcInput     ArcKind = "input"     // place -> transition (consumes)
	ArcOutput    ArcKind = "output"    // transition -> place (produces)
	ArcInhibitor ArcKind = "inhibitor" // place -o transition (blocks when tokens >= weight)
)

// Place is a passive node. Capacity <= 0 means unbounded. Unit describes
// the counting basis of the tokens held by the place.
type Place struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity int64  `json:"capacity"` // <= 0 means unbounded
	Unit     string `json:"unit"`
	X        int    `json:"x"` // layout coordinate basis: svg user units
	Y        int    `json:"y"`
}

// Transition is an active node.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// Arc connects a place and a transition with a positive integer weight
// expressed in the place unit ("tokens" when the place is dimensionless).
type Arc struct {
	ID     string  `json:"id"`
	Place  string  `json:"place"`
	Trans  string  `json:"transition"`
	Kind   ArcKind `json:"kind"`
	Weight int64   `json:"weight"`
}

// Marking assigns a non-negative token count to places. Absent places hold
// zero tokens. Counts are in the per-place unit.
type Marking map[string]int64

// Net is an immutable (after construction) Petri net definition.
type Net struct {
	ID          string
	Name        string
	Places      []Place
	Transitions []Transition
	Arcs        []Arc

	places      map[string]*Place
	transitions map[string]*Transition
	// inputs/outputs/inhibitors keyed by transition, in import order.
	inputs     map[string][]Arc
	outputs    map[string][]Arc
	inhibitors map[string][]Arc
}

// Build validates references, weights and capacities and indexes the net.
func Build(id, name string, places []Place, transitions []Transition, arcs []Arc) (*Net, error) {
	n := &Net{
		ID:          id,
		Name:        name,
		Places:      places,
		Transitions: transitions,
		Arcs:        arcs,
		places:      map[string]*Place{},
		transitions: map[string]*Transition{},
		inputs:      map[string][]Arc{},
		outputs:     map[string][]Arc{},
		inhibitors:  map[string][]Arc{},
	}
	if id == "" {
		return nil, fmt.Errorf("net id is empty")
	}
	for i := range places {
		p := &places[i]
		if p.ID == "" {
			return nil, fmt.Errorf("place %d has empty id", i)
		}
		if _, dup := n.places[p.ID]; dup {
			return nil, fmt.Errorf("duplicate place id %q", p.ID)
		}
		n.places[p.ID] = p
	}
	for i := range transitions {
		t := &transitions[i]
		if t.ID == "" {
			return nil, fmt.Errorf("transition %d has empty id", i)
		}
		if _, dup := n.transitions[t.ID]; dup {
			return nil, fmt.Errorf("duplicate transition id %q", t.ID)
		}
		n.transitions[t.ID] = t
	}
	for i := range arcs {
		a := &arcs[i]
		if a.ID == "" {
			return nil, fmt.Errorf("arc %d has empty id", i)
		}
		if n.places[a.Place] == nil {
			return nil, fmt.Errorf("arc %q references unknown place %q", a.ID, a.Place)
		}
		if n.transitions[a.Trans] == nil {
			return nil, fmt.Errorf("arc %q references unknown transition %q", a.ID, a.Trans)
		}
		if a.Weight <= 0 {
			return nil, fmt.Errorf("arc %q has non-positive weight %d", a.ID, a.Weight)
		}
		switch a.Kind {
		case ArcInput:
			n.inputs[a.Trans] = append(n.inputs[a.Trans], *a)
		case ArcOutput:
			n.outputs[a.Trans] = append(n.outputs[a.Trans], *a)
		case ArcInhibitor:
			n.inhibitors[a.Trans] = append(n.inhibitors[a.Trans], *a)
		default:
			return nil, fmt.Errorf("arc %q has unknown kind %q", a.ID, a.Kind)
		}
	}
	return n, nil
}

// PlaceByID returns the place and an existence flag.
func (n *Net) PlaceByID(id string) (Place, bool) {
	p, ok := n.places[id]
	if !ok {
		return Place{}, false
	}
	return *p, true
}

// TransitionByID returns the transition and an existence flag.
func (n *Net) TransitionByID(id string) (Transition, bool) {
	t, ok := n.transitions[id]
	if !ok {
		return Transition{}, false
	}
	return *t, true
}

// InputArcs returns input arcs of a transition in import order.
func (n *Net) InputArcs(t string) []Arc { return n.inputs[t] }

// OutputArcs returns output arcs of a transition in import order.
func (n *Net) OutputArcs(t string) []Arc { return n.outputs[t] }

// InhibitorArcs returns inhibitor arcs of a transition in import order.
func (n *Net) InhibitorArcs(t string) []Arc { return n.inhibitors[t] }

// BlockReason describes why a transition is disabled, if known.
type BlockReason string

const (
	BlockInput     BlockReason = "insufficient_input_tokens"
	BlockInhibitor BlockReason = "inhibitor_arc_active"
	BlockCapacity  BlockReason = "output_capacity_exceeded"
)

// EnablementFailure reports one reason a transition cannot fire.
type EnablementFailure struct {
	Transition string      `json:"transition"`
	Reason     BlockReason `json:"reason"`
	Detail     string      `json:"detail"`
}

// Enabled reports whether t may fire in m. Enablement requires, together:
// every input arc is covered by enough tokens, every inhibitor arc sees
// strictly fewer tokens than its weight, AND firing keeps all bounded
// places within capacity.
func (n *Net) Enabled(t string, m Marking) bool {
	return n.checkEnable(t, m) == nil
}

// EnablementCheck returns nil when enabled or the first blocking reason.
func (n *Net) EnablementCheck(t string, m Marking) *EnablementFailure {
	return n.checkEnable(t, m)
}

func (n *Net) checkEnable(t string, m Marking) *EnablementFailure {
	if n.transitions[t] == nil {
		return &EnablementFailure{Transition: t, Reason: BlockInput, Detail: "unknown transition"}
	}
	for _, a := range n.inputs[t] {
		if m[a.Place] < a.Weight {
			return &EnablementFailure{
				Transition: t,
				Reason:     BlockInput,
				Detail:     fmt.Sprintf("place %s holds %d but arc %s requires %d %s", a.Place, m[a.Place], a.ID, a.Weight, n.unit(a.Place)),
			}
		}
	}
	for _, a := range n.inhibitors[t] {
		// Weighted inhibitor: active when tokens >= weight.
		if m[a.Place] >= a.Weight {
			return &EnablementFailure{
				Transition: t,
				Reason:     BlockInhibitor,
				Detail:     fmt.Sprintf("place %s holds %d and inhibitor arc %s blocks at >= %d %s", a.Place, m[a.Place], a.ID, a.Weight, n.unit(a.Place)),
			}
		}
	}
	// Capacity is evaluated on the post-firing marking so input and
	// output of the same firing (including self loops) are both counted.
	post := n.peekFire(t, m)
	for _, a := range n.outputs[t] {
		p := n.places[a.Place]
		if p.Capacity > 0 && post[a.Place] > p.Capacity {
			return &EnablementFailure{
				Transition: t,
				Reason:     BlockCapacity,
				Detail:     fmt.Sprintf("firing would raise place %s to %d, capacity is %d %s", a.Place, post[a.Place], p.Capacity, n.unit(a.Place)),
			}
		}
	}
	return nil
}

func (n *Net) unit(place string) string {
	if u := n.places[place].Unit; u != "" {
		return u
	}
	return "tokens"
}

func (n *Net) peekFire(t string, m Marking) Marking {
	post := m.Clone()
	for _, a := range n.inputs[t] {
		post[a.Place] -= a.Weight
	}
	for _, a := range n.outputs[t] {
		post[a.Place] += a.Weight
	}
	return post
}

// Fire returns the successor marking. Self loops consume and produce on
// the same place in one step.
func (n *Net) Fire(t string, m Marking) (Marking, error) {
	if f := n.checkEnable(t, m); f != nil {
		return nil, fmt.Errorf("transition %s not enabled: %s (%s)", t, f.Reason, f.Detail)
	}
	return n.peekFire(t, m), nil
}

// EnabledTransitions returns the enabled transitions in declared order.
func (n *Net) EnabledTransitions(m Marking) []string {
	var out []string
	for _, t := range n.Transitions {
		if n.Enabled(t.ID, m) {
			out = append(out, t.ID)
		}
	}
	return out
}

// DisabledTransitions returns every disabled transition with one reason.
func (n *Net) DisabledTransitions(m Marking) []EnablementFailure {
	var out []EnablementFailure
	for _, t := range n.Transitions {
		if f := n.checkEnable(t.ID, m); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// Clone returns an independent copy of the marking.
func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Key is the canonical serialization of a marking. Canonicalization keeps
// per-place token counts distinct: two markings with different counts
// never share a key. Zero entries are retained for places explicitly
// tracked so that {p:0} and {} differ where callers inserted the zero.
func (m Marking) Key() string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%d", m[k]))
	}
	return b.String()
}

// Equal reports exact token-by-token equality.
func (m Marking) Equal(o Marking) bool { return m.Key() == o.Key() }

// Covers reports m >= o component-wise on the union of their places.
func (m Marking) Covers(o Marking) bool {
	for k, v := range o {
		if m[k] < v {
			return false
		}
	}
	return true
}

// StrictlyDominates reports m >= o with at least one place strictly larger,
// over the union of places.
func (m Marking) StrictlyDominates(o Marking) bool {
	if !m.Covers(o) {
		return false
	}
	for k, v := range m {
		if v > o[k] {
			return true
		}
	}
	return false
}
