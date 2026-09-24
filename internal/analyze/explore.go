package analyze

import (
	"sort"

	"petriws/internal/domain"
)

const (
	ModeReachability = "reachability"
	ModeCoverability = "coverability"
)

// Options controls one exploration run.
type Options struct {
	Mode        string
	MaxStates   int64
	MaxFrontier int64
	Targets     []map[string]int64 // goal markings expressed on place stable ids
}

// Explore runs bounded state-space construction from the initial marking.
func Explore(spec *domain.NetSpec, opts Options) (*Result, error) {
	n, err := Build(spec)
	if err != nil {
		return nil, err
	}
	if opts.Mode == "" {
		opts.Mode = ModeReachability
	}
	res := &Result{
		Mode:        opts.Mode,
		BudgetState: opts.MaxStates,
		RuleVersion: spec.RuleVersion,
	}

	start := n.initialMarking()
	startKey := key(start, n.places)
	visited := map[string]*State{startKey: {
		Key: startKey, Marking: copyM(start), Seq: 0,
	}}
	expanded := map[string]bool{}
	states := []*State{visited[startKey]}
	edgeSet := map[string]bool{}
	addEdge := func(e Edge) {
		k := e.From + "->" + e.To + "/" + e.Transition
		if !edgeSet[k] {
			edgeSet[k] = true
			res.Edges = append(res.Edges, e)
		}
	}

	type qitem struct {
		m      map[string]int64
		parent string
		pTr    string
	}
	queue := []qitem{{m: start}}
	trSorted := append([]string{}, n.transitions...)
	sort.Strings(trSorted)

	var frontier []FrontierItem
	status := StatusComplete

	deadlockAt := func(m map[string]int64, k string) {
		d := Deadlock{StateKey: k, Marking: copyM(m)}
		anyEnabled := false
		for _, t := range trSorted {
			ok, br := n.enabled(t, m)
			if ok {
				anyEnabled = true
				continue
			}
			d.Reasons = append(d.Reasons, br)
		}
		if !anyEnabled {
			st := visited[k]
			st.IsDead = true
			res.Deadlocks = append(res.Deadlocks, d)
		}
	}

	for len(queue) > 0 {
		// Budget is measured on state expansions. Once the visited-state
		// allowance is spent, the current node is still expanded so its newly
		// discovered successors are recorded; they then join the frontier.
		cur := queue[0]
		queue = queue[1:]
		curKey := key(cur.m, n.places)
		overBudget := int64(len(states)) >= opts.MaxStates && opts.MaxStates > 0

		anyEnabled := false
		for _, t := range trSorted {
			ok, _ := n.enabled(t, cur.m)
			if !ok {
				continue
			}
			anyEnabled = true
			suc := n.fire(t, cur.m)

			if opts.Mode == ModeCoverability {
				if dom, anKey := n.dominatedByAncestor(suc, curKey, visited); dom {
					suc = n.accelerate(suc, visited[anKey].Marking)
				}
			}

			sKey := key(suc, n.places)
			addEdge(Edge{From: curKey, To: sKey, Transition: t})
			if _, seen := visited[sKey]; seen {
				continue
			}
			st := &State{
				Key: sKey, Marking: copyM(suc), Seq: len(states),
				Parent: curKey, ParentTr: t,
			}
			if hasOmega(suc) {
				st.IsOmega = true
			}
			visited[sKey] = st
			states = append(states, st)
			queue = append(queue, qitem{m: suc, parent: curKey, pTr: t})
		}
		expanded[curKey] = true
		if overBudget {
			// Budget exhausted: every discovered-but-unexpanded state is a
			// retained frontier item; result is unknown, never "unreachable".
			for _, st := range states {
				if !expanded[st.Key] {
					frontier = append(frontier, FrontierItem{
						Key: st.Key, Marking: copyM(st.Marking),
						Parent: st.Parent, ParentTr: st.ParentTr,
					})
				}
			}
			sort.Slice(frontier, func(i, j int) bool { return frontier[i].Key < frontier[j].Key })
			status = StatusUnknown
			break
		}
		if !anyEnabled {
			deadlockAt(cur.m, curKey)
		}
	}

	res.States = states
	res.MaxedStates = int64(len(states))
	res.Status = status
	if len(frontier) > opts.MaxFrontier && opts.MaxFrontier > 0 {
		frontier = frontier[:opts.MaxFrontier]
	}
	res.Frontier = frontier

	for _, goal := range opts.Targets {
		res.Targets = append(res.Targets, n.evaluateTarget(goal, visited, status))
	}
	return res, nil
}

// dominatedByAncestor reports whether candidate is component-wise <= an
// ancestor marking a with at least one strict increase (a < candidate).
// Omega places participate consistently (omega dominates finite values).
func (n *Net) dominatedByAncestor(cand map[string]int64, parentKey string,
	visited map[string]*State) (bool, string) {
	k := parentKey
	for k != "" {
		a := visited[k]
		strict := false
		dominates := true
		for _, p := range n.places {
			av, cv := a.Marking[p], cand[p]
			if av == Omega {
				continue // omega already maximal
			}
			if cv == Omega {
				dominates = false
				break
			}
			if av > cv {
				dominates = false
				break
			}
			if av < cv {
				strict = true
			}
		}
		if dominates && strict {
			return true, k
		}
		k = a.Parent
	}
	return false, ""
}

// accelerate promotes to omega every place that strictly increased relative
// to the dominating ancestor.
func (n *Net) accelerate(cand, anc map[string]int64) map[string]int64 {
	out := copyM(cand)
	for _, p := range n.places {
		if anc[p] != Omega && out[p] != Omega && anc[p] < out[p] {
			out[p] = Omega
		}
	}
	return out
}

// evaluateTarget determines goal reachability and reconstructs candidate
// paths. On truncation it is unknown; otherwise yes/no.
func (n *Net) evaluateTarget(goal map[string]int64, visited map[string]*State,
	status string) TargetResult {
	// Normalize goal onto the full place vector (unspecified places = 0).
	g := map[string]int64{}
	for _, p := range n.places {
		g[p] = goal[p]
	}
	tr := TargetResult{Target: copyM(g), Reachable: "no"}
	gKey := key(g, n.places)
	st, ok := visited[gKey]
	if !ok {
		if status == StatusUnknown {
			tr.Reachable = "unknown"
		}
		return tr
	}
	tr.Reachable = "yes"
	keys, trs := reconstruct(st, visited)
	tr.PathKeys = keys
	tr.PathTrs = trs
	return tr
}

func reconstruct(st *State, visited map[string]*State) ([]string, []string) {
	var keysRev []string
	var trsRev []string
	cur := st
	for cur != nil {
		keysRev = append(keysRev, cur.Key)
		if cur.ParentTr != "" {
			trsRev = append(trsRev, cur.ParentTr)
		}
		if cur.Parent == "" {
			break
		}
		cur = visited[cur.Parent]
	}
	keys := reverseKeys(keysRev)
	trs := reverseKeys(trsRev)
	return keys, trs
}

func reverseKeys(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func copyM(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func hasOmega(m map[string]int64) bool {
	for _, v := range m {
		if v == Omega {
			return true
		}
	}
	return false
}
