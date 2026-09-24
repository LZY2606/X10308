// Package petri implements a Petri net reachability / state-space engine.
//
// Semantics enforced here (these are the versioned derivation rules):
//   - transition enablement considers input arcs, inhibitor arcs and place
//     capacities simultaneously;
//   - state canonicalization never merges markings whose token counts differ;
//   - when the exploration budget is exhausted the result is "unknown" and
//     the unexpanded frontier is preserved.
package petri

import (
	"fmt"
	"sort"
	"strings"
)

// Omega marks an unbounded (ω) token count produced by coverability
// acceleration. It is the only negative value allowed in a marking.
const Omega = -1

// ArcKind distinguishes the three supported arc roles.
type ArcKind string

const (
	ArcInput     ArcKind = "input"
	ArcOutput    ArcKind = "output"
	ArcInhibitor ArcKind = "inhibitor"
)

// Place is a net place. Capacity < 0 means unbounded.
type Place struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// Transition is a net transition.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Arc is a weighted arc. For ArcInput/ArcInhibitor Src is a place and Dst a
// transition; for ArcOutput Src is a transition and Dst a place.
type Arc struct {
	ID     string  `json:"id"`
	Src    string  `json:"src"`
	Dst    string  `json:"dst"`
	Kind   ArcKind `json:"kind"`
	Weight int     `json:"weight"`
}

// Net is a versioned Petri net model. Node identities are stable strings
// supplied by the source material; they are never renumbered.
type Net struct {
	ID          string       `json:"id"`
	Places      []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []Arc        `json:"arcs"`
}

// Marking maps place ID to token count (or Omega).
type Marking map[string]int

// PlaceIndex returns places sorted by ID for deterministic iteration.
func (n *Net) PlaceIndex() []Place {
	ps := make([]Place, len(n.Places))
	copy(ps, n.Places)
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	return ps
}

// TransitionIndex returns transitions sorted by ID.
func (n *Net) TransitionIndex() []Transition {
	ts := make([]Transition, len(n.Transitions))
	copy(ts, n.Transitions)
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	return ts
}

func (n *Net) placeByID(id string) (Place, bool) {
	for _, p := range n.Places {
		if p.ID == id {
			return p, true
		}
	}
	return Place{}, false
}

