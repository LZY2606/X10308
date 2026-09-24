// Package petri implements the domain engine for the token-flow net workspace:
// transition enablement (inputs + inhibitor arcs + capacities), state-space
// exploration under a truncation budget, coverability and deadlock analysis.
package petri

import (
	"fmt"
	"sort"
	"strings"
)

type ArcKind string

const (
	ArcIn        ArcKind = "in"
	ArcOut       ArcKind = "out"
	ArcInhibitor ArcKind = "inhibitor"
)

type Place struct {
	ID          string
	Capacity    int
	HasCapacity bool
	X           float64
	Y           float64
}

type Transition struct {
	ID string
	X  float64
	Y  float64
}

// Arc is a weighted arc. For inhibitor arcs Weight is the inhibitor threshold:
// the transition is enabled only when the input place holds fewer than Weight
// tokens (the classic empty-place inhibitor is Weight == 1).
type Arc struct {
	ID     string
	From   string
	To     string
	Weight int
	Kind   ArcKind
}

type Model struct {
	ID          string
	Places      []Place
	Transitions []Transition
	Arcs        []Arc
}

type Marking map[string]int

// CanonicalKey serializes a marking. Distinct token counts always produce
// distinct keys; canonicalization never merges token-different markings.
func CanonicalKey(m Marking) string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s=%d", id, m[id]))
	}
	return strings.Join(parts, "|")
}

// Covers reports whether a covers b componentwise (a >= b for every place).
func Covers(a, b Marking) bool {
	for id, bv := range b {
		if a[id] < bv {
			return false
		}
	}
	return true
}

func (m Model) place(id string) (Place, bool) {
	for _, p := range m.Places {
		if p.ID == id {
			return p, true
		}
	}
	return Place{}, false
}

// Enabled implements transition enablement considering input arcs, inhibitor
// arcs and capacity bounds simultaneously.
func (m Model) Enabled(mk Marking, tid string) bool {
	if _, ok := m.transition(tid); !ok {
		return false
	}
	consume := map[string]int{}
	produce := map[string]int{}
	for _, a := range m.Arcs {
		if a.To != tid && a.From != tid {
			continue
		}
		if a.Kind == ArcInhibitor && a.To == tid {
			if mk[a.From] >= a.Weight {
				return false
			}
			continue
		}
		if a.Kind == ArcIn && a.To == tid {
			if mk[a.From] < a.Weight {
				return false
			}
			consume[a.From] += a.Weight
		}
		if a.Kind == ArcOut && a.From == tid {
			produce[a.To] += a.Weight
		}
	}
	next := Marking{}
	for id, v := range mk {
		next[id] = v
	}
	for id, w := range consume {
		next[id] -= w
	}
	for id, w := range produce {
		next[id] += w
	}
	for _, p := range m.Places {
		if !p.HasCapacity {
			continue
		}
		if next[p.ID] > p.Capacity {
			return false
		}
	}
	return true
}

func (m Model) transition(id string) (Transition, bool) {
	for _, t := range m.Transitions {
		if t.ID == id {
			return t, true
		}
	}
	return Transition{}, false
}

// Fire fires tid; returns ok=false if the transition is not enabled.
func (m Model) Fire(mk Marking, tid string) (Marking, bool) {
	if !m.Enabled(mk, tid) {
		return nil, false
	}
	next := Marking{}
	for id, v := range mk {
		next[id] = v
	}
	for _, a := range m.Arcs {
		if a.Kind == ArcIn && a.To == tid {
			next[a.From] -= a.Weight
		}
		if a.Kind == ArcOut && a.From == tid {
			next[a.To] += a.Weight
		}
	}
	return next, true
}

// EnabledTransitions returns every enabled transition in declared order.
func (m Model) EnabledTransitions(mk Marking) []string {
	out := []string{}
	for _, t := range m.Transitions {
		if m.Enabled(mk, t.ID) {
			out = append(out, t.ID)
		}
	}
	return out
}

