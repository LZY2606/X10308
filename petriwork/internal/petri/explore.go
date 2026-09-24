package petri

import "sort"

// Deadlock records a state with no enabled transition together with the
// enablement witnesses explaining why every transition is blocked.
type Deadlock struct {
	State   string          `json:"state"`
	Marking Marking         `json:"marking"`
	Path    []string        `json:"path"`
	Reasons []EnabledReason `json:"reasons"`
}

// CoverPair is a coverability witness: Upper strictly covers Lower along a
// reachable firing path, which certifies unbounded growth between them.
type CoverPair struct {
	Upper  Marking  `json:"upper"`
	Lower  Marking  `json:"lower"`
	Places []string `json:"places"` // places whose token count strictly grows
	Path   []string `json:"path"`   // path from lower to upper
}

// Candidate is one legal multi-candidate result: a reachable target marking
// may admit several firing paths and the analysis retains all of them.
type Candidate struct {
	Target Marking    `json:"target"`
	State  string     `json:"state"`
	Path   []string   `json:"path"`
	Length int        `json:"length"`
}

// Result is the full state-space analysis result.
type Result struct {
	Strategy Strategy `json:"strategy"`

	Initial Marking `json:"initial"`
	Target  Marking `json:"target,omitempty"`

	StatesReached  int64 `json:"states_reached"`
	StatesExpanded int64 `json:"states_expanded"`
	TransitionsFired int64 `json:"transitions_fired"`
	Budget         int64 `json:"budget"`
	MaxStates      int64 `json:"max_states"`

	// Status is "decided" (search exhausted) or "unknown" (truncated).
	Status string `json:"status"`
	// TruncatedReason is set when Status == "unknown": "budget" or "max_states".
	TruncatedReason string `json:"truncated_reason,omitempty"`

	Reachable bool `json:"reachable"`
	Coverable bool `json:"coverable"`
	Candidates []Candidate `json:"candidates,omitempty"`

	Deadlocks []Deadlock  `json:"deadlocks,omitempty"`
	Unbounded bool        `json:"unbounded"`
	CoverPairs []CoverPair `json:"cover_pairs,omitempty"`

	// Frontier holds the unexpanded states when the budget is exhausted;
	// returned-unknown results always preserve it for resumption/review.
	Frontier []FrontierState `json:"frontier"`

	// Edges is the explored state graph (parent -> transition -> child).
	Edges []Edge `json:"edges"`
}

// FrontierState is a reached-but-not-expanded state kept at truncation.
type FrontierState struct {
	State       string   `json:"state"`
	Marking     Marking  `json:"marking"`
	Parent      string   `json:"parent,omitempty"`
	Path        []string `json:"path"`
	ReachedBy   string   `json:"reached_by,omitempty"`
	Depth       int      `json:"depth"`
}

// Edge is one explored firing in the state graph.
type Edge struct {
	From   string  `json:"from"`
	Trans  string  `json:"trans"`
	To     string  `json:"to"`
	Marking Marking `json:"to_marking"`
}

type searchNode struct {
	key      string
	marking  Marking
	path     []string // transition ids fired from the initial state
	pathKeys []string // canonical state keys on the path, including this node
	depth    int
}

