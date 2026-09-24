// Package petri defines the domain model for token-flow nets: weighted
// place/transition Petri nets with capacity constraints and inhibitor arcs.
//
// Raw material is kept verbatim at the import layer. The assembled,
// normalized structures in this package are the derived layer: every numeric
// quantity still carries its source unit, and values that cannot be converted
// stay in place with a diagnostic instead of being dropped.
package petri

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Quantity is a numeric domain value that retains its original textual
// representation and unit. Canonical counts are always dimensionless integer
// tokens; a quantity that cannot be converted leaves Count unset and records a
// Diagnostic so drawing/assembly never silently discards it.
type Quantity struct {
	Raw         string `json:"raw"`
	Unit        string `json:"unit"`
	Count       int    `json:"count,omitempty"`
	Convertible bool   `json:"convertible"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

// ParseQuantity converts a raw value/unit pair into a dimensionless integer.
// Tokens and arc weights are unitless; capacities are expressed in tokens.
// Non-numeric or negative values stay attached to their origin with a
// diagnostic instead of forcing a default.
func ParseQuantity(raw string, unit string) Quantity {
	q := Quantity{Raw: strings.TrimSpace(raw), Unit: strings.TrimSpace(unit)}
	n, err := strconv.Atoi(q.Raw)
	if err != nil {
		q.Diagnostic = fmt.Sprintf("value %q is not an integer token count", raw)
		return q
	}
	if n < 0 {
		q.Diagnostic = fmt.Sprintf("value %q is negative", raw)
		return q
	}
	q.Count = n
	q.Convertible = true
	return q
}

// Coord is the geometry coordinate baseline used for network drawing. Origin
// and unit are declared once per model; coordinates here are already in that
// baseline.
type Coord struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Place is a state node. Capacity nil means unbounded.
type Place struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Coord     Coord     `json:"coord"`
	Initial   Quantity  `json:"initial"`
	Capacity  *Quantity `json:"capacity,omitempty"`
	Unbounded bool      `json:"unbounded"`
}

// Transition is an event node.
type Transition struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Coord Coord  `json:"coord"`
}

// Arc connects one node to another. Inhibitor arcs consume nothing and block
// firing while the source place holds at least the (weighted) threshold.
type Arc struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Weight    Quantity
	Inhibitor bool `json:"inhibitor,omitempty"`
}

// IsPT reports a normal place->transition input arc.
func (a Arc) IsPT() bool {
	return !a.Inhibitor && nodeKind(a.From) == kindPlace && nodeKind(a.To) == kindTransition
}

// IsTP reports a normal transition->place output arc.
func (a Arc) IsTP() bool {
	return !a.Inhibitor && nodeKind(a.From) == kindTransition && nodeKind(a.To) == kindPlace
}

// IsInhibitor reports an inhibitor arc place->transition.
func (a Arc) IsInhibitor() bool {
	return a.Inhibitor && nodeKind(a.From) == kindPlace && nodeKind(a.To) == kindTransition
}

const (
	kindPlace      = "P"
	kindTransition = "T"
)

func nodeKind(id string) string {
	if strings.HasPrefix(id, "P") {
		return kindPlace
	}
	return kindTransition
}

// Net is the assembled, normalized view of one token-flow model.
type Net struct {
	ID          string
	Name        string
	UnitBasis   string
	CoordBasis  string
	Places      []*Place
	Transitions []*Transition
	Arcs        []*Arc

	// Diagnostics preserves values that could not participate in derived
	// semantics. The corresponding nodes stay present in the drawing.
	Diagnostics []Diagnostic
}

// Diagnostic keeps an unconverted value at its origin.
type Diagnostic struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// ErrUnusableMaterial is returned when assembly cannot even produce a drawable
// structure (duplicate or dangling identities).
var ErrUnusableMaterial = errors.New("unusable net material")

// Assembly collects the seven versioned material slices. Every entry keeps its
// stable identity and the verbatim raw bytes are stored separately by the
// application layer.
type Assembly struct {
	Model      ModelMaterial
	Nodes      []NodeIdentity
	Arcs       []ArcWeight
	Initial    []InitialToken
	Capacities []CapacityRule
	Strategy   StrategySpec
	Budget     BudgetSpec
}

// ModelMaterial is the original model sheet (identity, baselines, ordering).
type ModelMaterial struct {
	NetID      string   `json:"net_id"`
	Name       string   `json:"name"`
	UnitBasis  string   `json:"unit_basis"`
	CoordBasis string   `json:"coord_basis"`
	PlaceOrder []string `json:"place_order"`
	TransOrder []string `json:"trans_order"`
}

// NodeIdentity declares a place or transition with its drawing coordinate.
type NodeIdentity struct {
	ID    string  `json:"id"`
	Kind  string  `json:"kind"` // "place" | "transition"
	Label string  `json:"label"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// ArcWeight declares one weighted arc in the original representation.
type ArcWeight struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	WeightRaw string `json:"weight_raw"`
	Unit      string `json:"unit"`
	Inhibitor bool   `json:"inhibitor,omitempty"`
}