// DeadlockReason describes why a marking is a deadlock candidate.
type DeadlockReason struct {
	StateID  string
	Blocked  []BlockedTransition
}

type BlockedTransition struct {
	Transition string
	Reasons    []string
}

// ExplainDeadlock returns per-transition blocking reasons (missing input,
// inhibitor held, capacity overflow). Returns nil when the marking is not a
// deadlock.
func (m Model) ExplainDeadlock(mk Marking, stateID string) *DeadlockReason {
	var blocked []BlockedTransition
	anyEnabled := false
	for _, t := range m.Transitions {
		var reasons []string
		for _, a := range m.Arcs {
			if a.Kind == ArcIn && a.To == t.ID {
				if mk[a.From] < a.Weight {
					reasons = append(reasons, fmt.Sprintf("input %s holds %d < weight %d", a.From, mk[a.From], a.Weight))
				}
			}
			if a.Kind == ArcInhibitor && a.To == t.ID {
				if mk[a.From] >= a.Weight {
					reasons = append(reasons, fmt.Sprintf("inhibitor %s holds %d >= threshold %d", a.From, mk[a.From], a.Weight))
				}
			}
		}
		produce := map[string]int{}
		consume := map[string]int{}
		for _, a := range m.Arcs {
			if a.Kind == ArcOut && a.From == t.ID {
				produce[a.To] += a.Weight
			}
			if a.Kind == ArcIn && a.To == t.ID {
				consume[a.From] += a.Weight
			}
		}
		for _, p := range m.Places {
			if !p.HasCapacity {
				continue
			}
			nv := mk[p.ID] - consume[p.ID] + produce[p.ID]
			if nv > p.Capacity {
				reasons = append(reasons, fmt.Sprintf("output %s would hold %d > capacity %d", p.ID, nv, p.Capacity))
			}
		}
		if len(reasons) == 0 {
			anyEnabled = true
		} else {
			blocked = append(blocked, BlockedTransition{Transition: t.ID, Reasons: reasons})
		}
	}
	if anyEnabled {
		return nil
	}
	return &DeadlockReason{StateID: stateID, Blocked: blocked}
}

type Strategy string

const (
	BFS Strategy = "bfs"
	DFS Strategy = "dfs"
)

