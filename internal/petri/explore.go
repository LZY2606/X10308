package petri

// Exploration strategy names (versioned via the rules/parameters material).
const (
	StrategyBFS = "bfs"
)

// ExplorationStatus is reachable | truncated | finished.
type ExplorationStatus string

const (
	StatusReachable ExplorationStatus = "reachable"
	StatusDeadlock  ExplorationStatus = "deadlock"
	StatusFinished  ExplorationStatus = "finished"
	StatusTruncated ExplorationStatus = "truncated"
)

// State is one visited node of the state space.
type State struct {
	Seq       int      `json:"seq"`
	Key       string   `json:"key"`
	Marking   Marking  `json:"marking"`
	ParentSeq int      `json:"parent_seq"`
	FiredBy   string   `json:"fired_by,omitempty"`
	Deadlock  bool     `json:"deadlock"`
	Reasons   []BlockReason `json:"reasons,omitempty"`
}

// Edge is one fired transition between visited states.
type Edge struct {
	FromSeq int    `json:"from_seq"`
	ToSeq   int    `json:"to_seq"`
	TransID string `json:"transition_id"`
}

// FrontierItem is an unexpanded state kept when the budget is exhausted.
type FrontierItem struct {
	StateSeq int      `json:"state_seq"`
	TransID  string   `json:"transition_id"`
	Marking  Marking  `json:"marking"`
	Key      string   `json:"key"`
}

// Budget bounds state-space exploration. FireBudget counts transition
// firings; StateBudget caps visited states. Exhaustion yields
// status=truncated and the pending frontier is preserved.
type Budget struct {
	FireBudget  int64 `json:"fire_budget"`
	StateBudget int64 `json:"state_budget"`
}

// ExplorationResult is the output of Explore.
type ExplorationResult struct {
	Status         ExplorationStatus `json:"status"`
	States         []State           `json:"states"`
	Edges          []Edge            `json:"edges"`
	Frontier       []FrontierItem    `json:"frontier"`
	TargetSeq      int               `json:"target_seq,omitempty"`
	TargetReached  bool              `json:"target_reached"`
	DeadlockSeqs   []int             `json:"deadlock_seqs"`
	FiresUsed      int64             `json:"fires_used"`
	Concurrency    []ConcurrentPair  `json:"concurrency,omitempty"`
	TargetPath     []PathStep        `json:"target_path,omitempty"`
	DeadlockPaths  [][]PathStep      `json:"deadlock_paths,omitempty"`
	Budget         Budget            `json:"budget"`
}

// PathStep is one step of a reconstructed firing sequence.
type PathStep struct {
	StateSeq  int     `json:"state_seq"`
	TransID   string  `json:"transition_id"`
	NextSeq   int     `json:"next_seq"`
}

// ConcurrentPair is two independently enabled transitions that can fire in
// either order (witnessed interleaving) or commute to the same successor.
type ConcurrentPair struct {
	StateSeq int    `json:"state_seq"`
	T1       string `json:"t1"`
	T2       string `json:"t2"`
}

// Explore performs budget-bounded BFS. It never merges markings with
// different token counts: the canonical key distinguishes them. Budget
// exhaustion returns status "truncated" and preserves the frontier.
func (m *Model) Explore(initial Marking, target Marking, budget Budget) *ExplorationResult {
	res := &ExplorationResult{Budget: budget, TargetSeq: -1}
	keyOf := func(marking Marking) string { return m.Canonical(marking) }

	root := State{Seq: 0, Key: keyOf(initial), Marking: clone(initial), ParentSeq: -1}
	annotateDead(m, &root)
	res.States = append(res.States, root)
	index := map[string]int{root.Key: 0}

	type qitem struct {
		seq int
	}
	queue := []qitem{{0}}
	var fires int64

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cs := res.States[cur.seq]

		var enabled []string
		for _, t := range m.Transitions {
			if m.CheckEnablement(t.ID, cs.Marking).Enabled {
				enabled = append(enabled, t.ID)
			}
		}
		if len(enabled) >= 2 {
			res.Concurrency = append(res.Concurrency, ConcurrentPair{
				StateSeq: cur.seq, T1: enabled[0], T2: enabled[1],
			})
		}
		if cs.Deadlock {
			res.DeadlockSeqs = append(res.DeadlockSeqs, cur.seq)
		}
		for _, tID := range enabled {
			if budget.FireBudget > 0 && fires >= budget.FireBudget {
				res.Frontier = appendPending(res, m, cur.seq, tID, cs.Marking)
				continue
			}
			fires++
			next := m.Fire(tID, cs.Marking)
			nkey := keyOf(next)
			if seq, ok := index[nkey]; ok {
				res.Edges = append(res.Edges, Edge{FromSeq: cur.seq, ToSeq: seq, TransID: tID})
				continue
			}
			if budget.StateBudget > 0 && int64(len(res.States)) >= budget.StateBudget {
				res.Frontier = appendPendingKV(res, cur.seq, tID, next, nkey)
				continue
			}
			ns := State{
				Seq: len(res.States), Key: nkey, Marking: next,
				ParentSeq: cur.seq, FiredBy: tID,
			}
			annotateDead(m, &ns)
			res.States = append(res.States, ns)
			index[nkey] = ns.Seq
			res.Edges = append(res.Edges, Edge{FromSeq: cur.seq, ToSeq: ns.Seq, TransID: tID})
			queue = append(queue, qitem{ns.Seq})
		}
	}
	res.FiresUsed = fires

	if target != nil {
		tk := keyOf(target)
		if seq, ok := index[tk]; ok {
			res.TargetReached = true
			res.TargetSeq = seq
			res.TargetPath = pathTo(res, seq)
		}
	}
	for _, dseq := range res.DeadlockSeqs {
		res.DeadlockPaths = append(res.DeadlockPaths, pathTo(res, dseq))
	}
	switch {
	case res.TargetReached:
		res.Status = StatusReachable
	case len(res.Frontier) > 0:
		res.Status = StatusTruncated
	case len(res.DeadlockSeqs) > 0:
		res.Status = StatusDeadlock
	default:
		res.Status = StatusFinished
	}
	return res
}

func annotateDead(m *Model, s *State) {
	verdicts := m.EnabledTransitions(s.Marking)
	for _, v := range verdicts {
		if v.Enabled {
			return
		}
		s.Reasons = append(s.Reasons, v.Reasons...)
	}
	s.Deadlock = true
}

func appendPending(res *ExplorationResult, m *Model, seq int, tID string, marking Marking) []FrontierItem {
	next := m.Fire(tID, marking)
	return append(res.Frontier, FrontierItem{
		StateSeq: seq, TransID: tID, Marking: next, Key: m.Canonical(next),
	})
}

func appendPendingKV(res *ExplorationResult, seq int, tID string, next Marking, key string) []FrontierItem {
	return append(res.Frontier, FrontierItem{
		StateSeq: seq, TransID: tID, Marking: next, Key: key,
	})
}

func pathTo(res *ExplorationResult, seq int) []PathStep {
	var path []PathStep
	for seq > 0 {
		s := res.States[seq]
		path = append(path, PathStep{StateSeq: s.ParentSeq, TransID: s.FiredBy, NextSeq: seq})
		seq = s.ParentSeq
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func clone(m Marking) Marking {
	c := Marking{}
	for k, v := range m {
		c[k] = v
	}
	return c
}
