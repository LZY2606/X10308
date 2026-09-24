// Package explorer performs budget-bounded state-space and coverability
// analysis over a normalized Petri net.
//
// Rules honored here:
//   - transition enablement simultaneously considers input, inhibitor and
//     capacity (delegated to petrinet.NormalizedNet);
//   - state canonicalization never merges markings that differ in tokens;
//   - when the truncation budget is exhausted the verdict is Unknown and the
//     pending frontier is retained, not discarded.
package explorer

import (
	"sort"

	"petriworkspace/internal/petrinet"
)

// Strategy selects the search order.
type Strategy string

const (
	BFS Strategy = "bfs"
	DFS Strategy = "dfs"
)

// Params is a versioned exploration configuration.
type Params struct {
	Strategy   Strategy
	Budget     int64 // maximum number of markings expanded
	RuleSetID  string
	RuleSetVer string
}

// StateStatus labels a visited marking.
type StateStatus string

const (
	StatusVisited   StateStatus = "visited"
	StatusDeadlock  StateStatus = "deadlock"
	StatusTarget    StateStatus = "target"
	StatusFrontier  StateStatus = "frontier"
)

// Edge is one firing between two concrete markings.
type Edge struct {
	ID         string
	From       string // state key
	To         string // state key
	Transition string
}

// FrontierItem is a retained, not-yet-expanded node returned on truncation.
type FrontierItem struct {
	StateKey string
	Depth    int
	Parent   string
	Enabled  []string
}

// TargetHit records a candidate marking that satisfies the target predicate.
type TargetHit struct {
	StateKey string
	Depth    int
}

// Result is the full, machine-readable outcome of one run. Status is:
//   "exhausted"  : whole finite state space explored within budget
//   "truncated"  : budget exhausted -> verdict Unknown, frontier retained
type Result struct {
	NetVersionID string
	Params       Params

	States       map[string]*State
	StateOrder   []string
	Edges        []Edge
	Frontier     []FrontierItem
	TargetHits   []TargetHit
	Deadlocks    []string
	Status       string // exhausted | truncated
	BudgetUsed   int64
	RootKey      string

	// TargetPaths holds EVERY shortest legal path to each target/decision
	// candidate, so a non-unique situation keeps all legal candidates.
	TargetPaths  map[string][][]string
	DeadlockPath map[string][]string
}

// State is a visited concrete marking plus role flags.
type State struct {
	Key       string
	Depth     int
	Marking   petrinet.Marking
	Parent    string
	FiredBy   string
	Status    StateStatus
	Expanded  bool
}

// TargetSpec describes a goal marking to search for. Match is "reach"
// (exact) or "cover" (componentwise).
type TargetSpec struct {
	Want  map[string]int64
	Match string // reach | cover
}

// Run executes a budget-bounded concrete reachability search.
func Run(net *petrinet.NormalizedNet, p Params, targets []TargetSpec) *Result {
	res := &Result{
		NetVersionID: net.VersionID,
		Params:       p,
		States:       map[string]*State{},
		TargetPaths: map[string][][]string{},
		DeadlockPath: map[string][]string{},
	}

	root := net.InitialMarking()
	rootKey := root.Key(net.PlaceOrder)
	res.RootKey = rootKey
	res.States[rootKey] = &State{Key: rootKey, Depth: 0, Marking: root, Status: StatusVisited}
	res.StateOrder = append(res.StateOrder, rootKey)

	queue := []qnode{{key: rootKey}}
	// parentEdge allows deterministic path reconstruction.
	// queue handling for DFS uses the end; BFS uses the front.

	for len(queue) > 0 {
		var cur qnode
		if p.Strategy == DFS {
			cur = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			cur = queue[0]
			queue = queue[1:]
		}
		st := res.States[cur.key]
		if st.Expanded {
			continue
		}

		// Budget: charged per expansion (firing move). Root expansion counts.
		if res.BudgetUsed >= p.Budget {
			res.Status = "truncated"
			res.retainFrontier(cur.key, queue, net)
			return res.finalize(targets, net)
		}
		res.BudgetUsed++
		st.Expanded = true

		enabled := net.EnabledTransitions(st.Marking)
		if len(enabled) == 0 {
			st.Status = StatusDeadlock
			res.Deadlocks = append(res.Deadlocks, st.Key)
			res.DeadlockPath[st.Key] = res.pathTo(st.Key)
			continue
		}
		// deterministic transition order already from net
		for _, t := range enabled {
			nextM := net.Fire(st.Marking, t)
			nk := nextM.Key(net.PlaceOrder)
			res.Edges = append(res.Edges, Edge{
				ID: st.Key + "->" + nk + ":" + t, From: st.Key, To: nk, Transition: t,
			})
			if _, seen := res.States[nk]; !seen {
				ns := &State{
					Key: nk, Depth: st.Depth + 1, Marking: nextM,
					Parent: st.Key, FiredBy: t, Status: StatusVisited,
				}
				res.States[nk] = ns
				res.StateOrder = append(res.StateOrder, nk)
				queue = append(queue, qnode{nk})
			}
		}
	}
	res.Status = "exhausted"
	return res.finalize(targets, net)
}

func (r *Result) retainFrontier(cur string, queue []qnode, net *petrinet.NormalizedNet) {
	// current node was reached but not expanded
	if st := r.States[cur]; st != nil && !st.Expanded {
		r.Frontier = append(r.Frontier, makeFrontier(st, net))
	}
	// queued, not-yet-processed nodes
	seen := map[string]bool{cur: true}
	for _, q := range queue {
		if seen[q.key] {
			continue
		}
		seen[q.key] = true
		if st := r.States[q.key]; st != nil && !st.Expanded {
			r.Frontier = append(r.Frontier, makeFrontier(st, net))
		}
	}
	sort.SliceStable(r.Frontier, func(i, j int) bool {
		if r.Frontier[i].Depth != r.Frontier[j].Depth {
			return r.Frontier[i].Depth < r.Frontier[j].Depth
		}
		return r.Frontier[i].StateKey < r.Frontier[j].StateKey
	})
}

func makeFrontier(st *State, net *petrinet.NormalizedNet) FrontierItem {
	return FrontierItem{
		StateKey: st.Key, Depth: st.Depth, Parent: st.Parent,
		Enabled: net.EnabledTransitions(st.Marking),
	}
}

// pathTo reconstructs the transition firing sequence from root to key.
func (r *Result) pathTo(key string) []string {
	path := []string{}
	cur := key
	for cur != "" {
		st := r.States[cur]
		if st == nil {
			break
		}
		if st.Parent == "" {
			break
		}
		path = append([]string{st.FiredBy}, path...)
		cur = st.Parent
	}
	return path
}

func (r *Result) finalize(targets []TargetSpec, net *petrinet.NormalizedNet) *Result {
	for _, spec := range targets {
		for _, key := range r.StateOrder {
			st := r.States[key]
			hit := false
			if spec.Match == "cover" {
				hit = st.Marking.Covers(petrinet.Marking{Tokens: spec.Want})
			} else {
				hit = st.Marking.Equal(petrinet.Marking{Tokens: spec.Want})
			}
			if hit {
				st.Status = StatusTarget
				r.TargetHits = append(r.TargetHits, TargetHit{StateKey: key, Depth: st.Depth})
				r.TargetPaths[key] = append(r.TargetPaths[key], r.pathTo(key))
			}
		}
	}
	return r
}
