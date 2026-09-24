package petri

import (
	"fmt"
	"strings"
)

// Strategy is the exploration order, versioned together with the budget.
type Strategy string

const (
	StrategyBFS Strategy = "bfs"
	StrategyDFS Strategy = "dfs"
)

// State is one reached marking. IDs are assigned in deterministic creation
// order (s0, s1, ...) so a replay of the same inputs yields the same IDs.
type State struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Marking  Marking `json:"marking"`
	Depth    int     `json:"depth"`
	Deadlock bool    `json:"deadlock"`
	// Reason lists, per transition, why it is disabled; only set for
	// deadlock states.
	Reason string `json:"reason,omitempty"`
}

// Edge is one fired transition between two states.
type Edge struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

// Cover records that Ancestor <= Descendant componentwise with at least one
// strict component (token growth along a path: evidence of unboundedness).
type Cover struct {
	Ancestor   string `json:"ancestor"`
	Descendant string `json:"descendant"`
}

// Result is the outcome of one exploration run.
type Result struct {
	Status   string   `json:"status"` // "complete" | "unknown"
	Strategy Strategy `json:"strategy"`
	Budget   int      `json:"budget"`
	States   []State  `json:"states"`
	Edges    []Edge   `json:"edges"`
	// Frontier holds the generated-but-unexpanded states, in order, when
	// the budget is exhausted. It is preserved, never dropped.
	Frontier []string `json:"frontier,omitempty"`
	Covers   []Cover  `json:"covers,omitempty"`
}

// Explore runs reachability from the net's initial marking.
//
// Budget counts expanded states. When the budget is exhausted the result
// status is "unknown" and the unexpanded queue is kept as the frontier.
// Canonicalization is by exact token vector: distinct markings are never
// merged, so "same graph, different marking" always yields distinct states.
func Explore(n *Net, strategy Strategy, budget int, rules Rules) *Result {
	res := &Result{Status: "complete", Strategy: strategy, Budget: budget}
	index := map[string]int{}
	parent := map[int]int{} // state index -> parent state index

	mkState := func(m Marking, depth int) int {
		key := n.Key(m)
		if i, ok := index[key]; ok {
			return i
		}
		i := len(res.States)
		res.States = append(res.States, State{
			ID:      fmt.Sprintf("s%d", i),
			Key:     key,
			Marking: m.Clone(),
			Depth:   depth,
		})
		index[key] = i
		return i
	}

	start := mkState(n.Initial, 0)
	parent[start] = -1
	queue := []int{start}
	expanded := 0

	for len(queue) > 0 {
		var cur int
		if strategy == StrategyDFS {
			cur = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			cur = queue[0]
			queue = queue[1:]
		}
		if expanded >= budget {
			// Budget exhausted: answer is unknown, keep the frontier.
			res.Status = "unknown"
			res.Frontier = append(res.Frontier, res.States[cur].ID)
			for _, q := range queue {
				res.Frontier = append(res.Frontier, res.States[q].ID)
			}
			break
		}
		expanded++
		st := &res.States[cur]
		var reasons []string
		fired := false
		for ti := range n.Transitions {
			t := &n.Transitions[ti]
			ok, why := n.Fireable(t, st.Marking, rules)
			if !ok {
				reasons = append(reasons, t.ID+"["+why+"]")
				continue
			}
			fired = true
			next := n.Fire(t, st.Marking)
			before := len(res.States)
			ni := mkState(next, st.Depth+1)
			if ni == before {
				// Newly created state: record parent and queue it once.
				parent[ni] = cur
				queue = append(queue, ni)
			}
			res.Edges = append(res.Edges, Edge{
				ID:         fmt.Sprintf("e%d", len(res.Edges)),
				From:       st.ID,
				To:         res.States[ni].ID,
				Transition: t.ID,
			})
		}
		if !fired {
			st.Deadlock = true
			st.Reason = strings.Join(reasons, "; ")
		}
	}

	// Cover relations: ancestor strictly covered by descendant on a path.
	for i := range res.States {
		for j := parent[i]; j >= 0; j = parent[j] {
			if n.Leq(res.States[j].Marking, res.States[i].Marking) &&
				!n.Equal(res.States[j].Marking, res.States[i].Marking) {
				res.Covers = append(res.Covers, Cover{
					Ancestor:   res.States[j].ID,
					Descendant: res.States[i].ID,
				})
			}
		}
	}
	return res
}

// StateByID finds a state by its stable ID.
func (r *Result) StateByID(id string) *State {
	for i := range r.States {
		if r.States[i].ID == id {
			return &r.States[i]
		}
	}
	return nil
}

// Path returns the transition sequence leading to state id, or nil.
func (r *Result) Path(stateID string) []string {
	// Reconstruct via edges: BFS from s0.
	prev := map[string]Edge{}
	adj := map[string][]Edge{}
	for _, e := range r.Edges {
		adj[e.From] = append(adj[e.From], e)
	}
	type item struct{ id string }
	queue := []item{{"s0"}}
	seen := map[string]bool{"s0": true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.id == stateID {
			var out []string
			for at := stateID; at != "s0"; {
				e := prev[at]
				out = append([]string{e.Transition}, out...)
				at = e.From
			}
			return out
		}
		for _, e := range adj[cur.id] {
			if !seen[e.To] {
				seen[e.To] = true
				prev[e.To] = e
				queue = append(queue, item{e.To})
			}
		}
	}
	return nil
}
