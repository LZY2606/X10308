package petri

import (
	"fmt"
	"sort"
	"strings"
)

// Strategy selects the frontier processing order.
type Strategy string

const (
	StrategyBFS Strategy = "bfs"
	StrategyDFS Strategy = "dfs"
)

// Config is the versioned exploration policy.
type Config struct {
	Strategy          Strategy `json:"strategy"`
	StateBudget       int      `json:"stateBudget"`
	MaxCandidatePaths int      `json:"maxCandidatePaths"`
	EngineRule        string   `json:"engineRule"`
}

// PathStep is one firing on a marking path.
type PathStep struct {
	TransitionID string `json:"transitionId"`
	FromStateID  string `json:"fromStateId"`
	ToStateID    string `json:"toStateId"`
}

// Path is a stable sequence of states and firings.
type Path struct {
	ID    string     `json:"id"`
	Steps []PathStep `json:"steps"`
}

// FrontierEntry preserves a pending state when the budget is exhausted.
type FrontierEntry struct {
	StateID      string `json:"stateId"`
	ParentID     string `json:"parentId"`
	TransitionID string `json:"transitionId"`
	Canonical    string `json:"canonical"`
	QueueOrder   int    `json:"queueOrder"`
}

// Candidate is one legal explanation kept when no unique answer exists.
type Candidate struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	PathID string `json:"pathId"`
	Detail string `json:"detail"`
}

// Finding records targets, deadlocks and unbounded components.
type Finding struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	Subject    string      `json:"subject"`
	Status     string      `json:"status"`
	StateID    string      `json:"stateId"`
	Candidates []Candidate `json:"candidates"`
}

// CoverRelation is a componentwise cover between two tree labels.
type CoverRelation struct {
	ID          string `json:"id"`
	CoveringID  string `json:"coveringId"`
	CoveredID   string `json:"coveredId"`
	Accelerated bool   `json:"accelerated"`
}

// Judgment is machine-readable evidence of a rule application.
type Judgment struct {
	ID           string       `json:"id"`
	Rule         string       `json:"rule"`
	StateID      string       `json:"stateId"`
	TransitionID string       `json:"transitionId,omitempty"`
	Outcome      string       `json:"outcome"`
	Reason       EnableReason `json:"reason,omitempty"`
	Detail       string       `json:"detail,omitempty"`
}

// Rule identifiers used as machine-readable judgment evidence.
const (
	RuleEnablement        = "petri.enablement.v1"
	RuleCanonicalization  = "petri.canonicalization.v1"
	RuleBudgetUnknown     = "petri.budget-unknown.v1"
	RuleCoverAcceleration = "petri.cover-acceleration.v1"
	RuleDeadlock          = "petri.deadlock.v1"
	RuleTargetReach       = "petri.target-reachability.v1"
	RuleImportRollback    = "ingest.rollback.v1"
)

// State is one coverability-tree node.
type State struct {
	ID        string       `json:"id"`
	ParentID  string       `json:"parentId,omitempty"`
	FiredBy   string       `json:"firedBy,omitempty"`
	Order     int          `json:"order"`
	Depth     int          `json:"depth"`
	Label     OmegaMarking `json:"label"`
	Canonical string       `json:"canonical"`
	Expanded  bool         `json:"expanded"`
}

// Edge connects a parent state to its successor state.
type Edge struct {
	ID           string `json:"id"`
	FromStateID  string `json:"fromStateId"`
	ToStateID    string `json:"toStateId"`
	TransitionID string `json:"transitionId"`
}

// Report is the fully deterministic result of one exploration run.
type Report struct {
	NetID       string          `json:"netId"`
	Config      Config          `json:"config"`
	States      []*State        `json:"states"`
	Edges       []Edge          `json:"edges"`
	Frontier    []FrontierEntry `json:"frontier"`
	Findings    []Finding       `json:"findings"`
	Covers      []CoverRelation `json:"covers"`
	Judgments   []Judgment      `json:"judgments"`
	Status      string          `json:"status"`
	RootStateID string          `json:"rootStateId"`

	byCanon map[string]*State
	parent  map[string]string // state -> parent state
	firedBy map[string]string // state -> transition id
}

// StateByID looks a state up.
func (r *Report) StateByID(id string) *State {
	for _, s := range r.States {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// PathTo reconstructs the firing path from root to state id.
func (r *Report) PathTo(id string) Path {
	var steps []PathStep
	cur := id
	for cur != r.RootStateID {
		p := r.parent[cur]
		if p == "" {
			break
		}
		steps = append(steps, PathStep{TransitionID: r.firedBy[cur], FromStateID: p, ToStateID: cur})
		cur = p
	}
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
	return Path{ID: "P-" + id, Steps: steps}
}
