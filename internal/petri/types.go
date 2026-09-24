// Package petri implements the domain engine: weighted Petri nets with
// place capacities, inhibitor arcs, reachability / coverability exploration
// and machine-readable rule events.
package petri

// NodeKind distinguishes places and transitions.
type NodeKind string

const (
	KindPlace      NodeKind = "place"
	KindTransition NodeKind = "transition"
)

// Node is a place or transition. Coord is the diagram coordinate basis (px)
// kept from the imported material; the derived layout layer never overwrites
// it.
type Node struct {
	ID    string    `json:"id"`
	Kind  NodeKind   `json:"kind"`
	Label string    `json:"label,omitempty"`
	X     *float64  `json:"x,omitempty"`
	Y     *float64  `json:"y,omitempty"`
}

// ArcInput is one imported arc. Weight/Capacity-style quantities retain their
// raw textual representation; Weight is normalized at the derived layer.
type ArcInput struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	WeightRaw string `json:"weight_raw"`
	Weight    int    `json:"weight"`
	Inhibitor bool   `json:"inhibitor,omitempty"`
}

// RawQty preserves an imported numeric representation. When the text cannot
// be normalized the value is left in place and the Diagnostic explains why
// (unbounded "∞" is a valid representation, not an error).
type RawQty struct {
	Raw        string  `json:"raw"`
	Value      *int    `json:"value"`
	Unbounded  bool    `json:"unbounded,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Diagnostic string  `json:"diagnostic,omitempty"`
}

// PlaceState carries initial tokens and the capacity constraint of a place.
type PlaceState struct {
	Place    string `json:"place"`
	Tokens   RawQty `json:"tokens"`
	Capacity RawQty `json:"capacity"`
}

// Alias maps an imported node identity to the canonical node id.
type Alias struct {
	Alias  string `json:"alias"`
	NodeID string `json:"node_id"`
}

// Strategy configures state-space exploration; both values are versioned in
// the imported material.
type Strategy struct {
	Name       string `json:"name"`        // "bfs" | "dfs"
	Budget     RawQty `json:"budget"`      // max new states
	MaxDepth   RawQty `json:"max_depth"`
	MaxPathLen RawQty `json:"max_path_len"`
}

// Diagnostic is attached to unconvertible values or structurally bad input.
type Diagnostic struct {
	Code     string `json:"code"`
	ObjectID string `json:"object_id,omitempty"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}

// Bundle is the versioned import material. Each section keeps its version
// string and the raw bytes are preserved by the storage layer.
type Bundle struct {
	BundleID     string      `json:"bundle_id"`
	ModelVersion string      `json:"model_version"`
	Model        ModelSection `json:"model"`
	Nodes        NodeSection `json:"nodes"`
	Arcs         ArcSection  `json:"arcs"`
	Tokens       TokenSection `json:"tokens"`
	Capacities   CapacitySection `json:"capacities"`
	Strategy     StrategySection `json:"strategy"`
	Budget       BudgetSection  `json:"budget"`
}

type ModelSection struct {
	Version  string `json:"version"`
	Name     string `json:"name"`
	Narrative string `json:"narrative,omitempty"`
}
type NodeSection struct {
	Version string `json:"version"`
	Nodes   []Node `json:"nodes"`
}
type ArcSection struct {
	Version string     `json:"version"`
	Arcs    []ArcInput `json:"arcs"`
}
type TokenSection struct {
	Version string       `json:"version"`
	Unit    string       `json:"unit"`
	Tokens  []PlaceState `json:"tokens"`
}
type CapacitySection struct {
	Version string       `json:"version"`
	Unit    string       `json:"unit"`
	States []PlaceState `json:"states"`
}
type StrategySection struct {
	Version string `json:"version"`
	Name    string `json:"name"`
}
type BudgetSection struct {
	Version string `json:"version"`
	States RawQty `json:"states"`
	Depth  RawQty `json:"depth"`
}

// Sections lists the seven versioned source sections in canonical order.
var Sections = []string{"model", "nodes", "arcs", "tokens", "capacities", "strategy", "budget"}

// Arc is a compiled, normalized arc.
type Arc struct {
	ID        string
	From      string
	To        string
	Weight    int
	Inhibitor bool
}

// PlaceInfo is the compiled per-place view.
type PlaceInfo struct {
	Node       Node
	Init       int
	HasInit    bool
	InitRaw    RawQty
	Capacity   *int
	Unbounded  bool
	CapRaw     RawQty
}

// Compiled is the normalized model used by the engine. Raw* fields retain the
// original representation for provenance; nothing unconvertible is dropped.
type Compiled struct {
	BundleID     string
	ModelVersion string
	Name         string
	Narrative    string
	SectionVer   map[string]string
	Places       map[string]*PlaceInfo
	Transitions  map[string]Node
	PlaceOrder   []string
	TransOrder   []string
	Arcs         []Arc
	ArcByID      map[string]Arc
	InputArc     map[string][]Arc // transition -> inputs / inhibitors
	OutputArc    map[string][]Arc // transition -> outputs
	Aliases      map[string]string
	Diagnostics  []Diagnostic
	Initial      map[string]int
	Blocked      bool // a hard structural error prevents exploration
}
