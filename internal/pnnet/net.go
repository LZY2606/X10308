// Package pnnet defines the immutable core domain model for a Petri net:
// places, transitions, weighted arcs, inhibitor arcs, capacities and markings.
//
// Domain quantities always travel with a unit or coordinate basis. A Raw
// field keeps the imported representation verbatim; normalization happens only
// in the derived layer, and values that cannot be converted stay in place with
// a diagnostic instead of being dropped.
package pnnet

import (
	"errors"
	"fmt"
	"sort"
)

// Basis classes for domain quantities.
const (
	UnitTokenCount = "token" // marking counts, arc weights, place capacities
	UnitBudget     = "state" // exploration truncation budget
	BasisSVGCoord  = "svg-user-unit"
)

// Raw is the verbatim representation of an imported scalar plus, when the
// derived layer could convert it, the normalized integer value.
//
// Unconvertible values are never deleted for the sake of drawing: Value stays
// nil and Diagnostics explains why. Callers must treat nil Value as unknown.
type Raw struct {
	Text        string   `json:"text"`
	Unit        string   `json:"unit"`
	Basis       string   `json:"basis,omitempty"`
	Value       *int64   `json:"value,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Kind discriminates node identities. Node identity survives re-imports: it is
// supplied by the modeling team's material and never renumbered by the system.
type Kind string

const (
	KindPlace      Kind = "place"
	KindTransition Kind = "transition"
)

// Place is a place node with an optional (possibly unbounded) capacity.
type Place struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	X        Raw    `json:"x"`
	Y        Raw    `json:"y"`
	Capacity Raw    `json:"capacity"` // nil Value means unbounded OR unconvertible
}

// HasCapacity reports whether an effective finite capacity is known.
func (p Place) HasCapacity() bool {
	return p.Capacity.Value != nil
}

// IsUnbounded reports an explicitly convertible unbounded marker (∞ or -1).
func (p Place) IsUnbounded() bool {
	return p.Capacity.Text != "" && p.Capacity.Value == nil && len(p.Capacity.Diagnostics) == 0
}

// Transition is a transition node.
type Transition struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	X     Raw    `json:"x"`
	Y     Raw    `json:"y"`
}

// Kind of arc.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor" // circle head; enables only when marking < weight
)

// Arc is a directed weighted arc. SameID endpoints mark a self loop.
type Arc struct {
	ID     string  `json:"id"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Kind   ArcKind `json:"kind"`
	Weight Raw     `json:"weight"`
}

// IsSelfLoop reports from == to (a transition feeding back into itself).
func (a Arc) IsSelfLoop() bool { return a.From == a.To }

// Node pairs the stable identity with its kind.
type Node struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Label string `json:"label,omitempty"`
}

// Net is the structural part of a model. It is treated as immutable content
// identified by the bytes it was imported from; edits create new versions.
type Net struct {
	ModelID     string            `json:"model_id"`
	Name        string            `json:"name"`
	Places      []Place           `json:"places"`
	Transitions []Transition      `json:"transitions"`
	Arcs        []Arc             `json:"arcs"`
	Nodes       []Node            `json:"nodes"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// PlaceByID indexes places.
func (n *Net) PlaceByID() map[string]*Place {
	m := make(map[string]*Place, len(n.Places))
	for i := range n.Places {
		m[n.Places[i].ID] = &n.Places[i]
	}
	return m
}

// TransitionByID indexes transitions.
func (n *Net) TransitionByID() map[string]*Transition {
	m := make(map[string]*Transition, len(n.Transitions))
	for i := range n.Transitions {
		m[n.Transitions[i].ID] = &n.Transitions[i]
	}
	return m
}

// Validate checks referential integrity and convertible weights. Unknown
// node identities or duplicated ids are fatal. Unconvertible scalars are not
// fatal: they remain on the owning object with diagnostics.
func (n *Net) Validate() error {
	seen := map[string]Kind{}
	for _, p := range n.Places {
		if p.ID == "" {
			return errors.New("place with empty id")
		}
		if k, ok := seen[p.ID]; ok {
			return fmt.Errorf("duplicate node id %q (already %s)", p.ID, k)
		}
		seen[p.ID] = KindPlace
	}
	for _, t := range n.Transitions {
		if t.ID == "" {
			return errors.New("transition with empty id")
		}
		if k, ok := seen[t.ID]; ok {
			return fmt.Errorf("duplicate node id %q (already %s)", t.ID, k)
		}
		seen[t.ID] = KindTransition
	}
	for _, a := range n.Arcs {
		fk, ok := seen[a.From]
		if !ok {
			return fmt.Errorf("arc %q references unknown source %q", a.ID, a.From)
		}
		tk, ok := seen[a.To]
		if !ok {
			return fmt.Errorf("arc %q references unknown target %q", a.ID, a.To)
		}
		if a.Kind != ArcNormal && a.Kind != ArcInhibitor {
			return fmt.Errorf("arc %q has unknown kind %q", a.ID, a.Kind)
		}
		if fk == tk {
			if fk != KindTransition || a.Kind != ArcNormal {
				return fmt.Errorf("self loop %q must connect a transition to itself with a normal arc", a.ID)
			}
		}
		if a.Weight.Value != nil && *a.Weight.Value <= 0 {
			return fmt.Errorf("arc %q weight must be positive, got %d", a.ID, *a.Weight.Value)
		}
	}
	return nil
}

// Preset/Postset helpers ------------------------------------------------------

// ArcRef groups an arc with its normalized weight (0 when unconvertible).
type ArcRef struct {
	Arc    Arc
	Weight int64
}

// Preset of a transition: normal arcs place -> transition.
func (n *Net) Preset(tid string) []ArcRef {
	var refs []ArcRef
	for _, a := range n.Arcs {
		if a.To == tid && a.Kind == ArcNormal {
			w := int64(1)
			if a.Weight.Value != nil {
				w = *a.Weight.Value
			}
			refs = append(refs, ArcRef{Arc: a, Weight: w})
		}
	}
	return refs
}

// Postset of a transition: normal arcs transition -> place (includes self loop).
func (n *Net) Postset(tid string) []ArcRef {
	var refs []ArcRef
	for _, a := range n.Arcs {
		if a.From == tid && a.Kind == ArcNormal {
			w := int64(1)
			if a.Weight.Value != nil {
				w = *a.Weight.Value
			}
			refs = append(refs, ArcRef{Arc: a, Weight: w})
		}
	}
	return refs
}

// Inhibitors of a transition: inhibitor arcs place -> transition.
func (n *Net) Inhibitors(tid string) []ArcRef {
	var refs []ArcRef
	for _, a := range n.Arcs {
		if a.To == tid && a.Kind == ArcInhibitor {
			w := int64(1)
			if a.Weight.Value != nil {
				w = *a.Weight.Value
			}
			refs = append(refs, ArcRef{Arc: a, Weight: w})
		}
	}
	return refs
}

// SortedPlaceIDs returns place ids in a stable order.
func (n *Net) SortedPlaceIDs() []string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// SortedTransitionIDs returns transition ids in a stable order.
func (n *Net) SortedTransitionIDs() []string {
	ids := make([]string, 0, len(n.Transitions))
	for _, t := range n.Transitions {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return ids
}
