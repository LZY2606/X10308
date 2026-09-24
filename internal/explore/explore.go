// Package explore runs bounded state-space exploration over a pnnet.Net.
//
// Rules (kept here explicitly and asserted by tests):
//
//   - enablement jointly considers input arcs, inhibitor arcs and capacities
//     (delegated to pnnet.Net.Enabled);
//   - states are canonicalized by the full token vector, so markings that
//     differ in any token count are never merged;
//   - when the truncation budget is exhausted before the question is settled,
//     the verdict is Unknown and the unexpanded frontier is retained.
package explore

import (
	"sort"

	"pnwork/internal/pnnet"
)

// Strategy selects the order in which discovered states are expanded.
type Strategy string

const (
	StrategyBFS Strategy = "bfs"
	StrategyDFS Strategy = "dfs"
)

// Config is the versioned exploration configuration. Budget counts expanded
// states; once Budget states have been expanded, further open states form the
// retained frontier.
type Config struct {
	Strategy Strategy `json:"strategy"`
	Budget   int64    `json:"budget"` // maximum expanded states; <= 0 means unlimited
}

// Step is one firing on a path.
type Step struct {
	Transition string        `json:"transition"`
	From       pnnet.Marking `json:"from"`
	To         pnnet.Marking `json:"to"`
}

// FrontierItem is an unexpanded discovered state.
type FrontierItem struct {
	Marking    pnnet.Marking `json:"marking"`
	Parent     string        `json:"parent"`
	Transition string        `json:"transition,omitempty"`
	Depth      int           `json:"depth"`
}

// Verdict of a bounded exploration question.
type Verdict string

const (
	VerdictReachable   Verdict = "reachable"
	VerdictUnreachable Verdict = "unreachable"
	VerdictUnknown     Verdict = "unknown" // budget exhausted
)

// Result is the machine-readable outcome of one exploration run.
type Result struct {
	Verdict        Verdict         `json:"verdict"`
	VisitedCount   int             `json:"visited_count"`
	ExpandedCount  int             `json:"expanded_count"`
	FrontierCount  int             `json:"frontier_count"`
	BudgetHit      bool            `json:"budget_hit"`
	Paths          [][]Step        `json:"paths,omitempty"`
	Frontier       []FrontierItem  `json:"frontier,omitempty"`
	CoveringStates []pnnet.Marking `json:"covering_states,omitempty"`
	Deadlocks      []Deadlock      `json:"deadlocks,omitempty"`
	TruncatedAt    *pnnet.Marking  `json:"truncated_at,omitempty"`
}

// Deadlock is a reached marking with no enabled transition.
type Deadlock struct {
	Marking pnnet.Marking                  `json:"marking"`
	Reasons map[string][]pnnet.BlockReason `json:"reasons"`
	Path    []Step                         `json:"path"`
}

type node struct {
	marking pnnet.Marking
	parent  string
	tid     string
	depth   int
}

// Engine performs bounded exploration.
type Engine struct {
	net      *pnnet.Net
	cfg      Config
	placeIDs []string
}

func NewEngine(n *pnnet.Net, cfg Config) *Engine {
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyBFS
	}
	return &Engine{net: n, cfg: cfg, placeIDs: n.SortedPlaceIDs()}
}

func (e *Engine) key(m pnnet.Marking) string { return m.Canonical(e.placeIDs) }

type queue struct {
	dfs   bool
	items []*node
}

func (q *queue) push(n *node) { q.items = append(q.items, n) }
func (q *queue) pop() *node {
	if q.dfs {
		last := len(q.items) - 1
		n := q.items[last]
		q.items = q.items[:last]
		return n
	}
	n := q.items[0]
	q.items = q.items[1:]
	return n
}
func (q *queue) len() int { return len(q.items) }

