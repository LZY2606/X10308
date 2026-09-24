// Package petrinet contains the domain model of a token-flow net (Petri net):
// places, transitions, weighted arcs, markings, capacities and inhibitor arcs.
//
// Two layers are deliberately kept separate:
//   - Raw* types preserve the original representation (strings, units,
//     coordinate basis) exactly as imported. Nothing is dropped to make
//     plotting possible.
//   - Normalized types are the derived layer used by reachability analysis.
//
// Values that cannot be converted are left in place in the raw layer and
// attached a Diagnostic, rather than being silently cleaned away.
package petrinet

// Coordinate is the original geometric anchor of a node in the author's
// coordinate basis. It is retained verbatim for stable rendering.
type Coordinate struct {
	X      string `json:"x"`
	Y      string `json:"y"`
	Basis  string `json:"basis"`
	Unit   string `json:"unit,omitempty"`
}

// Quantity is a domain quantity carrying its original textual representation
// and its unit / coordinate basis. Normalization converts Text into Value in
// the derived layer; if conversion fails, ValueOK is false and the raw text is
// preserved with a Diagnostic instead of being discarded.
type Quantity struct {
	Text      string `json:"text"`
	Unit      string `json:"unit,omitempty"`
	Basis     string `json:"basis,omitempty"`
	Value     int64  `json:"value,omitempty"`
	ValueOK   bool   `json:"value_ok"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// RawPlace is a place as authored.
type RawPlace struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Capacity Quantity   `json:"capacity"`
	Initial  Quantity   `json:"initial"`
	Coord    Coordinate `json:"coord"`
}

// RawTransition is a transition as authored.
type RawTransition struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Coord Coordinate `json:"coord"`
}

// RawArc is a directed weighted arc as authored. Kind is one of:
//   "input"     : place -> transition (normal input arc)
//   "output"    : transition -> place
//   "inhibitor" : place -> transition (enables only while place holds fewer
//                            tokens than the arc weight)
type RawArc struct {
	ID     string   `json:"id"`
	From   string   `json:"from"`
	To     string   `json:"to"`
	Kind   string   `json:"kind"`
	Weight Quantity `json:"weight"`
}

// RawNet is the immutable original material snapshot of one model version.
type RawNet struct {
	Schema      string         `json:"schema"`
	ModelID     string         `json:"model_id"`
	VersionID   string         `json:"version_id"`
	BasedOn     string         `json:"based_on,omitempty"`
	Label       string         `json:"label"`
	Places      []RawPlace     `json:"places"`
	Transitions []RawTransition `json:"transitions"`
	Arcs        []RawArc       `json:"arcs"`
}

// NormalizedNet is the derived, analysis-ready layer. IDs are stable and come
// straight from the raw layer so provenance never loses node identity.
type NormalizedNet struct {
	ModelID     string
	VersionID   string
	Places      []Place
	Transitions []Transition
	Arcs        []Arc
	// PlaceOrder/TransitionOrder hold the authored ordering, which must be
	// preserved across export/import.
	PlaceOrder      []string
	TransitionOrder []string
	Diagnostics     []Diagnostic
}

// Place is a normalized place. Capacity < 0 means unbounded.
type Place struct {
	ID       string
	Name     string
	Initial  int64
	Capacity int64
	Coord    Coordinate
}

// Transition only carries identity; structure lives on arcs.
type Transition struct {
	ID    string
	Name  string
	Coord Coordinate
}

// Arc is a normalized weighted arc.
type Arc struct {
	ID     string
	From   string
	To     string
	Kind   ArcKind
	Weight int64
}

// ArcKind enumerates the three supported arc kinds.
type ArcKind string

const (
	ArcInput     ArcKind = "input"
	ArcOutput    ArcKind = "output"
	ArcInhibitor ArcKind = "inhibitor"
)

// Diagnostic records an import/normalization problem without dropping data.
type Diagnostic struct {
	ObjectID string `json:"object_id"`
	Field    string `json:"field"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Raw      string `json:"raw,omitempty"`
}