// Explore runs the state-space search.
//
// Reachability is decided by exact marking match only. Coverability is
// reported separately. Canonicalization never merges markings whose token
// counts differ. When the firing budget or the state cap is exhausted the
// status is "unknown", reachability is not asserted false, and the open
// frontier is preserved in the result.
func Explore(n *Net, initial, target Marking, opts Options) *Result {
	if opts.Strategy != StrategyBFS && opts.Strategy != StrategyDFS {
		opts.Strategy = StrategyBFS
	}
	if opts.Budget <= 0 {
		opts.Budget = DefaultOptions().Budget
	}
	if opts.MaxStates <= 0 {
		opts.MaxStates = DefaultOptions().MaxStates
	}
	if err := n.Validate(); err != nil {
		return &Result{Status: "invalid", Initial: initial}
	}
	placeIDs := n.SortedPlaceIDs()

	res := &Result{
		Strategy: opts.Strategy,
		Initial:  initial,
		Target:   target,
		Budget:   opts.Budget,
		MaxStates: opts.MaxStates,
	}

	startKey := Canonical(initial, placeIDs)
	visited := map[string]Marking{startKey: initial}
	pathOf := map[string][]string{startKey: nil}
	depthOf := map[string]int{startKey: 0}

	var queue []searchNode
	queue = append(queue, searchNode{key: startKey, marking: initial, pathKeys: []string{startKey}})

	// Order of states for stable edges/output: first-seen discovery order.
	discovery := []string{startKey}

	// If the initial marking itself is the target, record the zero-length path.
	if target != nil && initial.Equal(target) {
		res.Reachable = true
		res.Coverable = true
		res.Candidates = append(res.Candidates, Candidate{Target: target.Clone(), State: startKey, Path: nil, Length: 0})
	}

	truncated := ""

	for len(queue) > 0 {
		if res.StatesExpanded >= opts.MaxStates {
			truncated = "max_states"
			break
		}
		var cur searchNode
		if opts.Strategy == StrategyDFS {
			cur = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			cur = queue[0]
			queue = queue[1:]
		}
		res.StatesExpanded++

		succs := Successors(n, cur.marking)

		// Deadlock: no transition enabled; capture every reason.
		if len(succs) == 0 {
			dl := Deadlock{State: cur.key, Marking: cur.marking.Clone(), Path: clonePath(cur.path)}
			for _, t := range n.Transitions {
				dl.Reasons = append(dl.Reasons, Enabled(n, cur.marking, t.ID))
			}
			res.Deadlocks = append(res.Deadlocks, dl)
			continue
		}

		// Stable firing order: Successors is already transition-id sorted.
		for _, s := range succs {
			if res.TransitionsFired >= opts.Budget {
				truncated = "budget"
				break
			}
			res.TransitionsFired++
			child := s.Result
			ckey := Canonical(child, placeIDs)
			edge := Edge{From: cur.key, Trans: s.Trans, To: ckey, Marking: child.Clone()}
			res.Edges = append(res.Edges, edge)

			if prior, seen := visited[ckey]; !seen {
				visited[ckey] = child
				childPath := append(clonePath(cur.path), s.Trans)
				pathOf[ckey] = childPath
				depthOf[ckey] = cur.depth + 1
				childKeys := append(cloneKeys(cur.pathKeys), ckey)
				discovery = append(discovery, ckey)
				queue = append(queue, searchNode{key: ckey, marking: child, path: childPath, pathKeys: childKeys, depth: cur.depth + 1})
			}
			// Coverability / unbounded-growth witness (Karp-Miller pump):
			// only a state strictly covering an ANCESTOR on its own firing
			// path certifies a repeatable growth loop.
			for i := len(cur.pathKeys) - 1; i >= 0; i-- {
				ak := cur.pathKeys[i]
				anc := visited[ak]
				if child.StrictlyCovers(anc) {
					pairPath := append(clonePath(cur.path), s.Trans)
					res.CoverPairs = append(res.CoverPairs, makeCoverPair(child, anc, pairPath, placeIDs))
					res.Unbounded = true
					break
				}
			}

			if target != nil {
				if child.Equal(target) && !candidatePresent(res.Candidates, ckey) {
					p := append(clonePath(cur.path), s.Trans)
					res.Candidates = append(res.Candidates, Candidate{Target: target.Clone(), State: ckey, Path: p, Length: len(p)})
					res.Reachable = true
					res.Coverable = true
				} else if child.Covers(target) && !res.Coverable {
					res.Coverable = true
				}
			}
		}
		if truncated != "" {
			break
		}
	}

	res.StatesReached = int64(len(visited))

	// Deterministic ordering of cover pairs and deadlocks.
	sort.SliceStable(res.CoverPairs, func(i, j int) bool {
		return Canonical(res.CoverPairs[i].Lower, placeIDs) < Canonical(res.CoverPairs[j].Lower, placeIDs)
	})
	sort.SliceStable(res.Deadlocks, func(i, j int) bool { return res.Deadlocks[i].State < res.Deadlocks[j].State })

	if truncated != "" {
		// Budget exhausted: return unknown and preserve the open frontier.
		res.Status = "unknown"
		res.TruncatedReason = truncated
		res.Reachable = false // cannot assert reachability from truncated search
		res.Frontier = collectFrontier(queue)
		return res
	}
	res.Status = "decided"
	return res
}

func cloneKeys(p []string) []string {
	if p == nil {
		return nil
	}
	c := make([]string, len(p))
	copy(c, p)
	return c
}

func clonePath(p []string) []string {
	if p == nil {
		return nil
	}
	c := make([]string, len(p))
	copy(c, p)
	return c
}

func makeCoverPair(upper, lower Marking, path []string, placeIDs []string) CoverPair {
	cp := CoverPair{Upper: upper.Clone(), Lower: lower.Clone(), Path: clonePath(path)}
	for _, pid := range placeIDs {
		if upper[pid] > lower[pid] {
			cp.Places = append(cp.Places, pid)
		}
	}
	return cp
}

func candidatePresent(cs []Candidate, state string) bool {
	for _, c := range cs {
		if c.State == state {
			return true
		}
	}
	return false
}

func collectFrontier(queue []searchNode) []FrontierState {
	out := make([]FrontierState, 0, len(queue))
	for _, q := range queue {
		out = append(out, FrontierState{
			State:   q.key,
			Marking: q.marking.Clone(),
			Path:    clonePath(q.path),
			Depth:   q.depth,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].State < out[j].State })
	return out
}
