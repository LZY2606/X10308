package petri

// Config controls one exploration run. Strategy and Budget are
// versioned together with the model they were applied to.
type Config struct {
	Strategy string `json:"strategy"` // currently "bfs"
	Budget   int    `json:"budget"`   // maximum number of states to materialize
}

// State is one node of the reachability graph. ID is stable within a
// Result and assigned in discovery order.
type State struct {
	ID      int     `json:"id"`
	Marking Marking `json:"marking"`
}

// Edge is a fired transition between two states.
type Edge struct {
	From       int    `json:"from"`
	To         int    `json:"to"`
	Transition string `json:"transition"`
}

// Deadlock records a state with no enabled transition and, for every
// transition, the precise enablement failure reasons.
type Deadlock struct {
	State   int                 `json:"state"`
	Reasons map[string][]string `json:"reasons"`
}

// CoverPair means States[A] strictly dominates States[B].
type CoverPair struct {
	Covering int `json:"covering"`
	Covered  int `json:"covered"`
}

// Result is the full outcome of an exploration. Status is "complete"
// when the whole state space was enumerated inside the budget, and
// "unknown" when the budget was exhausted; in that case Frontier holds
// the IDs of discovered-but-unexpanded states.
type Result struct {
	Status    string      `json:"status"` // "complete" | "unknown"
	States    []State     `json:"states"`
	Edges     []Edge      `json:"edges"`
	Parents   []int       `json:"parents"` // parent state id per state, -1 for root
	ParentTr  []string    `json:"parent_transitions"`
	Cover     []CoverPair `json:"cover"`
	Deadlocks []Deadlock  `json:"deadlocks"`
	Frontier  []int       `json:"frontier"`
	Unbounded []int       `json:"unbounded"` // states strictly covering an ancestor
	GoalHits  []int       `json:"goal_hits"`
}

// Explore enumerates the reachable state space of n under cfg.
// States are deduplicated by exact canonical marking only; markings
// with different token counts are never merged. When cfg.Budget is
// reached the run stops, reports Status "unknown" and preserves the
// unexpanded frontier.
func Explore(n *Net, cfg Config, goal Marking) *Result {
	res := &Result{Status: "complete", Parents: []int{-1}, ParentTr: []string{""}}
	seen := map[string]int{n.Canonical(n.Initial): 0}
	res.States = append(res.States, State{ID: 0, Marking: copyMarking(n.Initial)})
	if goalMatches(n.Initial, goal) {
		res.GoalHits = append(res.GoalHits, 0)
	}
	queue := []int{0}
	truncated := false

	for len(queue) > 0 && !truncated {
		id := queue[0]
		queue = queue[1:]
		m := res.States[id].Marking
		anyEnabled := false
		reasons := map[string][]string{}
		for _, t := range n.Transitions {
			ok, why := n.Enabled(t.ID, m)
			if !ok {
				reasons[t.ID] = why
				continue
			}
			anyEnabled = true
			post := n.Fire(t.ID, m)
			key := n.Canonical(post)
			nid, exists := seen[key]
			if !exists {
				if len(res.States) >= cfg.Budget {
					truncated = true
					break
				}
				nid = len(res.States)
				seen[key] = nid
				res.States = append(res.States, State{ID: nid, Marking: post})
				res.Parents = append(res.Parents, id)
				res.ParentTr = append(res.ParentTr, t.ID)
				queue = append(queue, nid)
				if goalMatches(post, goal) {
					res.GoalHits = append(res.GoalHits, nid)
				}
				if n.coversAncestor(res, nid) {
					res.Unbounded = append(res.Unbounded, nid)
				}
			}
			res.Edges = append(res.Edges, Edge{From: id, To: nid, Transition: t.ID})
		}
		if truncated {
			break
		}
		if !anyEnabled {
			res.Deadlocks = append(res.Deadlocks, Deadlock{State: id, Reasons: reasons})
		}
	}

	if truncated {
		res.Status = "unknown"
		res.Frontier = append(res.Frontier, queue...)
	}

	for i := range res.States {
		for j := range res.States {
			if i != j && Dominates(res.States[i].Marking, res.States[j].Marking, n.Places) {
				res.Cover = append(res.Cover, CoverPair{Covering: i, Covered: j})
			}
		}
	}
	return res
}

// coversAncestor reports whether the marking of state nid strictly
// dominates the marking of one of its ancestors on the discovery path,
// which witnesses unbounded token growth (omega).
func (n *Net) coversAncestor(res *Result, nid int) bool {
	m := res.States[nid].Marking
	for p := res.Parents[nid]; p >= 0; p = res.Parents[p] {
		if Dominates(m, res.States[p].Marking, n.Places) {
			return true
		}
	}
	return false
}

// Path returns the sequence of (transition, state) steps from the
// initial state to state id, for replaying a counterexample.
func (r *Result) Path(id int) []Edge {
	var rev []Edge
	for cur := id; cur > 0; cur = r.Parents[cur] {
		rev = append(rev, Edge{From: r.Parents[cur], To: cur, Transition: r.ParentTr[cur]})
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func goalMatches(m, goal Marking) bool {
	if len(goal) == 0 {
		return false
	}
	for p, v := range goal {
		if m[p] != v {
			return false
		}
	}
	return true
}

func copyMarking(m Marking) Marking {
	out := make(Marking, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
