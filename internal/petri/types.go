// Package petri contains the domain kernel for token-flow Petri nets:
// transition enablement (inputs + inhibitor arcs + capacities), marking
// canonicalization, budget-bounded state-space exploration and Karp-Miller
// coverability analysis.
package petri

// ArcKind classifies a weighted arc.
type ArcKind string

const (
	// ArcInput runs from a place to a transition (consumption).
	ArcInput ArcKind = "input"
	// ArcOutput runs from a transition to a place (production).
	ArcOutput ArcKind = "output"
	// ArcInhibitor runs from a place to a transition and enables only while
	// the place holds strictly fewer tokens than the arc weight (threshold).
	ArcInhibitor ArcKind = "inhibitor"
)

// Place is a stable node identity declared by source material.
type Place struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	CapRaw  string  `json:"cap_raw"`
	Cap     *int64 `json:"cap,omitempty"`
	CapUnit string  `json:"cap_unit"`
	X       *float64 `json:"x,omitempty"`
	Y       *float64 `json:"y,omitempty"`
}

// Transition is a stable node identity declared by source material.
type Transition struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	X    *float64 `json:"x,omitempty"`
	Y    *float64 `json:"y,omitempty"`
}

// Arc is a weighted directed connection. WeightRaw preserves the original
// textual representation; Weight is the derived normalized value.
type Arc struct {
	ID        string  `json:"id"`
	Kind      ArcKind `json:"kind"`
	Src       string  `json:"src"`
	Dst       string  `json:"dst"`
	WeightRaw string  `json:"weight_raw"`
	Weight    int64   `json:"weight"`
}

// Marking assigns token counts (unit: token) to place identities.
type Marking map[string]int64

// Diagnostic keeps an unconvertible value at its original position instead of
// silently dropping it during a derived normalization.
type Diagnostic struct {
	Path     string `json:"path"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	RawValue string `json:"raw_value"`
}

// Model is the interpreted (derived) layer. Source bytes always stay in the
// store; this layer only normalizes and annotates.
type Model struct {
	ID           string       `json:"id"`
	RulesVersion string       `json:"rules_version"`
	Places       []Place      `json:"places"`
	Transitions  []Transition `json:"transitions"`
	Arcs         []Arc        `json:"arcs"`
	Diagnostics  []Diagnostic `json:"diagnostics"`

	placeOrder []string
	inArc      map[string][]Arc // transition -> incoming normal arcs (place->t)
	outArc     map[string][]Arc // transition -> outgoing arcs (t->place)
	inhibArc   map[string][]Arc // transition -> inhibitor arcs (place->t)
}

// Index builds lookup tables. It must be called after interpretation.
func (m *Model) Index() {
	m.placeOrder = make([]string, 0, len(m.Places))
	for _, p := range m.Places {
		m.placeOrder = append(m.placeOrder, p.ID)
	}
	m.inArc = map[string][]Arc{}
	m.outArc = map[string][]Arc{}
	m.inhibArc = map[string][]Arc{}
	for _, a := range m.Arcs {
		switch a.Kind {
		case ArcInput:
			m.inArc[a.Dst] = append(m.inArc[a.Dst], a)
		case ArcOutput:
			m.outArc[a.Src] = append(m.outArc[a.Src], a)
		case ArcInhibitor:
			m.inhibArc[a.Dst] = append(m.inhibArc[a.Dst], a)
		}
	}
}

// PlaceOrder returns place identities in source-declared order.
func (m *Model) PlaceOrder() []string { return m.placeOrder }
