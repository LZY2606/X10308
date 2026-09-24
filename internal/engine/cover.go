package engine

import (
	"fmt"
	"sort"

	"tflocal/internal/domain"
)

// CovNode is a node of the Karp-Miller coverability tree.
type CovNode struct {
	ID        string                 `json:"id"`
	Seq       int                    `json:"seq"`
	Marking   OmegaMarking           `json:"marking"`
	Canon     string                 `json:"canon"`
	ParentID  string                 `json:"parent_id,omitempty"`
	ParentFire string                `json:"parent_fire,omitempty"`
	Depth     int                    `json:"depth"`
	OmegaPlaces []string              `json:"omega_places"`
	Accelerated bool                  `json:"accelerated"`
	Duplicate bool                   `json:"duplicate"`
	Terminal  bool                   `json:"terminal"`
	Frontier  bool                   `json:"frontier"`
	Deadlock  bool                   `json:"deadlock"`
	CoversGoal bool                  `json:"covers_goal"`
	Enablement map[string]*Enablement `json:"enablement,omitempty"`
}

type CovEdge struct {
	FromID     string `json:"from_id"`
	ToID       string `json:"to_id"`
	Transition string `json:"transition"`
}

// CoverPair records that CoveringID (an omega marking) covers CoveredID
// (a strictly dominated ancestor). Places lists components raised to omega.
type CoverPair struct {
	CoveringID string   `json:"covering_id"`
	CoveredID  string   `json:"covered_id"`
	Places     []string `json:"omega_places"`
}

// CovResult is the machine-readable coverability record.
type CovResult struct {
	Strategy   string           `json:"strategy"`
	Budget     int              `json:"budget"`
	Fired      int              `json:"steps_used"`
	Truncated  bool             `json:"truncated"`
	Unbounded  bool             `json:"unbounded"`
	Nodes      []*CovNode       `json:"nodes"`
	Edges      []*CovEdge       `json:"edges"`
	CoverPairs []CoverPair      `json:"cover_pairs"`
	Frontier   []string         `json:"frontier_ids"`
	GoalCoveredBy []string      `json:"goal_covered_by,omitempty"`
	Events     []map[string]any `json:"events,omitempty"`
}

func toOmega(m domain.Marking) OmegaMarking {
	o := OmegaMarking{}
	for k, v := range m {
		o[k] = int64(v)
	}
	return o
}

func fireOmega(n *domain.Net, tID string, m OmegaMarking) OmegaMarking {
	c := m.Clone()
	for _, a := range n.Inputs[tID] {
		if c[a.From] == Omega {
			c[a.From] = Omega // omega - n = omega
		} else {
			c[a.From] -= int64(a.Weight)
		}
	}
	for _, a := range n.Outputs[tID] {
		if c[a.To] == Omega {
			c[a.To] = Omega
		} else {
			c[a.To] += int64(a.Weight)
		}
	}
	return c
}

func enableOmega(n *domain.Net, tID string, m OmegaMarking, inhibitors bool) Enablement {
	// Concrete enablement against omega: omega inputs always satisfy input
	// arcs; inhibitors at omega places always block (cannot guarantee zero).
	e := Enablement{TransitionID: tID, InputOK: true, InhibitorOK: true, CapacityOK: true}
	for _, a := range n.Inputs[tID] {
		v := m[a.From]
		if v != Omega && v < int64(a.Weight) {
			e.InputOK = false
			e.Details = append(e.Details, fmt.Sprintf("input %s has %d < %d", a.From, v, a.Weight))
		}
	}
	if inhibitors {
		for _, a := range n.Inhibitors[tID] {
			v := m[a.From]
			if v == Omega || v >= int64(a.Weight) {
				e.InhibitorOK = false
				e.Details = append(e.Details, fmt.Sprintf("inhibitor %s holds %d >= %d", a.From, v, a.Weight))
			}
		}
	}
	for _, a := range n.Outputs[tID] {
		if n.Bounded[a.To] {
			v := m[a.To]
			if v == Omega {
				continue
			}
			if v+int64(a.Weight) > int64(n.Capacity[a.To]) {
				e.CapacityOK = false
				e.Details = append(e.Details, fmt.Sprintf("capacity %s would exceed %d", a.To, n.Capacity[a.To]))
			}
		}
	}
	e.Enabled = e.InputOK && e.InhibitorOK && e.CapacityOK
	return e
}

// strictLess reports a < b componentwise with at least one strict inequality,
// treating omega as incomparable (no omega on either side in KM acceleration
// comparisons).
func strictLess(a, b OmegaMarking) bool {
	strict := false
	for p, av := range a {
		bv, ok := b[p]
		if !ok {
			return false
		}
		if av == Omega || bv == Omega {
			if av != bv {
				return false
			}
			continue
		}
		if av > bv {
			return false
		}
		if av < bv {
			strict = true
		}
	}
	return strict
}

