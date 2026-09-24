package pn

import (
	"fmt"
	"sort"
	"strings"
)

// Omega is the unbound token symbol in a coverability marking.
const Omega int64 = -1

// OmegaMarking maps place id -> token count, or Omega.
type OmegaMarking map[string]int64

func (n *Net) omegaKey(m OmegaMarking) string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(';')
		}
		v := m[id]
		if v == Omega {
			fmt.Fprintf(&b, "%s=w", id)
		} else {
			fmt.Fprintf(&b, "%s=%d", id, v)
		}
	}
	return b.String()
}

// CoverNode is a (possibly omega) node of the coverability graph.
type CoverNode struct {
	ID      string       `json:"id"`
	Key     string       `json:"canonical_key"`
	Marking OmegaMarking `json:"marking"`
	Omega   []string     `json:"omega_places"`
	Depth   int          `json:"depth"`
}

// CoverEdge is a firing edge between coverability nodes.
type CoverEdge struct {
	ID         string `json:"id"`
	From       string `json:"from_node_id"`
	To         string `json:"to_node_id"`
	Transition string `json:"transition_id"`
	Kind       string `json:"kind"` // normal | self_loop
}

// OmegaGrowth records one Karp-Miller acceleration (unbounded witness).
type OmegaGrowth struct {
	Place      string `json:"place_id"`
	From       string `json:"ancestor_node_id"`
	To         string `json:"accelerated_node_id"`
	Transition string `json:"transition_id"`
	Detail     string `json:"detail"`
}

// CoverPair is a cover relation between concrete explored markings.
type CoverPair struct {
	From   string `json:"covering_state_id"`
	To     string `json:"covered_state_id"`
	Strict bool   `json:"strict"`
}

// CoverResult is the coverability analysis report.
type CoverResult struct {
	Nodes  []CoverNode  `json:"nodes"`
	Edges  []CoverEdge  `json:"edges"`
	Growth []OmegaGrowth `json:"omega_growth"`
	Cover  []CoverPair   `json:"cover_relations"`
	Events []Event       `json:"judgment_events"`

	nodeByKey map[string]string
}

// IsOmegaEnabled evaluates enablement on an omega marking: inputs pass on
// omega places, inhibitor arcs block on omega places, capacities only
// constrain finite output places.
func (n *Net) IsOmegaEnabled(tID string, m OmegaMarking) bool {
	for _, a := range n.incoming[tID] {
		v := m[a.From]
		if v != Omega && v < a.Weight {
			return false
		}
	}
	for _, a := range n.inhibitors[tID] {
		if v := m[a.From]; v == Omega || v >= a.Weight {
			return false
		}
	}
	for _, a := range n.outgoing[tID] {
		p, ok := n.PlaceAt(a.To)
		v := m[a.To]
		if v == Omega {
			continue
		}
		if ok && p.Capacity != nil && v+a.Weight > *p.Capacity {
			return false
		}
	}
	return true
}

func (n *Net) fireOmega(tID string, m OmegaMarking) OmegaMarking {
	next := make(OmegaMarking, len(m))
	for k, v := range m {
		next[k] = v
	}
	for _, a := range n.incoming[tID] {
		if next[a.From] != Omega {
			next[a.From] -= a.Weight
		}
	}
	for _, a := range n.outgoing[tID] {
		if next[a.To] == Omega {
			continue
		}
		next[a.To] += a.Weight
	}
	return next
}

