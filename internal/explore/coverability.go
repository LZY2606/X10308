package explore

import (
	"sort"
	"strconv"

	"pnwork/internal/pnnet"
)

// Omega marks an unbounded component in a generalized coverability marking.
const Omega int64 = -1

// OmegaMarking is a generalized marking; components may be Omega (ω).
type OmegaMarking map[string]int64

func omKey(m OmegaMarking, placeIDs []string) string {
	parts := make([]string, 0, len(placeIDs))
	for _, pid := range placeIDs {
		v, ok := m[pid]
		if !ok {
			v = 0
		}
		if v == Omega {
			parts = append(parts, pid+"=w")
		} else {
			parts = append(parts, pid+"="+strconv.FormatInt(v, 10))
		}
	}
	return strings2(parts)
}

func strings2(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

func coversOmega(m, other OmegaMarking) bool {
	for pid, v := range other {
		mv, ok := m[pid]
		if !ok {
			mv = 0
		}
		if mv == Omega {
			continue
		}
		if v == Omega || mv < v {
			return false
		}
	}
	return true
}

func toOmega(m pnnet.Marking) OmegaMarking {
	o := OmegaMarking{}
	for k, v := range m {
		o[k] = v
	}
	return o
}

// concreteLE reports concrete <= omega componentwise.
func concreteLE(concrete pnnet.Marking, omega OmegaMarking) bool {
	for pid, v := range concrete {
		ov, ok := omega[pid]
		if !ok {
			ov = 0
		}
		if ov == Omega {
			continue
		}
		if v > ov {
			return false
		}
	}
	return true
}

// CoverVerdict is the result of coverability-tree reachability analysis.
type CoverVerdict struct {
	Verdict     Verdict        `json:"verdict"`
	ExactReach  bool           `json:"exact_reachable"`
	Covered     bool           `json:"covered"`
	NodeCount   int            `json:"node_count"`
	Paths       [][]Step       `json:"paths,omitempty"`
	Witness     *pnnet.Marking `json:"witness,omitempty"`
	OmegaPlaces []string       `json:"omega_places,omitempty"`
	Frontier    []FrontierItem `json:"frontier,omitempty"`
	BudgetHit   bool           `json:"budget_hit"`
}

type cnode struct {
	marking  OmegaMarking
	parent   string
	tid      string
	depth    int
	concrete pnnet.Marking
}

// accelerablePlaces are the only places allowed to become ω: they must be
// unbounded AND must never feed an inhibitor arc. Acceleration is unsound
// otherwise, because arbitrarily many tokens on an inhibitor source would
// disable transitions instead of enabling more behavior, and finite-capacity
// places simply cannot grow without bound.
func (e *Engine) accelerablePlaces() map[string]bool {
	places := e.net.PlaceByID()
	out := map[string]bool{}
	for _, p := range e.net.Places {
		out[p.ID] = !p.HasCapacity()
	}
	for _, a := range e.net.Arcs {
		if a.Kind == pnnet.ArcInhibitor {
			out[a.From] = false
		}
	}
	_ = places
	return out
}

// CoverReach answers a bounded reachability/coverability question using a
// Karp-Miller-style coverability tree with inhibitor-safe acceleration.
func (e *Engine) CoverReach(initial, target pnnet.Marking) *CoverVerdict {
	res := &CoverVerdict{}
	dfs := e.cfg.Strategy == StrategyDFS
	accel := e.accelerablePlaces()

	var list []*cnode
	push := func(n *cnode) { list = append(list, n) }
	pop := func() *cnode {
		if dfs {
			last := len(list) - 1
			n := list[last]
			list = list[:last]
			return n
		}
		n := list[0]
		list = list[1:]
		return n
	}

	start := &cnode{marking: toOmega(initial), concrete: initial.Clone()}
	key0 := omKey(start.marking, e.placeIDs)
	nodes := map[string]*cnode{key0: start}
	parentOf := map[string]string{}
	push(start)
	targetKey := target.Canonical(e.placeIDs)
	omegaSet := map[string]bool{}

	var exactNode string
	expanded := 0
	for len(list) > 0 {
		if e.cfg.Budget > 0 && int64(expanded) >= e.cfg.Budget {
			break
		}
		cur := pop()
		ck := omKey(cur.marking, e.placeIDs)
		expanded++
		if ck == targetKey && exactNode == "" {
			exactNode = ck
		}
		for _, tid := range e.enabledOmega(cur.marking) {
			next := e.fireOmega(cur.marking, tid)
			// Karp-Miller acceleration against every strict ancestor, but
			// restricted to accelerable places (inhibitor/capacity safe).
			chain := ck
			for chain != "" {
				anc, ok := nodes[chain]
				if !ok {
					break
				}
				if coversOmega(next, anc.marking) &&
					omKey(next, e.placeIDs) != omKey(anc.marking, e.placeIDs) {
					for _, pid := range e.placeIDs {
						if !accel[pid] {
							continue
						}
						av := anc.marking[pid]
						if av == Omega {
							continue
						}
						nv, ok2 := next[pid]
						if !ok2 {
							nv = 0
						}
						if nv != Omega && nv > av {
							next[pid] = Omega
							omegaSet[pid] = true
						}
					}
				}
				chain = parentOf[chain]
			}
			nk := omKey(next, e.placeIDs)
			if _, ok := nodes[nk]; ok {
				continue
			}
			nn := &cnode{marking: next, parent: ck, tid: tid, depth: cur.depth + 1,
				concrete: representative(next)}
			nodes[nk] = nn
			parentOf[nk] = ck
			push(nn)
			if nk == targetKey && exactNode == "" {
				exactNode = nk
			}
		}
	}

	res.NodeCount = len(nodes)
	for p := range omegaSet {
		res.OmegaPlaces = append(res.OmegaPlaces, p)
	}
	sort.Strings(res.OmegaPlaces)

	if exactNode != "" {
		res.Verdict = VerdictReachable
		res.ExactReach = true
		res.Paths = e.coverPath(nodes, parentOf, exactNode)
		return res
	}
	if initial.Covers(target) && initial.Canonical(e.placeIDs) != targetKey {
		// covered by start but not equal
	}
	for k, n := range nodes {
		if concreteLE(target, n.marking) {
			res.Covered = true
			w := n.concrete.Clone()
			res.Witness = &w
			res.Paths = e.coverPath(nodes, parentOf, k)
			break
		}
	}
	if e.cfg.Budget > 0 && int64(expanded) >= e.cfg.Budget && len(list) > 0 {
		// Exact match would have been returned above. Partial coverage of an
		// incompletely explored tree is not a final verdict: return unknown and
		// retain the frontier.
		res.Covered = false
		res.Paths = nil
		res.Witness = nil
		res.Verdict = VerdictUnknown
		res.BudgetHit = true
		for _, n := range list {
			res.Frontier = append(res.Frontier, FrontierItem{
				Marking: n.concrete.Clone(), Parent: n.parent,
				Transition: n.tid, Depth: n.depth,
			})
		}
		sort.Slice(res.Frontier, func(i, j int) bool {
			return res.Frontier[i].Marking.Canonical(e.placeIDs) <
				res.Frontier[j].Marking.Canonical(e.placeIDs)
		})
		return res
	}
	if res.Covered {
		res.Verdict = VerdictReachable
		return res
	}
	res.Verdict = VerdictUnreachable
	return res
}

func representative(o OmegaMarking) pnnet.Marking {
	m := pnnet.Marking{}
	for k, v := range o {
		if v == Omega {
			m[k] = 0
		} else {
			m[k] = v
		}
	}
	return m
}

func (e *Engine) coverPath(nodes map[string]*cnode, parentOf map[string]string, target string) [][]Step {
	var path []Step
	cur := target
	for cur != "" {
		n, ok := nodes[cur]
		if !ok || n.tid == "" {
			break
		}
		pn2 := nodes[parentOf[cur]]
		if pn2 == nil {
			break
		}
		path = append([]Step{{
			Transition: n.tid,
			From:       pn2.concrete.Clone(),
			To:         n.concrete.Clone(),
		}}, path...)
		cur = parentOf[cur]
	}
	if len(path) == 0 {
		return nil
	}
	return [][]Step{path}
}

// enabledOmega enables transitions on generalized markings. ω places satisfy
// any finite input requirement; an ω inhibitor source (which acceleration now
// forbids) would block, handled defensively.
func (e *Engine) enabledOmega(m OmegaMarking) []string {
	var out []string
	for _, tid := range e.net.SortedTransitionIDs() {
		if e.enabledOmegaT(tid, m) {
			out = append(out, tid)
		}
	}
	return out
}

func (e *Engine) enabledOmegaT(tid string, m OmegaMarking) bool {
	for _, ref := range e.net.Preset(tid) {
		if ref.Arc.Weight.Value == nil {
			return false
		}
		v, ok := m[ref.Arc.From]
		if !ok {
			v = 0
		}
		if v != Omega && v < ref.Weight {
			return false
		}
	}
	for _, ref := range e.net.Inhibitors(tid) {
		if ref.Arc.Weight.Value == nil {
			return false
		}
		v, ok := m[ref.Arc.From]
		if !ok {
			v = 0
		}
		if v == Omega || v >= ref.Weight {
			return false
		}
	}
	next := e.fireOmega(m, tid)
	places := e.net.PlaceByID()
	for pid := range next {
		d := next[pid]
		if d == Omega {
			continue
		}
		ov := m[pid]
		if ov == Omega {
			continue
		}
		p, ok := places[pid]
		if !ok || !p.HasCapacity() {
			continue
		}
		if d > ov && d > *p.Capacity.Value {
			return false
		}
	}
	return true
}

func (e *Engine) fireOmega(m OmegaMarking, tid string) OmegaMarking {
	next := OmegaMarking{}
	for k, v := range m {
		next[k] = v
	}
	for _, ref := range e.net.Postset(tid) {
		cur, ok := next[ref.Arc.To]
		if !ok {
			cur = 0
		}
		if cur == Omega {
			continue
		}
		next[ref.Arc.To] = cur + ref.Weight
	}
	for _, ref := range e.net.Preset(tid) {
		cur, ok := next[ref.Arc.From]
		if !ok {
			cur = 0
		}
		if cur == Omega {
			continue
		}
		next[ref.Arc.From] = cur - ref.Weight
	}
	return next
}
