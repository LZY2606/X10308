// Package domain defines the versioned, source-preserving Petri net model.
//
// The model is split into two layers on purpose:
//   - Raw* fields keep exactly what the modeling group imported (including
//     values that cannot be converted). They are never overwritten.
//   - normalized fields (int weights/tokens/capacities) live in a derived
//     interpretation layer and are recomputed when parser parameters change.
package domain

import "fmt"

// Unit of a domain quantity. All domain quantities must carry a unit
// (tokens/capacity) or a coordinate basis (places/transitions).
type Unit string

const (
	UnitToken      Unit = "token"
	UnitCapacity   Unit = "capacity"
	UnitCoordinate Unit = "px"
)

// Coord is a layout coordinate measured on the document's coordinate basis.
type Coord struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Unit Unit    `json:"unit"`
	Basis string `json:"basis,omitempty"`
}

// Diagnosis records why a raw value could not be normalized. A value with a
// diagnosis stays at its original location; it is never dropped for drawing.
type Diagnosis struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
	Raw     string `json:"raw"`
}

// RawToken keeps the original textual representation of an initial marking.
type RawToken struct {
	PlaceID   string     `json:"place_id"`
	Raw       string     `json:"raw"`
	Unit      Unit       `json:"unit"`
	Value     int        `json:"value"`
	ConvertOK bool       `json:"convert_ok"`
	Diag      *Diagnosis `json:"diag,omitempty"`
}

// RawCapacity keeps the original textual representation of a place capacity.
// A null/absent capacity means unbounded (omega in coverability terms).
type RawCapacity struct {
	PlaceID   string     `json:"place_id"`
	Raw       string     `json:"raw"`
	Unit      Unit       `json:"unit"`
	Value     int        `json:"value"`
	Finite    bool       `json:"finite"`
	ConvertOK bool       `json:"convert_ok"`
	Diag      *Diagnosis `json:"diag,omitempty"`
}

type Place struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Coord    Coord  `json:"coord"`
	Capacity *RawCapacity
}

type Transition struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Coord Coord  `json:"coord"`
}

// ArcKind distinguishes normal flow arcs from inhibitor arcs.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

type Arc struct {
	ID     string  `json:"id"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight int     `json:"weight"`
	Kind   ArcKind `json:"kind"`
	RawWeight string `json:"raw_weight"`
}

// Params version the exploration strategy and the truncation budget.
type Params struct {
	Version            string `json:"version"`
	Strategy           string `json:"strategy"`
	Budget             int    `json:"budget"`
	InhibitorsEnabled  bool   `json:"inhibitors_enabled"`
	ParserVersion      string `json:"parser_version"`
	Notes              string `json:"notes,omitempty"`
}

// Scenario is one named situation inside a case document. A single document
// usually carries several nets (self-loop, concurrency, capacity boundary,
// inhibitor arc, unbounded growth, same graph different marking, truncation).
type Scenario struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Places      []*Place     `json:"places"`
	Transitions []*Transition `json:"transitions"`
	Arcs        []*Arc       `json:"arcs"`
	Tokens      []*RawToken  `json:"initial_tokens"`
	Goal        map[string]string `json:"goal,omitempty"`
	// BudgetOverride lets the truncation scenario run under its own cut budget
	// while the document-level Params stay versioned unchanged.
	BudgetOverride int `json:"budget_override,omitempty"`
}

// Document is the immutable raw source unit. Its bytes are stored verbatim.
type Document struct {
	Schema   string             `json:"schema"`
	DocID    string             `json:"doc_id"`
	Revision string             `json:"revision"`
	Title    string             `json:"title"`
	CoordBasis string           `json:"coord_basis"`
	Params   Params             `json:"params"`
	Scenarios []*Scenario       `json:"scenarios"`
}

// Net is a derived, normalized view of one scenario.
type Net struct {
	ScenarioID string
	Places     map[string]*Place
	Transitions map[string]*Transition
	// inputs/outputs for normal arcs only; inhibitors are separate.
	Inputs     map[string][]*Arc // transition -> incoming normal arcs
	Outputs    map[string][]*Arc // transition -> outgoing normal arcs
	Inhibitors map[string][]*Arc // transition -> inhibitor arcs
	Arcs       []*Arc
	Capacity   map[string]int // place -> finite capacity; absent means unbounded
	Bounded    map[string]bool
	Init       Marking
	Goal       *Marking
	// ParseDiagnostics collects non-fatal conversion problems. The offending
	// raw value still stays in RawToken/RawCapacity at its original node.
	ParseDiagnostics []*Diagnosis
}

// Marking maps place id -> token count. Different token counts always produce
// a different canonical key: canonicalization never merges them.
type Marking map[string]int

// Canonical renders a marking as a stable, domain-ordered string. Only places
// with non-zero tokens are listed, ordered by place id, so two markings are
// equal iff their canonical forms are equal.
func (m Marking) Canonical() string {
	keys := make([]string, 0, len(m))
	for p, v := range m {
		if v != 0 {
			keys = append(keys, p)
		}
	}
	// insertion sort on ids keeps determinism without importing sort here
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s=%d;", k, m[k])
	}
	return out
}

func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Equal compares markings without mutating either side.
func (m Marking) Equal(o Marking) bool { return m.Canonical() == o.Canonical() }
