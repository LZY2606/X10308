package petri

import "fmt"

// Strategy is the versioned exploration configuration.
type Strategy struct {
	ID       string `json:"id"`
	Name     string `json:"name"` // "bfs" | "dfs"
	Budget   int    `json:"budget"`
	Version  int    `json:"version"`
	Revision string `json:"revision,omitempty"`
}

// State is one explored marking with a stable identity inside a run.
type State struct {
	ID       string                     `json:"id"`
	Key      string                     `json:"key"`
	Marking  Marking                    `json:"marking"`
	Depth    int                        `json:"depth"`
	Initial  bool                       `json:"initial,omitempty"`
	Deadlock bool                       `json:"deadlock,omitempty"`
	Reasons  map[string][]DisableReason `json:"reasons,omitempty"` // transition id -> reasons, for deadlocks
	Frontier bool                       `json:"frontier,omitempty"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

// Cover records that Coverer's marking strictly dominates Covered's in every
// place (the cover relation over explored states).
type Cover struct {
	Coverer string `json:"coverer"`
	Covered string `json:"covered"`
}

// Result is the full, persistable outcome of one exploration run.
type Result struct {
	Status    string   `json:"status"` // "complete" | "unknown_budget"
	States    []*State `json:"states"`
	Edges     []Edge   `json:"edges"`
	Covers    []Cover  `json:"covers"`
	Deadlocks []string `json:"deadlocks"` // state ids
	Frontier  []string `json:"frontier"`  // state ids, retained when budget is exhausted
}

// Explore runs the reachability exploration. When the budget is exhausted the
// result status is "unknown_budget" and the unexpanded frontier is retained
// in the result rather than discarded.
func Explore(model *Model, strat Strategy, runID string) *Result {
	res := &Result{Status: "complete"}
	seen := map[string]*State{}
	var queue []*State
	mk := model.InitialMarking()
	root := &State{
		ID:      fmt.Sprintf("%s#s0", runID),
		Key:     CanonicalKey(mk),
		Marking: mk,
		Initial: true,
	}
	seen[root.Key] = root
	queue = append(queue, root)
	res.States = append(res.States, root)

	seq := 1
	expanded := 0
	for len(queue) > 0 {
		if expanded >= strat.Budget {
			// budget exhausted: answer is unknown, keep the frontier
			res.Status = "unknown_budget"
			for _, s := range queue {
				s.Frontier = true
				res.Frontier = append(res.Frontier, s.ID)
			}
			break
		}
		var cur *State
		if strat.Name == "dfs" {
			cur = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			cur = queue[0]
			queue = queue[1:]
		}
		expanded++
		anyEnabled := false
		reasons := map[string][]DisableReason{}
		for _, t := range model.Transitions {
			ok, why := model.Enablement(t, cur.Marking)
			if !ok {
				reasons[t.ID] = why
				continue
			}
			anyEnabled = true
			next := model.Fire(t, cur.Marking)
			key := CanonicalKey(next)
			st, exists := seen[key]
			if !exists {
				st = &State{
					ID:      fmt.Sprintf("%s#s%d", runID, seq),
					Key:     key,
					Marking: next,
					Depth:   cur.Depth + 1,
				}
				seq++
				seen[key] = st
				res.States = append(res.States, st)
				queue = append(queue, st)
			}
			res.Edges = append(res.Edges, Edge{From: cur.ID, To: st.ID, Transition: t.ID})
		}
		if !anyEnabled {
			cur.Deadlock = true
			cur.Reasons = reasons
			res.Deadlocks = append(res.Deadlocks, cur.ID)
		}
	}
	res.Covers = computeCovers(res.States)
	return res
}

// computeCovers derives the cover relation over explored states: a covers b
// when a's marking is >= b's in every place and strictly greater somewhere.
func computeCovers(states []*State) []Cover {
	var covers []Cover
	for _, a := range states {
		for _, b := range states {
			if a == b {
				continue
			}
			if dominates(a.Marking, b.Marking) {
				covers = append(covers, Cover{Coverer: a.ID, Covered: b.ID})
			}
		}
	}
	return covers
}

func dominates(a, b Marking) bool {
	strict := false
	for pid, bv := range b {
		av := a[pid]
		if av < bv {
			return false
		}
		if av > bv {
			strict = true
		}
	}
	return strict
}
