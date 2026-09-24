// Package analyze implements Petri-net reachability / coverability state-space
// exploration. The rules encoded here (versioned by RulesVersion) are:
//
//   - Transition enablement simultaneously requires (1) every weighted normal
//     input arc has enough tokens, (2) every inhibitor arc is satisfied
//     (place tokens < inhibition weight), and (3) firing respects every place
//     capacity after the delta is applied.
//   - State canonicalization never merges markings whose token counts differ.
//   - When the exploration budget is exhausted the result is "unknown" and the
//     unvisited frontier is retained rather than reported as unreachable.
package analyze

import (
	"fmt"
	"sort"

	"petriws/internal/domain"
)

// Omega denotes an unbounded token count in coverability markings.
const Omega int64 = -1

// ResultStatus values.
const (
	StatusComplete = "complete" // budget sufficient, state space fully explored
	StatusUnknown  = "unknown"  // budget exhausted; frontier retained
)

// FiredStep references one transition firing between two states.
type FiredStep struct {
	Transition string `json:"transition"` // stable transition id
	Enabled    bool   `json:"enabled"`
	ArcID      string `json:"arc_id,omitempty"`
}

// State is a visited marking with stable identity assigned by the store layer.
// Seq is discovery order (dense, stable within a run).
type State struct {
	Key      string         `json:"key"`
	Marking  map[string]int64 `json:"marking"`
	Seq      int            `json:"seq"`
	IsOmega  bool           `json:"is_omega"`
	IsDead   bool           `json:"is_dead"`
	Parent   string         `json:"parent_key,omitempty"`
	ParentTr string         `json:"parent_transition,omitempty"`
}

// Edge is a firing between two visited states.
type Edge struct {
	From       string `json:"from_key"`
	To         string `json:"to_key"`
	Transition string `json:"transition"`
}

// FrontierItem is a not-yet-expanded pending state.
type FrontierItem struct {
	Key      string           `json:"key"`
	Marking  map[string]int64 `json:"marking"`
	Parent   string           `json:"parent_key"`
	ParentTr string           `json:"parent_transition"`
}

// Deadlock records why a state is deadlocked.
type Deadlock struct {
	StateKey string         `json:"state_key"`
	Marking  map[string]int64 `json:"marking"`
	Reasons  []BlockReason  `json:"reasons"`
}

// BlockReason explains why one transition cannot fire at a deadlocked state.
type BlockReason struct {
	Transition string      `json:"transition"`
	Inputs     []Need      `json:"input_deficits,omitempty"`
	Inhibitors []Inhibit   `json:"inhibitor_blocks,omitempty"`
	Capacity   []CapBlock  `json:"capacity_blocks,omitempty"`
}

// Need is a missing-token input requirement.
type Need struct {
	Place    string `json:"place"`
	Have     int64  `json:"have"`
	Need     int64  `json:"need"`
	ArcID    string `json:"arc_id"`
	Weight   int64  `json:"weight"`
}

// Inhibit is a failed inhibitor condition.
type Inhibit struct {
	Place  string `json:"place"`
	Have   int64  `json:"have"`
	Limit  int64  `json:"limit"` // tokens must be strictly below weight
	ArcID  string `json:"arc_id"`
}

// CapBlock is a capacity violation after firing.
type CapBlock struct {
	Place    string `json:"place"`
	Result   int64  `json:"result_tokens"`
	Capacity int64  `json:"capacity"`
}

// TargetResult captures reachability of a goal marking.
type TargetResult struct {
	Target   map[string]int64 `json:"target"`
	Reachable string          `json:"reachable"` // yes | no | unknown
	PathKeys []string         `json:"path_keys"`
	PathTrs  []string         `json:"path_transitions"`
}

// Result is the full exploration output (domain identities only).
type Result struct {
	Mode        string            `json:"mode"`
	Status      string            `json:"status"`
	States      []*State          `json:"states"`
	Edges       []Edge            `json:"edges"`
	Frontier    []FrontierItem    `json:"frontier"`
	Deadlocks   []Deadlock        `json:"deadlocks"`
	Targets     []TargetResult    `json:"targets"`
	MaxedStates int64             `json:"states_visited"`
	BudgetState int64             `json:"budget_states"`
	RuleVersion string            `json:"rule_version"`
}

// Net is a normalized, admissible representation used during exploration.
type Net struct {
	spec        *domain.NetSpec
	places      []string
	placeIdx    map[string]int
	capacity    []int64 // Omega => infinite
	preset      map[string][]arcRef // transition -> incoming normal/inhib
	postset     map[string][]arcRef // transition -> outgoing normal
	transitions []string
}

type arcRef struct {
	place  string
	weight int64
	arcID  string
	kind   domain.ArcKind
}
