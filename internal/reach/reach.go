// Package reach performs bounded state-space exploration and coverability
// analysis of Petri nets.
package reach

import (
	"fmt"

	"petriworkspace/internal/petrinet"
)

// Strategy selects the exploration order and caps resource use.
type Strategy struct {
	// Order is "bfs" or "dfs". BFS yields the shortest firing path.
	Order string `json:"order"`
	// StateBudget caps the number of newly expanded states. Zero means
	// unlimited. When the budget is consumed before the question is
	// settled the result status is Unknown and the open frontier is kept.
	StateBudget int `json:"state_budget"`
}

// Status is the verdict of an exploration run.
type Status string

const (
	StatusReachable   Status = "reachable"
	StatusUnreachable Status = "unreachable"
	StatusUnknown     Status = "unknown" // budget exhausted; frontier retained
	StatusFound       Status = "found"   // coverability / deadlock positive
	StatusNotFound    Status = "not_found"
)

// Deadlock records a marking where no transition is enabled, with the
// reason each candidate transition is blocked.
type Deadlock struct {
	Marking petrinet.Marking             `json:"marking"`
	Path    []Step                       `json:"path"`
	Reasons []petrinet.EnablementFailure `json:"reasons"`
}

// Step is one firing on a reconstructed path.
type Step struct {
	Transition string           `json:"transition"`
	From       petrinet.Marking `json:"from"`
	To         petrinet.Marking `json:"to"`
}

// FrontierItem is an unexpanded open state returned on budget exhaustion.
type FrontierItem struct {
	Marking petrinet.Marking `json:"marking"`
	PathLen int              `json:"path_len"`
}

// ReachResult is the output of bounded reachability exploration.
type ReachResult struct {
	Status    Status           `json:"status"`
	Target    petrinet.Marking `json:"target"`
	Path      []Step           `json:"path,omitempty"`
	Deadlocks []Deadlock       `json:"deadlocks,omitempty"`
	Frontier  []FrontierItem   `json:"frontier,omitempty"`
	Expanded  int              `json:"expanded"`
	Generated int              `json:"generated"`
	Budget    int              `json:"budget"`
}

type parentInfo struct {
	parent     string
	transition string
	marking    petrinet.Marking
}

// Explore searches the reachable state space from initial for the target
// marking and collects deadlock candidates. When StateBudget is exhausted
// before exhausting the graph it returns StatusUnknown with the open
// frontier preserved.
func Explore(net *petrinet.Net, initial, target petrinet.Marking, s Strategy) *ReachResult {
	order := s.Order
	if order != "dfs" {
		order = "bfs"
	}
	result := &ReachResult{Target: target, Budget: s.StateBudget}

	start := initial.Clone()
	startKey := start.Key()
	parents := map[string]parentInfo{startKey: {marking: start}}
	visited := map[string]petrinet.Marking{startKey: start}
	type frame struct {
		key string
		m   petrinet.Marking
	}
	open := []frame{{startKey, start}}
	result.Generated = 1

	expandNext := func() frame {
		if order == "dfs" {
			f := open[len(open)-1]
			open = open[:len(open)-1]
			return f
		}
		f := open[0]
		open = open[1:]
		return f
	}

	for len(open) > 0 {
		f := expandNext()
		result.Expanded++

		if target != nil && f.m.Equal(target) {
			result.Status = StatusReachable
			result.Path = reconstruct(parents, f.key)
			// Keep scanning for deadlocks too, as reviewers need both;
			// but do not exceed the budget while doing so.
		}

		enabled := net.EnabledTransitions(f.m)
		if len(enabled) == 0 {
			dl := Deadlock{
				Marking: f.m,
				Path:    reconstruct(parents, f.key),
				Reasons: net.DisabledTransitions(f.m),
			}
			result.Deadlocks = append(result.Deadlocks, dl)
		}

		successors := successorsInOrder(net, f.m, enabled)
		for _, succ := range successors {
			nextKey := succ.m.Key()
			if _, seen := visited[nextKey]; seen {
				continue
			}
			visited[nextKey] = succ.m
			parents[nextKey] = parentInfo{parent: f.key, transition: succ.t, marking: succ.m}
			open = append(open, frame{nextKey, succ.m})
			result.Generated++
		}

		if result.Status == "" && s.StateBudget > 0 && result.Expanded >= s.StateBudget {
			result.Status = StatusUnknown
			for _, o := range open {
				result.Frontier = append(result.Frontier, FrontierItem{Marking: o.m, PathLen: len(reconstruct(parents, o.key))})
			}
			return result
		}
	}

	if result.Status == StatusReachable {
		return result
	}
	if target == nil {
		result.Status = StatusNotFound
	} else {
		result.Status = StatusUnreachable
	}
	return result
}

type successor struct {
	t string
	m petrinet.Marking
}

func successorsInOrder(net *petrinet.Net, m petrinet.Marking, enabled []string) []successor {
	out := make([]successor, 0, len(enabled))
	for _, t := range enabled {
		next, err := net.Fire(t, m)
		if err != nil {
			continue
		}
		out = append(out, successor{t: t, m: next})
	}
	return out
}

func reconstruct(parents map[string]parentInfo, key string) []Step {
	var rev []Step
	cur := key
	for {
		pi, ok := parents[cur]
		if !ok {
			break
		}
		if pi.transition == "" {
			break
		}
		pp := parents[pi.parent]
		rev = append(rev, Step{Transition: pi.transition, From: pp.marking, To: pi.marking})
		cur = pi.parent
	}
	path := make([]Step, len(rev))
	for i := range rev {
		path[len(rev)-1-i] = rev[i]
	}
	return path
}

// ReachableOnly runs a quick reachability check and stops at the target.
func ReachableOnly(net *petrinet.Net, initial, target petrinet.Marking, s Strategy) *ReachResult {
	r := Explore(net, initial, target, s)
	if r.Status == StatusUnknown {
		return r
	}
	if r.Status == StatusReachable {
		r.Deadlocks = nil
		return r
	}
	r.Deadlocks = nil
	return r
}

// String renders a compact human summary for logs.
func (r *ReachResult) String() string {
	return fmt.Sprintf("status=%s expanded=%d generated=%d deadlocks=%d frontier=%d path=%d",
		r.Status, r.Expanded, r.Generated, len(r.Deadlocks), len(r.Frontier), len(r.Path))
}