// Reach explores up to budget to decide whether target is reachable from
// initial. It records the shortest paths (all shortest witness paths, so a
// nondeterministic net keeps multiple legal candidates instead of collapsing
// to one arbitrarily).
func (e *Engine) Reach(initial, target pnnet.Marking) *Result {
	res := &Result{}
	q := &queue{dfs: e.cfg.Strategy == StrategyDFS}
	seen := map[string]*node{}
	parents := map[string][]*node{} // child key -> parents at discovery depth (BFS shortest)

	start := &node{marking: initial.Clone()}
	k0 := e.key(start.marking)
	seen[k0] = start
	q.push(start)
	res.VisitedCount = 1

	targetKey := e.key(target)
	var targetNode *node

	for q.len() > 0 {
		if e.cfg.Budget > 0 && int64(res.ExpandedCount) >= e.cfg.Budget {
			break
		}
		cur := q.pop()
		res.ExpandedCount++

		enabled := e.net.EnabledTransitions(cur.marking)
		// Deterministic ordering for stable ids/replay: EnabledTransitions
		// already returns sorted ids; concurrent transitions stay distinct
		// successors, never interleaved into one state.
		for _, tid := range enabled {
			next, err := e.net.Fire(tid, cur.marking)
			if err != nil {
				continue
			}
			nk := e.key(next)
			if existing, ok := seen[nk]; ok {
				// Never enqueue an already-seen state again: self loops and
				// merge edges would otherwise flood the DFS stack and starve
				// genuine successors. Token-different markings have different
				// keys, so this never merges distinct states.
				if existing.depth == cur.depth+1 {
					parents[nk] = append(parents[nk], cur)
				}
				continue
			}
			nn := &node{marking: next, parent: e.key(cur.marking), tid: tid, depth: cur.depth + 1}
			seen[nk] = nn
			parents[nk] = append(parents[nk], cur)
			q.push(nn)
			res.VisitedCount++
			if nk == targetKey && targetNode == nil {
				targetNode = nn
			}
		}
		if e.key(cur.marking) == targetKey && targetNode == nil {
			targetNode = cur
		}
		if targetNode != nil {
			// Continue consuming nothing else; BFS guarantees shortest depth.
		}
	}

	if targetNode != nil {
		res.Verdict = VerdictReachable
		res.Paths = e.allShortestPaths(targetNode, seen, parents, targetKey)
	} else if e.cfg.Budget > 0 && int64(res.ExpandedCount) >= e.cfg.Budget && q.len() > 0 {
		e.drainUnknown(q, res)
	} else {
		res.Verdict = VerdictUnreachable
	}
	return res
}

func (e *Engine) drainUnknown(q *queue, res *Result) {
	res.Verdict = VerdictUnknown
	res.BudgetHit = true
	for q.len() > 0 {
		f := q.pop()
		res.Frontier = append(res.Frontier, FrontierItem{
			Marking: f.marking.Clone(), Parent: f.parent,
			Transition: f.tid, Depth: f.depth,
		})
	}
	sort.Slice(res.Frontier, func(i, j int) bool {
		return e.key(res.Frontier[i].Marking) < e.key(res.Frontier[j].Marking)
	})
	res.FrontierCount = len(res.Frontier)
	if len(res.Frontier) > 0 {
		t := res.Frontier[0].Marking.Clone()
		res.TruncatedAt = &t
	}
}

func (e *Engine) allShortestPaths(tn *node, seen map[string]*node, parents map[string][]*node, targetKey string) [][]Step {
	// Rebuild every shortest path by walking recorded parents whose depth is
	// exactly one less at each step.
	var walk func(k string) [][]Step
	walk = func(k string) [][]Step {
		n := seen[k]
		if n == nil || n.parent == "" {
			return [][]Step{nil}
		}
		var out [][]Step
		for _, p := range parents[k] {
			if p.depth != n.depth-1 {
				continue
			}
			for _, sub := range walk(e.key(p.marking)) {
				path := append([]Step{}, sub...)
				path = append(path, Step{Transition: n.tid, From: p.marking.Clone(), To: n.marking.Clone()})
				out = append(out, path)
			}
		}
		return out
	}
	paths := walk(targetKey)
	sort.Slice(paths, func(i, j int) bool {
		return pathKey(paths[i]) < pathKey(paths[j])
	})
	return paths
}

func pathKey(p []Step) string {
	s := ""
	for _, st := range p {
		s += st.Transition + ";"
	}
	return s
}

// StateSpace enumerates the reachable state space under budget, additionally
// collecting cover relations for goal and deadlock candidates. Every
// discovered state is returned via callback; exploration remains deterministic.
type StateInfo struct {
	Marking pnnet.Marking
	Parent  string
	TID     string
	Depth   int
}

