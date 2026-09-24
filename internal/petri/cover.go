package petri

import "sort"

// Omega denotes an unbounded token count (omega in a Karp-Miller tree).
const Omega int64 = -1

// OmegaMarking allows place counts to be Omega.
type OmegaMarking map[string]int64

// CoverEdge is one edge of the coverability tree. Accelerated edges carry
// the pump sequence that drives an omega component.
type CoverEdge struct {
	FromKey string   `json:"from_key"`
	ToKey   string   `json:"to_key"`
	TransID string   `json:"transition_id"`
	Pump    []string `json:"pump,omitempty"`
}

// CoverNode is a node of the Karp-Miller coverability tree.
type CoverNode struct {
	Key     string       `json:"key"`
	Marking OmegaMarking `json:"marking"`
	Parent  string       `json:"parent,omitempty"`
	TransID string       `json:"transition_id,omitempty"`
	Pump    []string     `json:"pump,omitempty"`
}

// CoverResult reports whether a query marking is coverable.
type CoverResult struct {
	Coverable       bool                   `json:"coverable"`
	Query           Marking                `json:"query"`
	Covering        OmegaMarking           `json:"covering,omitempty"`
	Nodes           []CoverNode            `json:"nodes"`
	Edges           []CoverEdge            `json:"edges"`
	OmegaPlaces     []string               `json:"omega_places,omitempty"`
	Witness         []CoverEdge            `json:"witness,omitempty"`
	AcceleratedFrom []string               `json:"accelerated_from,omitempty"`
}

// Covers reports whether finite marking a covers b componentwise
// (a(p) >= b(p) for every place).
func Covers(a, b Marking) bool {
	for p, v := range b {
		if v != 0 && a[p] < v {
			return false
		}
	}
	return true
}

func omegaCovers(a OmegaMarking, b Marking) bool {
	for p, v := range b {
		if v == 0 {
			continue
		}
		av, ok := a[p]
		if !ok || (av != Omega && av < v) {
			return false
		}
	}
	return true
}

// Coverability builds a Karp-Miller coverability tree under the same
// three-clause enablement rule. Capacities cap components: an omega in a
// finite-capacity place cannot occur (producing into it stays disabled),
// while unbounded / unknown-capacity places may be accelerated to omega.
func (m *Model) Coverability(initial, query Marking) *CoverResult {
	res := &CoverResult{Query: query}
	root := OmegaMarking{}
	for _, p := range m.placeOrder {
		root[p] = initial[p]
	}
	rkey := m.canonOmega(root)
	nodes := []CoverNode{{Key: rkey, Marking: root}}
	nodeIdx := map[string]int{rkey: 0}
	var edges []CoverEdge

	type frame struct{ key string }
	stack := []frame{{rkey}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		idx := nodeIdx[top.key]
		cur := nodes[idx]

		// Duplicate of an earlier (non-ancestor) node: no expansion.
		for j := 0; j < idx; j++ {
			if omegaEqual(nodes[j].Marking, cur.Marking) {
				goto done
			}
		}
		// Ancestor dominance check + acceleration.
		for a := cur.Parent; a != ""; {
			pi := nodeIdx[a]
			anc := nodes[pi]
			if omegaLE(anc.Marking, cur.Marking) && !omegaEqual(anc.Marking, cur.Marking) {
				accelerated := false
				for _, p := range m.placeOrder {
					if anc.Marking[p] != Omega && cur.Marking[p] != Omega &&
						anc.Marking[p] < cur.Marking[p] {
						cur.Marking[p] = Omega
						accelerated = true
					}
				}
				nodes[idx].Marking = cur.Marking
				if accelerated {
					res.AcceleratedFrom = append(res.AcceleratedFrom, a, cur.Key)
				}
			}
			a = nodes[pi].Parent
		}
		for _, t := range m.Transitions {
			succ, ok := omegaFire(m, cur.Marking, t.ID)
			if !ok {
				continue
			}
			skey := m.canonOmega(succ)
			var pump []string
			if a := cur.Parent; a != "" && cur.TransID != "" {
				pump = collectPump(nodes, idx)
			}
			if _, exists := nodeIdx[skey]; !exists {
				nodes = append(nodes, CoverNode{
					Key: skey, Marking: succ, Parent: cur.Key,
					TransID: t.ID, Pump: pump,
				})
				nodeIdx[skey] = len(nodes) - 1
				stack = append(stack, frame{skey})
			}
			edges = append(edges, CoverEdge{
				FromKey: cur.Key, ToKey: skey, TransID: t.ID, Pump: pump,
			})
		}
	done:
	}

	res.Nodes = nodes
	res.Edges = edges
	omegaSet := map[string]bool{}
	for _, n := range nodes {
		if omegaCovers(n.Marking, query) {
			res.Coverable = true
			res.Covering = n.Marking
			for p, v := range n.Marking {
				if v == Omega {
					omegaSet[p] = true
				}
			}
			res.Witness = coverWitness(nodes, edges, nodeIdx[n.Key])
			break
		}
	}
	for p := range omegaSet {
		res.OmegaPlaces = append(res.OmegaPlaces, p)
	}
	sort.Strings(res.OmegaPlaces)
	return res
}