// InitialToken assigns the initial marking of one place.
type InitialToken struct {
	Place string `json:"place"`
	Raw   string `json:"raw"`
	Unit  string `json:"unit"`
}

// CapacityRule declares the capacity boundary of one place. Unbounded places
// are declared explicitly so the boundary remains auditable.
type CapacityRule struct {
	Place     string `json:"place"`
	Raw       string `json:"raw"`
	Unit      string `json:"unit"`
	Unbounded bool   `json:"unbounded,omitempty"`
}

// StrategySpec is the versioned exploration strategy.
type StrategySpec struct {
	Kind     string            `json:"kind"` // "bfs" | "dfs"
	GoalRaw  map[string]string `json:"goal,omitempty"`
	GoalUnit string            `json:"goal_unit,omitempty"`
}

// BudgetSpec is the versioned truncation budget.
type BudgetSpec struct {
	MaxStates  int    `json:"max_states"`
	MaxFirings int    `json:"max_firings"`
	Unit       string `json:"unit"`
}

// Assemble builds a Net from versioned materials without irreversible
// cleaning: unconvertible quantities stay attached as diagnostics and the
// affected nodes remain drawable. Structural errors are fatal.
func Assemble(a Assembly) (*Net, error) {
	n := &Net{
		ID:         a.Model.NetID,
		Name:       a.Model.Name,
		UnitBasis:  a.Model.UnitBasis,
		CoordBasis: a.Model.CoordBasis,
	}

	placeByID := map[string]*Place{}
	transByID := map[string]*Transition{}
	checkID := map[string]bool{}

	for _, node := range a.Nodes {
		if checkID[node.ID] {
			return nil, fmt.Errorf("%w: duplicate node identity %q", ErrUnusableMaterial, node.ID)
		}
		checkID[node.ID] = true
		switch node.Kind {
		case "place":
			p := &Place{ID: node.ID, Label: node.Label, Coord: Coord{X: node.X, Y: node.Y}, Unbounded: true}
			n.Places = append(n.Places, p)
			placeByID[p.ID] = p
		case "transition":
			t := &Transition{ID: node.ID, Label: node.Label, Coord: Coord{X: node.X, Y: node.Y}}
			n.Transitions = append(n.Transitions, t)
			transByID[t.ID] = t
		default:
			return nil, fmt.Errorf("%w: node %q has unknown kind %q", ErrUnusableMaterial, node.ID, node.Kind)
		}
	}
	if len(n.Places) == 0 {
		return nil, fmt.Errorf("%w: model %q declares no places", ErrUnusableMaterial, n.ID)
	}

	// Initial tokens: preserve raw text and unit; non-convertible values are
	// retained as diagnostics and keep the place non-executable.
	for _, tok := range a.Initial {
		p, ok := placeByID[tok.Place]
		if !ok {
			return nil, fmt.Errorf("%w: initial token references unknown place %q", ErrUnusableMaterial, tok.Place)
		}
		q := ParseQuantity(tok.Raw, tok.Unit)
		if !q.Convertible {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{Subject: p.ID, Kind: "initial_token", Message: q.Diagnostic})
		}
		p.Initial = q
	}
	for _, p := range n.Places {
		if p.Initial.Raw == "" {
			p.Initial = ParseQuantity("0", "token")
		}
	}

	for _, cap := range a.Capacities {
		p, ok := placeByID[cap.Place]
		if !ok {
			return nil, fmt.Errorf("%w: capacity references unknown place %q", ErrUnusableMaterial, cap.Place)
		}
		if cap.Unbounded {
			p.Unbounded = true
			continue
		}
		q := ParseQuantity(cap.Raw, cap.Unit)
		if !q.Convertible {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{Subject: p.ID, Kind: "capacity", Message: q.Diagnostic})
			continue
		}
		p.Unbounded = false
		p.Capacity = &q
		if q.Count < p.Initial.Count && p.Initial.Convertible {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{
				Subject: p.ID, Kind: "capacity",
				Message: fmt.Sprintf("capacity %d below initial marking %d", q.Count, p.Initial.Count),
			})
		}
	}

	seenArc := map[string]bool{}
	for _, aw := range a.Arcs {
		if seenArc[aw.ID] {
			return nil, fmt.Errorf("%w: duplicate arc identity %q", ErrUnusableMaterial, aw.ID)
		}
		seenArc[aw.ID] = true
		_, fromP := placeByID[aw.From]
		_, fromT := transByID[aw.From]
		_, toP := placeByID[aw.To]
		_, toT := transByID[aw.To]
		if !fromP && !fromT {
			return nil, fmt.Errorf("%w: arc %q source %q missing", ErrUnusableMaterial, aw.ID, aw.From)
		}
		if !toP && !toT {
			return nil, fmt.Errorf("%w: arc %q target %q missing", ErrUnusableMaterial, aw.ID, aw.To)
		}
		if aw.Inhibitor {
			if !fromP || !toT {
				return nil, fmt.Errorf("%w: inhibitor arc %q must run place->transition", ErrUnusableMaterial, aw.ID)
			}
		} else if (fromP && toP) || (fromT && toT) {
			return nil, fmt.Errorf("%w: arc %q must cross place and transition", ErrUnusableMaterial, aw.ID)
		}
		w := ParseQuantity(aw.WeightRaw, aw.Unit)
		if !w.Convertible {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{Subject: aw.ID, Kind: "arc_weight", Message: w.Diagnostic})
		}
		n.Arcs = append(n.Arcs, &Arc{ID: aw.ID, From: aw.From, To: aw.To, Weight: w, Inhibitor: aw.Inhibitor})
	}

	orderPlaces(n, a.Model.PlaceOrder)
	orderTrans(n, a.Model.TransOrder)
	return n, nil
}