type State struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Marking Marking `json:"marking"`
	Depth   int    `json:"depth"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

type CoverPair struct {
	Larger  string `json:"larger"`
	Smaller string `json:"smaller"`
}

type Result struct {
	Status       string            `json:"status"` // complete | unknown
	Strategy     Strategy          `json:"strategy"`
	Budget       int               `json:"budget"`
	States       []State           `json:"states"`
	Edges        []Edge            `json:"edges"`
	Frontier     []State           `json:"frontier"`
	Deadlocks    []string          `json:"deadlocks"`
	DeadlockInfo []DeadlockReason  `json:"deadlock_info"`
	CoverPairs   []CoverPair       `json:"cover_pairs"`
	Unbounded    bool              `json:"unbounded"`
	Witness      []string          `json:"witness"`
	Truncated    bool              `json:"truncated"`
	ReachTarget  map[string]string `json:"reach_target,omitempty"`
}

type ExploreOptions struct {
	Strategy Strategy
	Budget   int
	Target   Marking
}

// Explore computes the reachable state space. When the state budget is
// exhausted the result status is "unknown" and the unexpanded frontier is
// retained verbatim.
func (m Model) Explore(initial Marking, opts ExploreOptions) Result {
	if opts.Strategy == "" {
		opts.Strategy = BFS
	}
	if opts.Budget <= 0 {
		opts.Budget = 256
	}
	res := Result{Strategy: opts.Strategy, Budget: opts.Budget, ReachTarget: map[string]string{}}
	root := State{ID: "s0", Key: CanonicalKey(initial), Marking: clone(initial), Depth: 0}
	states := []State{root}
	index := map[string]int{root.Key: 0}
	parent := map[int]edgeFrom{}
	var pending []string
	pending = append(pending, root.Key)
	truncated := false
	witness := map[string]bool{}
	for len(pending) > 0 {
		var curKey string
		if opts.Strategy == DFS {
			curKey = pending[len(pending)-1]
			pending = pending[:len(pending)-1]
		} else {
			curKey = pending[0]
			pending = pending[1:]
		}
		cur := states[index[curKey]]
		for _, tid := range m.EnabledTransitions(cur.Marking) {
			nextMk, _ := m.Fire(cur.Marking, tid)
			key := CanonicalKey(nextMk)
			if _, seen := index[key]; seen {
				res.Edges = append(res.Edges, Edge{From: cur.ID, To: states[index[key]].ID, Transition: tid})
				// Coverability along a discovered edge feeds unbounded witnesses.
				if Covers(nextMk, cur.Marking) && !equalMarking(nextMk, cur.Marking) {
					for _, id := range m.pathTo(parent, index[curKey], index[key]) {
						witness[id] = true
					}
					res.Unbounded = true
				}
				continue
			}
			if len(states) >= opts.Budget {
				truncated = true
				frontier := State{ID: fmt.Sprintf("f%d", len(res.Frontier)), Key: key, Marking: clone(nextMk), Depth: cur.Depth + 1}
				res.Frontier = append(res.Frontier, frontier)
				res.Edges = append(res.Edges, Edge{From: cur.ID, To: frontier.ID, Transition: tid})
				continue
			}
			ns := State{ID: fmt.Sprintf("s%d", len(states)), Key: key, Marking: clone(nextMk), Depth: cur.Depth + 1}
			states = append(states, ns)
			index[key] = len(states) - 1
			parent[len(states)-1] = edgeFrom{prev: index[curKey], trans: tid}
			res.Edges = append(res.Edges, Edge{From: cur.ID, To: ns.ID, Transition: tid})
			pending = append(pending, key)
			if Covers(nextMk, cur.Marking) && !equalMarking(nextMk, cur.Marking) {
				res.Unbounded = true
				for _, id := range m.pathTo(parent, index[curKey], len(states)-1) {
					witness[id] = true
				}
			}
		}
	}
	res.States = states
	if opts.Target != nil {
		for _, s := range states {
			match := true
			for pid, tv := range opts.Target {
				if s.Marking[pid] != tv {
					match = false
					break
				}
			}
			if match {
				res.ReachTarget[CanonicalKey(opts.Target)] = s.ID
				break
			}
		}
	}
	for _, s := range states {
		if len(m.EnabledTransitions(s.Marking)) == 0 {
			res.Deadlocks = append(res.Deadlocks, s.ID)
			if reason := m.ExplainDeadlock(s.Marking, s.ID); reason != nil {
				res.DeadlockInfo = append(res.DeadlockInfo, *reason)
			}
		}
	}
	for i := 0; i < len(states); i++ {
		for j := 0; j < len(states); j++ {
			if i == j {
				continue
			}
			if Covers(states[i].Marking, states[j].Marking) && !equalMarking(states[i].Marking, states[j].Marking) {
				res.CoverPairs = append(res.CoverPairs, CoverPair{Larger: states[i].ID, Smaller: states[j].ID})
			}
		}
	}
	if res.Unbounded {
		var w []string
		for id := range witness {
			w = append(w, id)
		}
		sort.Strings(w)
		res.Witness = w
	}
	res.Truncated = truncated
	if truncated {
		res.Status = "unknown"
	} else {
		res.Status = "complete"
	}
	return res
}

type edgeFrom struct {
	prev  int
	trans string
}

func (m Model) pathTo(parent map[int]edgeFrom, from, to int) []string {
	ids := []string{fmt.Sprintf("s%d", to)}
	cur := to
	for cur != from && cur != 0 {
		p, ok := parent[cur]
		if !ok {
			break
		}
		cur = p.prev
		ids = append(ids, fmt.Sprintf("s%d", cur))
	}
	return ids
}

func clone(mk Marking) Marking {
	out := Marking{}
	for k, v := range mk {
		out[k] = v
	}
	return out
}

func equalMarking(a, b Marking) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