func collectPump(nodes []CoverNode, idx int) []string {
	var pump []string
	for i := idx; nodes[i].Parent != ""; {
		pump = append([]string{nodes[i].TransID}, pump...)
		pi, ok := findKey(nodes, nodes[i].Parent)
		if !ok {
			break
		}
		i = pi
	}
	return pump
}

func findKey(nodes []CoverNode, key string) (int, bool) {
	for i := range nodes {
		if nodes[i].Key == key {
			return i, true
		}
	}
	return 0, false
}

func coverWitness(nodes []CoverNode, edges []CoverEdge, idx int) []CoverEdge {
	chain := map[string]bool{}
	for i := idx; nodes[i].Parent != ""; {
		chain[nodes[i].Key] = true
		pi, _ := findKey(nodes, nodes[i].Parent)
		i = pi
	}
	var out []CoverEdge
	for _, e := range edges {
		if chain[e.ToKey] {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, _ := findKey(nodes, out[i].ToKey)
		dj, _ := findKey(nodes, out[j].ToKey)
		return di < dj
	})
	return out
}

// omegaFire computes the successor under omega semantics. It applies the
// input/inhibitor/capacity clauses against omega components.
func omegaFire(m *Model, marking OmegaMarking, tID string) (OmegaMarking, bool) {
	next := OmegaMarking{}
	for _, p := range m.placeOrder {
		next[p] = marking[p]
	}
	for _, a := range m.inArc[tID] {
		v := marking[a.Src]
		if v == Omega {
			continue
		}
		if v < a.Weight {
			return nil, false
		}
		next[a.Src] = v - a.Weight
	}
	for _, a := range m.inhibArc[tID] {
		v := marking[a.Src]
		if v == Omega || v >= a.Weight {
			return nil, false
		}
	}
	caps := map[string]*int64{}
	for _, p := range m.Places {
		caps[p.ID] = p.Cap
	}
	for _, a := range m.outArc[tID] {
		v := next[a.Dst]
		if v == Omega {
			if c := caps[a.Dst]; c != nil {
				return nil, false
			}
			continue
		}
		v += a.Weight
		if c := caps[a.Dst]; c != nil && v > *c {
			return nil, false
		}
		next[a.Dst] = v
	}
	return next, true
}

func (m *Model) canonOmega(marking OmegaMarking) string {
	ids := make([]string, 0, len(marking))
	for id := range marking {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	buf := make([]byte, 0, 8*len(ids))
	for i, id := range ids {
		if i > 0 {
			buf = append(buf, '|')
		}
		buf = append(buf, id...)
		buf = append(buf, ':')
		if marking[id] == Omega {
			buf = append(buf, 'w')
		} else {
			buf = appendInt(buf, marking[id])
		}
	}
	return string(buf)
}

func omegaEqual(a, b OmegaMarking) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// omegaLE reports a <= b componentwise with omega as the greatest element.
func omegaLE(a, b OmegaMarking) bool {
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if av == Omega && bv != Omega {
			return false
		}
		if av != Omega && bv != Omega && av > bv {
			return false
		}
	}
	return true
}