func (n *Net) arcsInto(t string, kind ArcKind) []Arc {
	var out []Arc
	for _, a := range n.Arcs {
		if a.Dst == t && a.Kind == kind {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (n *Net) arcsOutOf(t string) []Arc {
	var out []Arc
	for _, a := range n.Arcs {
		if a.Src == t && a.Kind == ArcOutput {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Canonical renders a marking as a stable string. Two markings map to the
// same canonical string iff every place carries the same token count;
// markings that differ in tokens are never merged.
func (n *Net) Canonical(m Marking) string {
	var b strings.Builder
	for i, p := range n.PlaceIndex() {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(p.ID)
		b.WriteByte('=')
		v, ok := m[p.ID]
		if !ok {
			v = 0
		}
		if v == Omega {
			b.WriteString("ω")
		} else {
			fmt.Fprintf(&b, "%d", v)
		}
	}
	return b.String()
}

// Violation explains, for one arc or capacity constraint, why a transition
// is not enabled. It is machine-readable evidence for deadlock reports.
type Violation struct {
	TransitionID string  `json:"transition_id"`
	ArcID        string  `json:"arc_id,omitempty"`
	PlaceID      string  `json:"place_id"`
	Kind         ArcKind `json:"kind"` // "capacity" is reported with Kind=="capacity"
	Need         int     `json:"need"`
	Have         int     `json:"have"`
}

// ViolationKindCapacity is used in Violation.Kind for capacity failures.
const ViolationKindCapacity ArcKind = "capacity"

// Enabled reports whether t can fire in m, and if not, the full list of
// input, inhibitor and capacity violations. Capacity is checked against the
// hypothetical post-fire marking, so enablement considers all three
// constraint families at once.
func (n *Net) Enabled(t string, m Marking) (bool, []Violation) {
	var bad []Violation
	for _, a := range n.arcsInto(t, ArcInput) {
		have := m[a.Src]
		if have != Omega && have < a.Weight {
			bad = append(bad, Violation{TransitionID: t, ArcID: a.ID, PlaceID: a.Src, Kind: ArcInput, Need: a.Weight, Have: have})
		}
	}
	for _, a := range n.arcsInto(t, ArcInhibitor) {
		have := m[a.Src]
		// Inhibitor arc of weight w disables t while M(p) >= w.
		// ω always inhibits.
		if have == Omega || have >= a.Weight {
			bad = append(bad, Violation{TransitionID: t, ArcID: a.ID, PlaceID: a.Src, Kind: ArcInhibitor, Need: a.Weight, Have: have})
		}
	}
	if len(bad) > 0 {
		return false, bad
	}
	next := n.Fire(t, m)
	for _, p := range n.Places {
		if p.Capacity < 0 {
			continue
		}
		v := next[p.ID]
		if v != Omega && v > p.Capacity {
			bad = append(bad, Violation{TransitionID: t, PlaceID: p.ID, Kind: ViolationKindCapacity, Need: p.Capacity, Have: v})
		}
	}
	return len(bad) == 0, bad
}

// Fire returns the marking produced by firing t in m. Callers must check
// Enabled first. ω places stay ω.
func (n *Net) Fire(t string, m Marking) Marking {
	out := Marking{}
	for _, p := range n.Places {
		out[p.ID] = m[p.ID]
	}
	for _, a := range n.arcsInto(t, ArcInput) {
		if out[a.Src] != Omega {
			out[a.Src] -= a.Weight
		}
	}
	for _, a := range n.arcsOutOf(t) {
		if out[a.Dst] != Omega {
			out[a.Dst] += a.Weight
		}
	}
	return out
}

// geq reports whether token count a covers b (ω covers everything finite).
func geq(a, b int) bool {
	if b == Omega {
		return a == Omega
	}
	if a == Omega {
		return true
	}
	return a >= b
}

// covers reports whether m componentwise covers anc, and strictly covers it
// (some place has strictly more tokens).
func (n *Net) covers(m, anc Marking) (cover, strict bool) {
	strict = false
	for _, p := range n.Places {
		mv, av := m[p.ID], anc[p.ID]
		if !geq(mv, av) {
			return false, false
		}
		if av == Omega {
			continue
		}
		if mv == Omega || mv > av {
			strict = true
		}
	}
	return true, strict
}

// State is one explored marking.
type State struct {
	ID       string  `json:"id"` // canonical encoding, stable identity
	Marking  Marking `json:"marking"`
	Depth    int     `json:"depth"`
	Expanded bool    `json:"expanded"`
}

// Edge is a cover-relation / reachability edge between two states.
type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

// DeadlockInfo records a deadlocked state and, per transition, the
// machine-readable reasons no transition is enabled.
type DeadlockInfo struct {
	StateID string                 `json:"state_id"`
	Reasons map[string][]Violation `json:"reasons"`
}

// CoverPair records that state To ω-accelerated against ancestor From.
type CoverPair struct {
	Ancestor   string   `json:"ancestor"`
	Descendant string   `json:"descendant"`
	OmegaPlace []string `json:"omega_places"`
}

// Result is the outcome of one exploration run.
type Result struct {
	Status    string         `json:"status"` // "complete" or "unknown" (budget exhausted)
	States    []State        `json:"states"` // discovery order
	Edges     []Edge         `json:"edges"`
	Deadlocks []DeadlockInfo `json:"deadlocks"`
	Frontier  []State        `json:"frontier"` // unexpanded states when truncated
	Covers    []CoverPair    `json:"covers"`
	Budget    int            `json:"budget"`
}

// Explore runs BFS from the initial marking with Karp–Miller style ω
// acceleration against ancestors on the current path. When more than budget
// states would be expanded, exploration stops, returns Status "unknown" and
// keeps the remaining frontier.
func (n *Net) Explore(initial Marking, budget int) *Result {
	res := &Result{Status: "complete", Budget: budget}
	idx := map[string]int{}       // canonical -> index in res.States
	parent := map[string]string{} // canonical -> parent canonical ("")
	var queue []string

	init := Marking{}
	for _, p := range n.Places {
		init[p.ID] = initial[p.ID]
	}
	ic := n.Canonical(init)
	idx[ic] = 0
	res.States = append(res.States, State{ID: ic, Marking: init})
	parent[ic] = ""
	queue = append(queue, ic)

	expanded := 0
	for len(queue) > 0 {
		if expanded >= budget {
			res.Status = "unknown"
			seen := map[string]bool{}
			for _, c := range queue {
				if !seen[c] {
					seen[c] = true
					res.Frontier = append(res.Frontier, res.States[idx[c]])
				}
			}
			return res
		}
		cur := queue[0]
		queue = queue[1:]
		curState := res.States[idx[cur]]
		res.States[idx[cur]].Expanded = true
		expanded++

		// Reconstruct ancestor chain for cover checks.
		var ancestors []string
		for p := parent[cur]; p != ""; p = parent[p] {
			ancestors = append(ancestors, p)
		}

		anyEnabled := false
		reasons := map[string][]Violation{}
		for _, t := range n.TransitionIndex() {
			ok, bad := n.Enabled(t.ID, curState.Marking)
			if !ok {
				reasons[t.ID] = bad
				continue
			}
			anyEnabled = true
			next := n.Fire(t.ID, curState.Marking)

			// ω acceleration against strict ancestors on this path.
			var omegaPlaces []string
			var accelAnc string
			for _, ac := range ancestors {
				am := res.States[idx[ac]].Marking
				if cov, strict := n.covers(next, am); cov && strict {
					for _, p := range n.Places {
						mv, av := next[p.ID], am[p.ID]
						if av != Omega && (mv == Omega || mv > av) {
							if next[p.ID] != Omega {
								next[p.ID] = Omega
							}
							omegaPlaces = append(omegaPlaces, p.ID)
						}
					}
					accelAnc = ac
					break
				}
			}

			nc := n.Canonical(next)
			if _, exists := idx[nc]; !exists {
				idx[nc] = len(res.States)
				res.States = append(res.States, State{ID: nc, Marking: next, Depth: curState.Depth + 1})
				parent[nc] = cur
				queue = append(queue, nc)
			}
			res.Edges = append(res.Edges, Edge{From: cur, To: nc, Transition: t.ID})
			if accelAnc != "" {
				sort.Strings(omegaPlaces)
				res.Covers = append(res.Covers, CoverPair{Ancestor: accelAnc, Descendant: nc, OmegaPlace: omegaPlaces})
			}
		}
		if !anyEnabled {
			res.Deadlocks = append(res.Deadlocks, DeadlockInfo{StateID: cur, Reasons: reasons})
		}
	}
	return res
}

// MatchesTarget reports whether a state marking equals the target marking
// exactly (token counts are never merged, so equality is exact).
func (n *Net) MatchesTarget(m, target Marking) bool {
	for _, p := range n.Places {
		if m[p.ID] != target[p.ID] {
			return false
		}
	}
	return true
}
