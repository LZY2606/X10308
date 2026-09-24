// Package domain holds the core Petri-net model. Raw material (original textual
// representation, units, coordinates) is always preserved; normalization is
// performed only in derived layers and never mutates the source of record.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Kind identifies a node type.
type Kind string

const (
	KindPlace      Kind = "place"
	KindTransition Kind = "transition"
)

// Quantity is a token count exactly as authored, plus its normalized value
// when conversion succeeds. When conversion fails Value stays zero and a
// diagnostic is attached; the raw representation is never dropped.
type Quantity struct {
	Raw    string `json:"raw"`
	Unit   string `json:"unit"`
	Value  int64  `json:"value"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// Coord is an authored coordinate reference for layout (base: svg-user-units).
type Coord struct {
	X    string `json:"x"`
	Y    string `json:"y"`
	Base string `json:"base"`
}

// Node is a place or transition with a stable authored identity.
type Node struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Label     string    `json:"label"`
	Capacity  Quantity  `json:"capacity"` // places only; infinite when Valid=false && Raw==""
	Infinite  bool      `json:"infinite"`
	Init      Quantity  `json:"init"` // places only
	Coord     Coord     `json:"coord"`
	StableID  string    `json:"stable_id"` // identity carried through export/import
}

// ArcKind is normal (weighted) or inhibitor.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

// Arc is a weighted connection. Source/Target are stable node identities.
type Arc struct {
	ID       string   `json:"id"`
	Source   string   `json:"source"`
	Target   string   `json:"target"`
	Weight   Quantity `json:"weight"`
	Kind     ArcKind  `json:"kind"`
	StableID string   `json:"stable_id"`
}

// Diagnostic records an unconvertible or inconsistent authored value in place.
type Diagnostic struct {
	ObjectID string `json:"object_id"`
	Field    string `json:"field"`
	Raw      string `json:"raw"`
	Message  string `json:"message"`
}

// NetSpec is one interpreted version of a source. It is immutable once stored.
type NetSpec struct {
	SpecID      string       `json:"spec_id"`
	SourceID    string       `json:"source_id"`
	SourceVer   int          `json:"source_version"`
	RuleVersion string       `json:"rule_version"`
	Nodes       []Node       `json:"nodes"`
	Arcs        []Arc        `json:"arcs"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	RawSHA256   string       `json:"raw_sha256"`
}

// Strategy is a versioned exploration parameter set.
type Strategy struct {
	StrategyID string `json:"strategy_id"`
	Name       string `json:"name"`
	// mode: "reachability" (BFS, exact) or "coverability" (Karp-Miller, omega)
	Mode string `json:"mode"`
	// MaxStates bounds visited states; MaxFrontier bounds queued frontier.
	MaxStates   int64 `json:"max_states"`
	MaxFrontier int64 `json:"max_frontier"`
	RuleVersion string `json:"rule_version"`
}

// HashRaw returns the stable content hash of original bytes.
func HashRaw(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PlaceMap indexes places by stable id.
func (n *NetSpec) PlaceMap() map[string]*Node {
	m := map[string]*Node{}
	for i := range n.Nodes {
		if n.Nodes[i].Kind == KindPlace {
			m[n.Nodes[i].StableID] = &n.Nodes[i]
		}
	}
	return m
}

// TransitionMap indexes transitions by stable id.
func (n *NetSpec) TransitionMap() map[string]*Node {
	m := map[string]*Node{}
	for i := range n.Nodes {
		if n.Nodes[i].Kind == KindTransition {
			m[n.Nodes[i].StableID] = &n.Nodes[i]
		}
	}
	return m
}

// PlaceIDs returns place stable ids in authored order.
func (n *NetSpec) PlaceIDs() []string {
	var out []string
	for _, nd := range n.Nodes {
		if nd.Kind == KindPlace {
			out = append(out, nd.StableID)
		}
	}
	return out
}

// Marking maps place stable id -> token count. Token counts are part of state
// identity and must never be merged away.
type Marking map[string]int64

// CanonicalKey returns a stable serialization. Two markings with different
// token counts always produce different keys.
func (m Marking) CanonicalKey(places []string) string {
	b := make([]byte, 0, len(places)*4)
	for i, p := range places {
		if i > 0 {
			b = append(b, '|')
		}
		b = append(b, p...)
		b = append(b, ':')
		b = append(b, fmt.Sprintf("%d", m[p])...)
	}
	return string(b)
}

// SortedKeys is a helper for deterministic iteration.
func SortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