// SpaceResult is a full bounded enumeration.
type SpaceResult struct {
	States        []StateInfo    `json:"-"`
	ExpandedCount int            `json:"expanded_count"`
	VisitedCount  int            `json:"visited_count"`
	BudgetHit     bool           `json:"budget_hit"`
	Frontier      []FrontierItem `json:"frontier"`
	Deadlocks     []Deadlock     `json:"deadlocks"`
	Edges         []SpaceEdge    `json:"edges"`
}

// SpaceEdge is one reachable firing in the explored part of the space.
type SpaceEdge struct {
	From       pnnet.Marking `json:"from"`
	To         pnnet.Marking `json:"to"`
	Transition string        `json:"transition"`
}

// Enumerate runs the bounded state-space walk and gathers edges, deadlocks and
// cover witnesses against the optional goal markings.
func (e *Engine) Enumerate(initial pnnet.Marking, goals []pnnet.Marking) (*SpaceResult, map[string][]pnnet.Marking) {
	res := &SpaceResult{}
	q := &queue{dfs: e.cfg.Strategy == StrategyDFS}
	seen := map[string]*node{}
	pathOf := map[string][]Step{}
	start := &node{marking: initial.Clone()}
	k0 := e.key(start.marking)
	seen[k0] = start
	pathOf[k0] = nil
	q.push(start)
	res.VisitedCount = 1

	cover := map[string][]pnnet.Marking{}
	goalKeys := make([]string, len(goals))
	for i, g := range goals {
		goalKeys[i] = e.key(g)
	}
	addCover := func(m pnnet.Marking) {
		for i, g := range goals {
			if m.Covers(g) {
				cover[goalKeys[i]] = append(cover[goalKeys[i]], m.Clone())
			}
		}
	}
	addCover(initial)

	for q.len() > 0 {
		if e.cfg.Budget > 0 && int64(res.ExpandedCount) >= e.cfg.Budget {
			break
		}
		cur := q.pop()
		res.ExpandedCount++
		ck := e.key(cur.marking)
		res.States = append(res.States, StateInfo{
			Marking: cur.marking.Clone(), Parent: cur.parent, TID: cur.tid, Depth: cur.depth,
		})
		if dl := e.net.DeadlockInfo(cur.marking); dl != nil {
			res.Deadlocks = append(res.Deadlocks, Deadlock{
				Marking: cur.marking.Clone(), Reasons: dl, Path: append([]Step{}, pathOf[ck]...),
			})
		}
		for _, tid := range e.net.EnabledTransitions(cur.marking) {
			next, err := e.net.Fire(tid, cur.marking)
			if err != nil {
				continue
			}
			res.Edges = append(res.Edges, SpaceEdge{
				From: cur.marking.Clone(), To: next.Clone(), Transition: tid,
			})
			nk := e.key(next)
			if _, ok := seen[nk]; !ok {
				nn := &node{marking: next, parent: ck, tid: tid, depth: cur.depth + 1}
				seen[nk] = nn
				p := append([]Step{}, pathOf[ck]...)
				p = append(p, Step{Transition: tid, From: cur.marking.Clone(), To: next.Clone()})
				pathOf[nk] = p
				q.push(nn)
				res.VisitedCount++
				addCover(next)
			}
		}
	}
	sort.Slice(res.Deadlocks, func(i, j int) bool {
		return e.key(res.Deadlocks[i].Marking) < e.key(res.Deadlocks[j].Marking)
	})
	if e.cfg.Budget > 0 && int64(res.ExpandedCount) >= e.cfg.Budget && q.len() > 0 {
		res.BudgetHit = true
		for q.len() > 0 {
			f := q.pop()
			res.Frontier = append(res.Frontier, FrontierItem{
				Marking: f.marking.Clone(), Parent: f.parent,
				Transition: f.tid, Depth: f.depth,
			})
		}
		sort.Slice(res.Frontier, func(i, j int) bool {
			return e.key(res.Frontier[i].Marking) < e.key(res.Frontier[j].Marking)
		})
	}
	return res, cover
}