// Coverability builds the Karp-Miller tree under a firing budget.
func Coverability(n *domain.Net, params domain.Params, budgetOverride int) *CovResult {
	budget := params.Budget
	if budgetOverride > 0 {
		budget = budgetOverride
	}
	r := &CovResult{Strategy: "karp_miller", Budget: budget}
	emit := func(kind string, kv map[string]any) {
		ev := map[string]any{"event": kind}
		for k, v := range kv {
			ev[k] = v
		}
		r.Events = append(r.Events, ev)
	}
	emit("coverability_started", map[string]any{"strategy": "karp_miller", "budget": budget})

	byCanon := map[string]*CovNode{}
	root := &CovNode{Marking: toOmega(n.Init), Depth: 0}
	root.Canon = root.Marking.Canon()
	root.ID = "C" + root.Canon
	root.OmegaPlaces = []string{}
	byCanon[root.Canon] = root
	order := []*CovNode{root}
	type job struct{ node *CovNode }
	stack := []*CovNode{root}
	tids := sortedTransitions(n)
	frontierSet := map[string]bool{}

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Duplicate {
			cur.Terminal = true
			continue
		}
		cur.Enablement = map[string]*Enablement{}
		var enabled []string
		for _, t := range tids {
			en := enableOmega(n, t, cur.Marking, params.InhibitorsEnabled)
			en2 := en
			cur.Enablement[t] = &en2
			if en.Enabled {
				enabled = append(enabled, t)
			}
		}
		emit("enablement_evaluated", map[string]any{"state_id": cur.ID, "rule": "input_and_inhibitor_and_capacity"})
		if len(enabled) == 0 {
			cur.Deadlock = true
			cur.Terminal = true
			emit("deadlock_candidate", map[string]any{"state_id": cur.ID})
			continue
		}
		// expand in sorted order, pushing reversed so smallest pops first
		for i := len(enabled) - 1; i >= 0; i-- {
			t := enabled[i]
			if r.Fired >= budget {
				if !frontierSet[cur.ID] {
					frontierSet[cur.ID] = true
					cur.Frontier = true
					r.Frontier = append(r.Frontier, cur.ID)
				}
				emit("frontier_retained", map[string]any{"state_id": cur.ID, "blocked_fire": t, "reason": "budget_exhausted"})
				continue
			}
			r.Fired++
			nm := fireOmega(n, t, cur.Marking)
			// ancestor acceleration
			var dominated *CovNode
			for anc := cur; anc != nil; anc = byCanon[anc.ParentID] {
				if anc != cur && strictLess(anc.Marking, nm) {
					dominated = anc
					break
				}
			}
			accelerated := false
			raised := []string{}
			if dominated != nil {
				for p, dv := range dominated.Marking {
					nv := nm[p]
					if nv != Omega && dv != Omega && nv > dv {
						nm[p] = Omega
						raised = append(raised, p)
						accelerated = true
					}
				}
				sort.Strings(raised)
			}
			can := nm.Canon()
			child, seen := byCanon[can]
			if !seen {
				child = &CovNode{Marking: nm, Canon: can, ID: "C" + can, ParentID: cur.ID,
					ParentFire: t, Depth: cur.Depth + 1, Accelerated: accelerated}
				for p, v := range nm {
					if v == Omega {
						child.OmegaPlaces = append(child.OmegaPlaces, p)
					}
				}
				sort.Strings(child.OmegaPlaces)
				byCanon[can] = child
				order = append(order, child)
				stack = append(stack, child)
				if accelerated {
					r.Unbounded = true
					r.CoverPairs = append(r.CoverPairs, CoverPair{CoveringID: child.ID, CoveredID: dominated.ID, Places: raised})
					emit("omega_acceleration", map[string]any{"state_id": child.ID, "dominates": dominated.ID, "omega_places": raised})
				}
				if n.Goal != nil && nm.Covers(*n.Goal) {
					child.CoversGoal = true
					r.GoalCoveredBy = append(r.GoalCoveredBy, child.ID)
					emit("goal_covered", map[string]any{"state_id": child.ID})
				}
				emit("cover_state_discovered", map[string]any{"state_id": child.ID, "parent_id": cur.ID, "fire": t})
			} else if child != cur {
				child.Duplicate = true
			}
			r.Edges = append(r.Edges, &CovEdge{FromID: cur.ID, ToID: child.ID, Transition: t})
		}
	}
	// mark duplicates encountered later as terminal for drawing
	for _, nd := range order {
		if nd.Duplicate {
			nd.Terminal = true
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].Depth != order[j].Depth {
			return order[i].Depth < order[j].Depth
		}
		return order[i].ID < order[j].ID
	})
	for i, nd := range order {
		nd.Seq = i
	}
	sort.Strings(r.Frontier)
	r.Nodes = order
	r.Truncated = len(r.Frontier) > 0
	emit("coverability_finished", map[string]any{"unbounded": r.Unbounded, "truncated": r.Truncated, "nodes": len(order), "cover_pairs": len(r.CoverPairs)})
	return r
}