func orderPlaces(n *Net, order []string) {
	idx := map[string]int{}
	for i, id := range order {
		idx[id] = i
	}
	sort.SliceStable(n.Places, func(i, j int) bool {
		ai, oki := idx[n.Places[i].ID]
		bi, okj := idx[n.Places[j].ID]
		if oki && okj {
			return ai < bi
		}
		return n.Places[i].ID < n.Places[j].ID
	})
}

func orderTrans(n *Net, order []string) {
	idx := map[string]int{}
	for i, id := range order {
		idx[id] = i
	}
	sort.SliceStable(n.Transitions, func(i, j int) bool {
		ai, oki := idx[n.Transitions[i].ID]
		bi, okj := idx[n.Transitions[j].ID]
		if oki && okj {
			return ai < bi
		}
		return n.Transitions[i].ID < n.Transitions[j].ID
	})
}

// Executable reports whether derived semantics can run. A net is still
// drawable with diagnostics, but any malformed quantity blocks execution.
func (n *Net) Executable() bool {
	if len(n.Transitions) == 0 {
		return false
	}
	for _, d := range n.Diagnostics {
		if d.Kind == "initial_token" || d.Kind == "arc_weight" {
			return false
		}
	}
	for _, p := range n.Places {
		if !p.Initial.Convertible {
			return false
		}
	}
	for _, a := range n.Arcs {
		if !a.Weight.Convertible {
			return false
		}
		if a.Weight.Count == 0 && !a.Inhibitor {
			return false
		}
	}
	return true
}

// PlaceIndex returns the stable position of a place in declared order.
func (n *Net) PlaceIndex() map[string]int {
	m := map[string]int{}
	for i, p := range n.Places {
		m[p.ID] = i
	}
	return m
}

// InitialVector renders the initial marking in declared place order.
func (n *Net) InitialVector() Marking {
	m := make(Marking, len(n.Places))
	for i, p := range n.Places {
		m[i] = p.Initial.Count
	}
	return m
}

// GoalVector parses the strategy goal raw map against declared places.
func (n *Net) GoalVector(raw map[string]string) (Marking, map[string]string, bool) {
	m := make(Marking, len(n.Places))
	diag := map[string]string{}
	ok := true
	for i, p := range n.Places {
		text, present := raw[p.ID]
		if !present {
			m[i] = 0
			continue
		}
		q := ParseQuantity(text, "token")
		if !q.Convertible {
			diag[p.ID] = q.Diagnostic
			ok = false
			continue
		}
		m[i] = q.Count
	}
	return m, diag, ok
}
