package petrinet

// This file defines the domain model of a weighted Petri net with optional
// capacity bounds and inhibitor arcs. Every domain quantity carries a unit or
// coordinate basis. Imported representations are preserved verbatim in Raw;
// the Normalized layer is derived and must never overwrite the source value.

// Scalar is a token-count quantity with an explicit unit. Raw keeps the
// original textual representation supplied by the modelling team.
type Scalar struct {
	Value      int64  `json:"value"`
	Unit       string `json:"unit,omitempty"`
	Raw        string `json:"raw,omitempty"`
	ConvertOK  bool   `json:"convert_ok"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// Point is a coordinate basis (SVG user units) used for layout.
type Point struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Basis      string  `json:"basis,omitempty"`
	Raw        string  `json:"raw,omitempty"`
	ConvertOK  bool    `json:"convert_ok"`
	Diagnostic string  `json:"diagnostic,omitempty"`
}

type Place struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity Scalar `json:"capacity"` // Capacity.Value < 0 means unbounded.
	Pos      Point  `json:"pos"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pos  Point  `json:"pos"`
}

// Arc is a directed weighted connection. Kind is "normal" or "inhibitor".
// An inhibitor arc p -> t enables t only when the tokens of p are strictly
// below the arc weight. Self loops are expressed by equal Source and Target.
type Arc struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Weight Scalar `json:"weight"`
	Kind   string `json:"kind,omitempty"`
}

type Net struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Places      []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []Arc        `json:"arcs"`
}

// Marking maps a stable place ID to its token count. It is the canonical
// token vector: states with different vectors are never merged.
type Marking map[string]int64

// Model is one versioned interpretation of a source material: the graph,
// the initial marking, capacities live on places, and strategy + budget are
// versioned alongside so replay is deterministic.
type Model struct {
	Net            Net               `json:"net"`
	InitialMarking Marking           `json:"initial_marking"`
	Strategy       Strategy          `json:"strategy"`
	Budget         int64             `json:"budget"`
	ScenarioScope  []string          `json:"scenario_scope,omitempty"`
	Diagnostics    []ImportDiag      `json:"diagnostics,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

type Strategy struct {
	Kind          string `json:"kind"` // "bfs" or "bfs_coverability"
	Coverability  bool   `json:"coverability"`
	MaxOmegaSteps int64  `json:"max_omega_steps"`
}

type ImportDiag struct {
	ObjectID string `json:"object_id"`
	Field    string `json:"field"`
	Raw      string `json:"raw"`
	Reason   string `json:"reason"`
	Kept     bool   `json:"kept"` // the unconvertible value stays at its origin
}

// Scenario is a named boundary exercise (self loop, concurrency, ...).
type Scenario struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Features    []string `json:"features"`
	Scope       []string `json:"scope"`
	Marking     Marking  `json:"marking"`
	Targets     []string `json:"targets,omitempty"`
	Budget      int64    `json:"budget"`
	Cover       bool     `json:"cover"`
	ExpectNote  string   `json:"expect_note,omitempty"`
}

// CaseFile is the on-disk, human-authored counterexample package format.
type CaseFile struct {
	Schema       string          `json:"schema"`
	CaseID       string          `json:"case_id"`
	Title        string          `json:"title"`
	Domain       string          `json:"domain"`
	Revision     string          `json:"revision"`
	Strategy     Strategy        `json:"strategy"`
	Budget       int64           `json:"budget"`
	Net          Net             `json:"net"`
	Initial      Marking         `json:"initial_marking"`
	Targets      []string        `json:"targets,omitempty"`
	Scenarios    []Scenario      `json:"scenarios"`
	Narrative    []NarrativeStep `json:"narrative,omitempty"`
	SourceDigest string          `json:"source_digest,omitempty"`
}

type NarrativeStep struct {
	Order   int      `json:"order"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Detail  string   `json:"detail"`
	Feature []string `json:"features,omitempty"`
}