// Coverability builds the Karp-Miller coverability graph. firingsBudget
// bounds the work (unknown-result explorations keep a truncated frontier
// flag in events). concrete supplies optional concrete BFS states between
// which strict cover relations are reported.
func (n *Net) Coverability(start Marking, firingsBudget int64, concrete []State) (*CoverResult, error) {
	if err := n.MarkingValid(start); err != nil {
		return nil, fmt.Errorf("start marking: %w", err)
	}
	if firingsBudget <= 0 {
		firingsBudget = 10000
	}
	res := &CoverResult{nodeByKey: map[string]string{}}
	om := OmegaMarking{}
	for _, p := range n.Places {
		om[p.ID] = start[p.ID]
	}
	addNode := func(m OmegaMarking, depth int) string {
		key := n.omegaKey(m)
		if id, ok := res.nodeByKey[key]; ok {
			return id
		}
		id := fmt.Sprintf("c%05d", len(res.Nodes)+1)
		var wp []string
		for _, p := range n.Places {
			if m[p.ID] == Omega {
				wp = append(wp, p.ID)
			}
		}
		sort.Strings(wp)
		res.Nodes = append(res.Nodes, CoverNode{ID: id, Key: key, Marking: cloneOmega(m), Omega: wp, Depth: depth})
		res.nodeByKey[key] = id
		return id
	}
	emit := func(ev Event) {
		ev.Seq = len(res.Events) + 1
		res.Events = append(res.Events, ev)
	}

	type frame struct {
		id    string
		mark  OmegaMarking
		depth int
	}
	var firings int64
	truncated := false

	var visit func(m OmegaMarking, depth int, stack []frame) string
	visit = func(m OmegaMarking, depth int, stack []frame) string {
		id := addNode(m, depth)
		// Already present: merge instead of revisiting.
		if len(res.Nodes) > 0 && res.Nodes[len(res.Nodes)-1].ID != id {
			return id
		}

		var enabled []string
		for _, t := range n.Transitions {
			if n.IsOmegaEnabled(t.ID, m) {
				enabled = append(enabled, t.ID)
			}
		}
		sort.Strings(enabled)

		for _, tID := range enabled {
			if firings >= firingsBudget {
				truncated = true
				continue
			}
			next0 := n.fireOmega(tID, m)
			firings++
			next := cloneOmega(next0)

			// Karp-Miller acceleration against every strict-covering
			// ancestor on the current path.
			for _, an := range stack {
				le, lt := omegaLE(an.mark, next)
				if le && lt {
					for _, p := range n.Places {
						if next[p.ID] != Omega && next[p.ID] > an.mark[p.ID] {
							next[p.ID] = Omega
							res.Growth = append(res.Growth, OmegaGrowth{
								Place: p.ID, From: an.id, To: id, Transition: tID,
								Detail: fmt.Sprintf("marking dominates ancestor %s at %s; unbounded growth accelerated to omega", an.id, p.ID),
							})
							emit(Event{Type: EvFiring, State: an.id, Next: id, Trans: tID,
								Data: map[string]string{"omega_accelerated_place": p.ID}})
						}
					}
				}
			}

			childKey := n.omegaKey(next)
			childID, known := res.nodeByKey[childKey]
			if !known {
				childID = visit(next, depth+1, append(stack, frame{id: id, mark: cloneOmega(m), depth: depth}))
			}
			kind := "normal"
			if childID == id {
				kind = "self_loop"
			}
			res.Edges = append(res.Edges, CoverEdge{
				ID: fmt.Sprintf("ce%05d", len(res.Edges)+1), From: id, To: childID, Transition: tID, Kind: kind,
			})
		}
		return id
	}

	startOmega := OmegaMarking{}
	for _, p := range n.Places {
		startOmega[p.ID] = start[p.ID]
	}
	visit(startOmega, 0, nil)

	// Cover relations over concrete BFS states: m1 covers m2 iff all
	// places satisfy m1 >= m2.
	for i := range concrete {
		for j := range concrete {
			if i == j {
				continue
			}
			strict, ok := concreteCovers(concrete[i].Marking, concrete[j].Marking)
			if ok {
				res.Cover = append(res.Cover, CoverPair{From: concrete[i].ID, To: concrete[j].ID, Strict: strict})
			}
		}
	}
	sort.SliceStable(res.Cover, func(i, j int) bool {
		if res.Cover[i].From != res.Cover[j].From {
			return res.Cover[i].From < res.Cover[j].From
		}
		return res.Cover[i].To < res.Cover[j].To
	})
	if truncated {
		emit(Event{Type: EvBudgetExhausted, Detail: fmt.Sprintf("coverability budget %d exhausted", firingsBudget)})
		emit(Event{Type: EvTruncated, Detail: "coverability graph truncated"})
	}
	return res, nil
}

func cloneOmega(m OmegaMarking) OmegaMarking {
	c := make(OmegaMarking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// omegaLE reports (<= everywhere, < somewhere) treating Omega as top.
func omegaLE(a, b OmegaMarking) (bool, bool) {
	strict := false
	for k, bv := range b {
		av, ok := a[k]
		if !ok {
			av = 0
		}
		if av == Omega {
			if bv != Omega {
				return false, false
			}
			continue
		}
		if bv == Omega {
			strict = true
			continue
		}
		if av > bv {
			return false, false
		}
		if av < bv {
			strict = true
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			if a[k] != 0 && a[k] != Omega {
				return false, false
			}
		}
	}
	return true, strict
}

// CoversConcrete reports whether concrete marking m covers other.
func CoversConcrete(m, other Marking) (bool, bool) {
	return concreteCovers(m, other)
}

func concreteCovers(m, other Marking) (strict bool, ok bool) {
	places := map[string]bool{}
	for k := range m {
		places[k] = true
	}
	for k := range other {
		places[k] = true
	}
	for k := range places {
		if m[k] < other[k] {
			return false, false
		}
		if m[k] > other[k] {
			strict = true
		}
	}
	return strict, true
}
