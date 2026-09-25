// Package sourcemodel keeps the raw, byte-identical view of imported
// materials. The derived layer (internal/analysis) is the only place
// where normalization to integer tokens happens.
package sourcemodel

// Raw is an uninterpreted scalar that preserves the original textual
// representation alongside its unit/coordinate basis.
type Raw struct {
	Text   string `json:"text"`
	Unit   string `json:"unit,omitempty"`
	Basis  string `json:"basis,omitempty"` // e.g. "cartesian-px"
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// Node is a source-level node declaration. Identity is the imported ID;
// the importer never renames or merges nodes.
type Node struct {
	ID       string                 `json:"id"`
	Kind     string                 `json:"kind"` // place | transition
	Name     string                 `json:"name,omitempty"`
	Tokens   *Raw                   `json:"tokens,omitempty"`
	Capacity *Raw                   `json:"capacity,omitempty"`
	X        *Raw                   `json:"x,omitempty"`
	Y        *Raw                   `json:"y,omitempty"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
}

// Arc is a source-level weighted arc.
type Arc struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Weight   *Raw   `json:"weight,omitempty"`
	Kind     string `json:"kind,omitempty"` // normal | inhibitor
}

// Doc is one imported source document (net model or strategy/budget).
type Doc struct {
	ID       string                 `json:"id"`
	Kind     string                 `json:"kind"` // petrinet | strategy
	Revision string                 `json:"revision"`
	Title    string                 `json:"title,omitempty"`
	RawBytes []byte                 `json:"-"`
	RawSHA   string                 `json:"raw_sha"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
	Nodes    []Node                 `json:"nodes,omitempty"`
	Arcs     []Arc                  `json:"arcs,omitempty"`
}

// Diagnostic explains why a raw value could not be normalized. The
// raw value stays in place; unconvertible values are never dropped.
type Diagnostic struct {
	Severity string `json:"severity"` // error | warning
	Code     string `json:"code"`
	Message  string `json:"message"`
	ObjectID string `json:"object_id,omitempty"`
	Field    string `json:"field,omitempty"`
	Raw      *Raw   `json:"raw,omitempty"`
}
