// Package petri implements a Petri net reachability / state-space engine.
//
// Domain rules encoded here (also surfaced as machine-readable judgments):
//   - transition enablement considers input arcs, inhibitor arcs and place
//     capacities at the same time;
//   - state canonicalization never merges markings that differ in token
//     counts;
//   - when the exploration budget is exhausted the result is "unknown" and
//     the unexpanded frontier is preserved.
package petri

import (
	"fmt"
	"sort"
	"strings"
)

// Arc kinds.
const (
	ArcInput     = "input"
	ArcOutput    = "output"
	ArcInhibitor = "inhibitor"
)

// Place is a Petri net place. Capacity < 0 means unbounded. Unit and Basis
// carry the domain quantity metadata (token unit, coordinate basis) so the
// raw representation survives into the derived layer.
type Place struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Capacity int     `json:"capacity"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Unit     string  `json:"unit"`
	Basis    string  `json:"basis"`
}

// Transition is a Petri net transition.
type Transition struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// Arc connects a place and a transition with a weight. Inhibitor arcs use
// Weight as the inhibition threshold.
type Arc struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // input | output | inhibitor
	Place      string `json:"place"`
	Transition string `json:"transition"`
	Weight     int    `json:"weight"`
	Unit       string `json:"unit"`
}

// Net is a fully identified Petri net.
type Net struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Places      []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []Arc        `json:"arcs"`
}

// Marking maps place ID to token count.
type Marking map[string]int

// Strategy describes the exploration order. Only "bfs" is implemented; the
// value is versioned together with the model.
type Strategy struct {
	ID    string `json:"id"`
	Order string `json:"order"`
}

// Budget bounds exploration. MaxStates <= 0 means no bound.
type Budget struct {
	ID        string `json:"id"`
	MaxStates int    `json:"max_states"`
	Unit      string `json:"unit"` // "states"
}

// DisableReason explains, per transition, why it is not enabled at a
// marking. Cause is one of insufficient_input | inhibitor_active |
// capacity_full.
type DisableReason struct {
	Transition string `json:"transition"`
	Cause      string `json:"cause"`
	Place      string `json:"place"`
	Need       int    `json:"need"`
	Have       int    `json:"have"`
	Capacity   int    `json:"capacity,omitempty"`
}

// Judgment is a machine-readable record that a domain rule fired.
type Judgment struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Rule identifiers used in Judgment.Rule.
const (
	RuleEnablement       = "enablement_input_inhibitor_capacity"
	RuleCanonicalization = "canonicalization_token_sensitive"
	RuleBudgetExhaustion = "budget_exhaustion_unknown_keep_frontier"
)

func (n *Net) placeByID(id string) *Place {
	for i := range n.Places {
		if n.Places[i].ID == id {
			return &n.Places[i]
		}
	}
	return nil
}

// Enabled reports whether transition tID may fire at m, considering input
// arcs, inhibitor arcs and output-place capacities simultaneously. When it
// cannot fire, reasons lists every blocking cause.
func (n *Net) Enabled(m Marking, tID string) (bool, []DisableReason) {
	var reasons []DisableReason
	for _, a := range n.Arcs {
		if a.Transition != tID {
			continue
		}
		have := m[a.Place]
		switch a.Kind {
		case ArcInput:
			if have < a.Weight {
				reasons = append(reasons, DisableReason{
					Transition: tID, Cause: "insufficient_input",
					Place: a.Place, Need: a.Weight, Have: have,
				})
			}
		case ArcInhibitor:
			if have >= a.Weight {
				reasons = append(reasons, DisableReason{
					Transition: tID, Cause: "inhibitor_active",
					Place: a.Place, Need: a.Weight, Have: have,
				})
			}
		case ArcOutput:
			p := n.placeByID(a.Place)
			if p != nil && p.Capacity >= 0 && have+a.Weight > p.Capacity {
				reasons = append(reasons, DisableReason{
					Transition: tID, Cause: "capacity_full",
					Place: a.Place, Need: a.Weight, Have: have,
					Capacity: p.Capacity,
				})
			}
		}
	}
	return len(reasons) == 0, reasons
}

// Fire returns the successor marking after firing tID. Callers must check
// Enabled first; Fire does not re-validate.
func (n *Net) Fire(m Marking, tID string) Marking {
	next := make(Marking, len(m))
	for k, v := range m {
		next[k] = v
	}
	for _, a := range n.Arcs {
		if a.Transition != tID {
			continue
		}
		switch a.Kind {
		case ArcInput:
			next[a.Place] -= a.Weight
		case ArcOutput:
			next[a.Place] += a.Weight
		}
	}
	return next
}

// Canonical renders a marking as a token-sensitive canonical key: places in
// sorted order with exact token counts. Two markings with different token
// counts never share a key.
func Canonical(m Marking) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%s=%d", k, m[k])
	}
	return b.String()
}

// State is one reached marking with a stable identity assigned in discovery
// order (S1, S2, ...).
type State struct {
	ID            string  `json:"id"`
	Key           string  `json:"key"`
	Marking       Marking `json:"marking"`
	Depth         int     `json:"depth"`
	Parent        string  `json:"parent,omitempty"`
	ViaTransition string  `json:"via_transition,omitempty"`
}

// Edge is a fired transition between two states.
type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

// CoveragePair records that Covering strictly covers Covered componentwise.
type CoveragePair struct {
	Covered  string `json:"covered"`
	Covering string `json:"covering"`
}

// DeadlockInfo records a deadlocked state and why every transition is
// disabled there.
type DeadlockInfo struct {
	StateID string          `json:"state_id"`
	Reasons []DisableReason `json:"reasons"`
}

// Result is the outcome of one exploration run.
type Result struct {
	Status    string         `json:"status"` // complete | unknown
	States    []State        `json:"states"`
	Edges     []Edge         `json:"edges"`
	Coverage  []CoveragePair `json:"coverage"`
	Deadlocks []DeadlockInfo `json:"deadlocks"`
	Frontier  []State        `json:"frontier"`
	Unbounded bool           `json:"unbounded"`
	Judgments []Judgment     `json:"judgments"`
}

// covers reports whether a strictly covers b componentwise over the same
// place set.
func covers(a, b Marking) bool {
	strict := false
	for k, vb := range b {
		va, ok := a[k]
		if !ok || va < vb {
			return false
		}
		if va > vb {
			strict = true
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return strict
}

// Explore runs BFS from initial under budget and returns the full state
// space view, including machine-readable judgments for every domain rule
// that fired.
func Explore(n *Net, initial Marking, strat Strategy, budget Budget) *Result {
	res := &Result{Status: "complete"}
	keyOf := Canonical
	seen := map[string]string{}
	byID := map[string]*State{}

	mkState := func(m Marking, depth int, parent, via string) *State {
		s := &State{
			ID:            fmt.Sprintf("S%d", len(res.States)+1),
			Key:           keyOf(m),
			Marking:       m,
			Depth:         depth,
			Parent:        parent,
			ViaTransition: via,
		}
		res.States = append(res.States, *s)
		seen[s.Key] = s.ID
		byID[s.ID] = &res.States[len(res.States)-1]
		return byID[s.ID]
	}

	start := mkState(initial, 0, "", "")
	queue := []*State{start}
	canonNote := false

	for len(queue) > 0 {
		if budget.MaxStates > 0 && len(res.States) >= budget.MaxStates {
			// Budget exhausted: result is unknown, frontier is preserved.
			res.Status = "unknown"
			for _, s := range queue {
				res.Frontier = append(res.Frontier, *s)
			}
			res.Judgments = append(res.Judgments, Judgment{
				Rule:    RuleBudgetExhaustion,
				Subject: budget.ID,
				Detail: fmt.Sprintf("budget %d states exhausted; %d frontier states preserved",
					budget.MaxStates, len(queue)),
			})
			break
		}
		cur := queue[0]
		queue = queue[1:]

		enabledAny := false
		var reasons []DisableReason
		for _, t := range n.Transitions {
			ok, rs := n.Enabled(cur.Marking, t.ID)
			if !ok {
				reasons = append(reasons, rs...)
				for _, r := range rs {
					if r.Cause == "inhibitor_active" || r.Cause == "capacity_full" {
						res.Judgments = append(res.Judgments, Judgment{
							Rule:    RuleEnablement,
							Subject: t.ID,
							Detail: fmt.Sprintf("state %s: %s blocked by %s on %s (have %d, need %d, cap %d)",
								cur.ID, t.ID, r.Cause, r.Place, r.Have, r.Need, r.Capacity),
						})
					}
				}
				continue
			}
			enabledAny = true
			next := n.Fire(cur.Marking, t.ID)
			key := keyOf(next)
			if prevID, dup := seen[key]; dup {
				// Same canonical key: identical marking, merge is safe.
				res.Edges = append(res.Edges, Edge{From: cur.ID, To: prevID, Transition: t.ID})
				continue
			}
			// Token-different markings are never merged by canonicalization.
			if !canonNote {
				canonNote = true
				res.Judgments = append(res.Judgments, Judgment{
					Rule:    RuleCanonicalization,
					Subject: cur.ID,
					Detail:  "canonical key includes exact token counts; token-different markings stay distinct states",
				})
			}
			ns := mkState(next, cur.Depth+1, cur.ID, t.ID)
			res.Edges = append(res.Edges, Edge{From: cur.ID, To: ns.ID, Transition: t.ID})
			queue = append(queue, ns)
		}
		if !enabledAny {
			res.Deadlocks = append(res.Deadlocks, DeadlockInfo{StateID: cur.ID, Reasons: reasons})
		}
	}

	// Coverage relation among reached states.
	for i := range res.States {
		for j := range res.States {
			if i != j && covers(res.States[j].Marking, res.States[i].Marking) {
				res.Coverage = append(res.Coverage, CoveragePair{
					Covered: res.States[i].ID, Covering: res.States[j].ID,
				})
			}
		}
	}

	// Unbounded-growth candidate: a frontier state strictly covers an
	// ancestor on its own path (Karp-Miller omega witness).
	if res.Status == "unknown" {
		for _, f := range res.Frontier {
			if coversAncestor(byID, &f) {
				res.Unbounded = true
				break
			}
		}
	}
	return res
}

func coversAncestor(byID map[string]*State, s *State) bool {
	cur, ok := byID[s.Parent]
	for ok {
		if covers(s.Marking, cur.Marking) {
			return true
		}
		cur, ok = byID[cur.Parent]
	}
	return false
}
