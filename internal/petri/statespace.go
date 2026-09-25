package petri

import (
	"sort"
)

// Strategy selects exploration order and truncation limits.
type Strategy struct {
	Kind             string `json:"kind"` // "bfs" or "dfs"
	StateBudget      int    `json:"state_budget"`
	FrontierBudget   int    `json:"frontier_budget"`
	MaxTokensPerPlace int64 `json:"max_tokens_per_place"` // guard against runaway unbounded growth
}

// State is a discovered reachable marking with provenance inside the run.
type State struct {
	Seq       int    `json:"seq"`
	Key       string `json:"key"`
	Marking   Marking `json:"marking"`
	ParentSeq int    `json:"parent_seq"` // -1 for the initial state
	FiredBy   string `json:"fired_by"`   // transition id from parent
	Depth     int    `json:"depth"`
}

// FiringEdge records one explored transition firing between two states.
type FiringEdge struct {
	FromSeq      int    `json:"from_seq"`
	ToSeq        int    `json:"to_seq"`
	TransitionID string `json:"transition_id"`
}

// FrontierItem is a retained pending expansion when the budget is exhausted.
type FrontierItem struct {
	StateSeq     int    `json:"state_seq"`
	TransitionID string `json:"transition_id"`
	NextKey      string `json:"next_key"`
	Reason       string `json:"reason"`
}

// Deadlock reports a state from which no transition is enabled.
type Deadlock struct {
	StateSeq int      `json:"state_seq"`
	Key      string   `json:"key"`
	Reasons  []string `json:"reasons"`
}

// Result is the outcome of a bounded reachability exploration.
type Result struct {
	Status        string        `json:"status"` // "complete" | "truncated"
	States        []*State      `json:"states"`
	Edges         []FiringEdge  `json:"edges"`
	Frontier      []FrontierItem `json:"frontier"`
	Deadlocks     []Deadlock    `json:"deadlocks"`
	StatesVisited int           `json:"states_visited"`
	Budget        int           `json:"budget"`
}

// Queue entry for traversal.
type qentry struct {
	seq int
}

// Explore performs a budgeted BFS/DFS over the reachable state space.
// When a budget is exhausted status is "truncated" and the unexpanded
// frontier is preserved verbatim; conclusions stay "unknown" for that run.
func (n *Net) Explore(idx *Index, initial Marking, strat Strategy) (*Result, error) {
	if strat.StateBudget <= 0 {
		strat.StateBudget = 1000
	}
	if strat.FrontierBudget <= 0 {
		strat.FrontierBudget = 4096
	}
	if strat.Kind == "" {
		strat.Kind = "bfs"
	}
	if strat.MaxTokensPerPlace <= 0 {
		strat.MaxTokensPerPlace = 1 << 40
	}
	res := &Result{Budget: strat.StateBudget, Status: "complete"}
	key0 := n.CanonicalKey(initial)
	stateByKey := map[string]*State{key0: {Seq: 0, Key: key0, Marking: initial.Clone(), ParentSeq: -1, FiredBy: "", Depth: 0}}
	res.States = append(res.States, stateByKey[key0])

	// Pending expansions: (state seq, transition id, successor key, marking).
	type job struct {
		fromSeq int
		tid     string
		key     string
		marking Marking
	}
	var pending []job
	enqueue := func(fromSeq int, m Marking) {
		for _, tid := range n.EnabledTransitions(idx, m) {
			next, err := n.Fire(idx, m, tid)
			if err != nil {
				continue
			}
			pending = append(pending, job{fromSeq: fromSeq, tid: tid, key: n.CanonicalKey(next), marking: next})
		}
	}
	enqueue(0, initial)

	for len(pending) > 0 {
		var cur job
		switch strat.Kind {
		case "dfs":
			cur = pending[len(pending)-1]
			pending = pending[:len(pending)-1]
		default:
			cur = pending[0]
			pending = pending[1:]
		}
		if len(res.Frontier) >= strat.FrontierBudget {
			res.Status = "truncated"
			res.Frontier = append(res.Frontier, FrontierItem{StateSeq: cur.fromSeq, TransitionID: cur.tid, NextKey: cur.key, Reason: "frontier-budget"})
			continue
		}
		if st, ok := stateByKey[cur.key]; ok {
			res.Edges = append(res.Edges, FiringEdge{FromSeq: cur.fromSeq, ToSeq: st.Seq, TransitionID: cur.tid})
			continue
		}
		if res.StatesVisited+1 >= strat.StateBudget {
			res.Status = "truncated"
			res.Frontier = append(res.Frontier, FrontierItem{StateSeq: cur.fromSeq, TransitionID: cur.tid, NextKey: cur.key, Reason: "state-budget"})
			continue
		}
		for p, v := range cur.marking {
			if v > strat.MaxTokensPerPlace {
				res.Status = "truncated"
				res.Frontier = append(res.Frontier, FrontierItem{StateSeq: cur.fromSeq, TransitionID: cur.tid, NextKey: cur.key, Reason: "token-growth-guard"})
				return res, nil
			}
			_ = p
		}
		parent := stateByKey[seqKey(res.States, cur.fromSeq)]
		st := &State{Seq: len(res.States), Key: cur.key, Marking: cur.marking.Clone(), ParentSeq: cur.fromSeq, FiredBy: cur.tid, Depth: parent.Depth + 1}
		stateByKey[cur.key] = st
		res.States = append(res.States, st)
		res.StatesVisited++
		res.Edges = append(res.Edges, FiringEdge{FromSeq: cur.fromSeq, ToSeq: st.Seq, TransitionID: cur.tid})
		enqueue(st.Seq, cur.marking)
	}

	// Deadlocks: explored states with no enabled transition.
	for _, st := range res.States {
		var reasons []string
		any := false
		for _, t := range n.Transitions {
			ok, why := n.Enabled(idx, st.Marking, t.ID)
			if ok {
				any = true
				break
			}
			reasons = append(reasons, t.ID+":"+why)
		}
		if !any {
			sort.Strings(reasons)
			res.Deadlocks = append(res.Deadlocks, Deadlock{StateSeq: st.Seq, Key: st.Key, Reasons: reasons})
		}
	}
	return res, nil
}

func seqKey(states []*State, seq int) string { return states[seq].Key }

// PathTo returns the firing path (transition ids) from the initial state to seq.
func (r *Result) PathTo(seq int) []string {
	var path []string
	for seq > 0 {
		st := r.States[seq]
		path = append(path, st.FiredBy)
		seq = st.ParentSeq
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// Reachable checks whether target appears among discovered states.
// Returns (found, known). known=false when the run is truncated and the
// target was not reached — the honest answer is then "unknown".
func (r *Result) Reachable(targetKey string) (bool, bool) {
	for _, st := range r.States {
		if st.Key == targetKey {
			return true, true
		}
	}
	if r.Status == "truncated" {
		return false, false
	}
	return false, true
}
