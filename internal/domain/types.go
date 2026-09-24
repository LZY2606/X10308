// Package domain contains the Petri-net model types and the rule book used by
// the reachability/state-space workspace.
//
// Domain values are kept on two layers: the imported representation (raw
// strings with their unit/coordinate basis, preserved verbatim) and the
// derived normalized layer used by the exploration engine. A value that can
// not be normalized is never dropped: it stays on its node with a diagnostic.
package domain

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Rule versions. Every derived object records the rule book version that
// produced it, so rule changes compute a precise invalidation scope instead
// of silently replacing old conclusions.
const (
	EnablementRuleVersion = "enablement.v1"
	CanonicalRuleVersion  = "canonicalization.v1"
	BudgetRuleVersion     = "budget.v1"
)

// DocKind enumerates the seven versioned source documents.
type DocKind string

const (
	DocModel      DocKind = "model"
	DocIdentities DocKind = "identities"
	DocArcs       DocKind = "arcs"
	DocMarking    DocKind = "initial_marking"
	DocCapacity   DocKind = "capacity"
	DocStrategy   DocKind = "strategy"
	DocBudget     DocKind = "budget"
)

// AllDocKinds returns the seven versioned source kinds in stable order.
func AllDocKinds() []DocKind {
	return []DocKind{DocModel, DocIdentities, DocArcs, DocMarking, DocCapacity, DocStrategy, DocBudget}
}

// NodeKind distinguishes places and transitions.
type NodeKind string

const (
	Place      NodeKind = "place"
	Transition NodeKind = "transition"
)

// Scalar is an imported domain quantity. Raw, Unit and Basis are the imported
// representation; Value is the derived normalized integer. When Resolved is
// false the value is unusable for computation and Diagnostic explains why,
// but the raw representation is still retained in place.
type Scalar struct {
	Raw        string   `json:"raw"`
	Value      *big.Int `json:"value,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Resolved   bool     `json:"resolved"`
	Diagnostic string   `json:"diagnostic,omitempty"`
}

// Int64 returns the normalized value. Callers must check Resolved first.
func (s Scalar) Int64() (int64, error) {
	if !s.Resolved || s.Value == nil {
		return 0, fmt.Errorf("unresolved scalar %q: %s", s.Raw, s.Diagnostic)
	}
	if !s.Value.IsInt64() {
		return 0, fmt.Errorf("scalar %q does not fit int64", s.Raw)
	}
	return s.Value.Int64(), nil
}

// Coord is a node coordinate with its coordinate basis.
type Coord struct {
	RawX       string `json:"raw_x"`
	RawY       string `json:"raw_y"`
	Basis      string `json:"basis,omitempty"`
	X          int64  `json:"x"`
	Y          int64  `json:"y"`
	Resolved   bool   `json:"resolved"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// Node is a place or transition with its stable identity.
type Node struct {
	ID     string   `json:"id"`
	Kind   NodeKind `json:"kind"`
	Label  string   `json:"label,omitempty"`
	Coord  *Coord   `json:"coord,omitempty"`
	DocID  string   `json:"doc_id"`
	DocVer int64    `json:"doc_version"`
}

// ArcKind enumerates weighted flow arcs and inhibitor arcs.
type ArcKind string

const (
	ArcFlow      ArcKind = "flow"
	ArcInhibitor ArcKind = "inhibitor"
)

// Arc is a directed weighted arc (self loops allowed) or an inhibitor arc.
type Arc struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight Scalar  `json:"weight"`
	Kind   ArcKind `json:"kind"`
	DocID  string  `json:"doc_id"`
	DocVer int64   `json:"doc_version"`
}

// TokenEntry is one place/token pair from an initial/target marking.
type TokenEntry struct {
	PlaceID string `json:"place_id"`
	Tokens  Scalar `json:"tokens"`
}

// CapacityEntry is one place/capacity pair; a missing or unresolved entry
// means "unbounded" only for drawing, never for exploration.
type CapacityEntry struct {
	PlaceID string `json:"place_id"`
	Cap     Scalar `json:"cap"`
}

// Strategy selects the state-space exploration order.
type Strategy string

const (
	BFS Strategy = "bfs"
	DFS Strategy = "dfs"
)

// StrategySpec is the versioned exploration strategy.
type StrategySpec struct {
	Strategy Strategy `json:"strategy"`
	// TargetMarking is optional: its presence drives reachability/coverability
	// reporting; absence leaves pure deadlock/state-space exploration.
	TargetMarking []TokenEntry `json:"target_marking,omitempty"`
	DocID         string       `json:"doc_id"`
	DocVer        int64        `json:"doc_version"`
}

// BudgetSpec is the versioned truncation budget.
type BudgetSpec struct {
	MaxStates int64  `json:"max_states"`
	MaxSteps  int64  `json:"max_steps"`
	RawStates string `json:"raw_max_states,omitempty"`
	RawSteps  string `json:"raw_max_steps,omitempty"`
	Resolved  bool   `json:"resolved"`
	DocID     string `json:"doc_id"`
	DocVer    int64  `json:"doc_version"`
}

// Model is the normalized, derived view of a branch's source documents.
type Model struct {
	ModelID  string
	Nodes    []Node
	Arcs     []Arc
	Marking  []TokenEntry
	Capacity []CapacityEntry
	Strategy StrategySpec
	Budget   BudgetSpec
	DocID    map[DocKind]string
	DocVer   map[DocKind]int64
	// Diagnostics are unresolved-value / consistency problems. They never
	// discard data; hard problems block exploration.
	Diagnostics []Diagnostic
}

// Severity classifies an imported-value problem.
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Diagnostic stays attached to its object location.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Object   string   `json:"object"`
	Message  string   `json:"message"`
}

// NodeByID returns a node and its presence.
func (m *Model) NodeByID(id string) (Node, bool) {
	for _, n := range m.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Places returns the place nodes in declaration order.
func (m *Model) Places() []Node {
	out := make([]Node, 0)
	for _, n := range m.Nodes {
		if n.Kind == Place {
			out = append(out, n)
		}
	}
	return out
}

// Transitions returns the transition nodes in declaration order.
func (m *Model) Transitions() []Node {
	out := make([]Node, 0)
	for _, n := range m.Nodes {
		if n.Kind == Transition {
			out = append(out, n)
		}
	}
	return out
}

// Marking maps place id to token count (a normalized exact marking).
type Marking map[string]int64

// Canonical renders a marking to a stable string key. The place set is part
// of the identity: markings with different token counts never merge.
func (mk Marking) Canonical(places []string) string {
	ps := append([]string(nil), places...)
	sort.Strings(ps)
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(p)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%d", mk[p]))
	}
	return b.String()
}

// Clone returns a copy of the marking.
func (mk Marking) Clone() Marking {
	out := make(Marking, len(mk))
	for k, v := range mk {
		out[k] = v
	}
	return out
}
