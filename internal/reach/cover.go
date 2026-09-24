package reach

import (
	"fmt"

	"petriworkspace/internal/petrinet"
)

// Omega marks an unbounded coordinate in a coverability marking.
const Omega int64 = -1

// OmegaMarking uses Omega (-1) for coordinates that can grow without
// bound; all other coordinates are ordinary non-negative token counts.
type OmegaMarking map[string]int64

// IsOmega reports whether a coordinate is unbounded.
func IsOmega(v int64) bool { return v == Omega }

func (m OmegaMarking) key() string {
	return omegaKey(m)
}

func omegaKey(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort-free: reuse fmt with ordered map via petrinet key
	// conventions is not available for omega; do a small sort here.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ";"
		}
		if m[k] == Omega {
			out += k + "=w"
		} else {
			out += fmt.Sprintf("%s=%d", k, m[k])
		}
	}
	return out
}

// CoverNode is a node of the Karp-Miller coverability tree.
type CoverNode struct {
	ID      string       `json:"id"`
	Marking OmegaMarking `json:"marking"`
	PathLen int          `json:"path_len"`
}

// CoverEdge is a firing recorded between coverability nodes.
type CoverEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

// CoverResult is the coverability tree plus a target verdict.
type CoverResult struct {
	Status    Status         `json:"status"`
	Nodes     []CoverNode    `json:"nodes"`
	Edges     []CoverEdge    `json:"edges"`
	Frontier  []FrontierItem `json:"frontier,omitempty"`
	Expanded  int            `json:"expanded"`
	Budget    int            `json:"budget"`
	Unbounded []string       `json:"unbounded_places"`
	Witness   []Step         `json:"witness,omitempty"`
}

type coverFrame struct {
	key     string
	marking OmegaMarking
	parent  string
	via     string
	pathLen int
}

// Cover builds a Karp-Miller coverability tree. Coordinates that dominate
// an ancestor while the path between them is token-producing are promoted
// to Omega, which witnesses unbounded growth. If the expansion budget is
// reached the result is Unknown and the pending frontier is retained.
func Cover(net *petrinet.Net, initial petrinet.Marking, target petrinet.Marking, budget int) *CoverResult {
	res := &CoverResult{Budget: budget}

	root := OmegaMarking{}
	for k, v := range initial {
		root[k] = v
	}
	rootKey := root.key()
	nodes := map[string]*coverFrame{rootKey: {key: rootKey, marking: root, pathLen: 0}}
	nodeOrder := []string{rootKey}
	edges := []CoverEdge{}
	stack := []*coverFrame{nodes[rootKey]}
	unbounded := map[string]bool{}

	fireOmega := func(t string, m OmegaMarking) (OmegaMarking, bool) {
		next := OmegaMarking{}
		for k, v := range m {
			next[k] = v
		}
		// Enablement on omega coordinates is treated as satisfied for
		// inputs and inhibitors, because arbitrarily many tokens may
		// exist; finite coordinates are checked normally.
		for _, a := range net.InputArcs(t) {
			cv, ok := next[a.Place]
			if ok && !IsOmega(cv) && cv < a.Weight {
				return nil, false
			}
		}
		for _, a := range net.InhibitorArcs(t) {
			cv, ok := next[a.Place]
			if ok && !IsOmega(cv) && cv >= a.Weight {
				return nil, false
			}
			// An inhibitor on an omega coordinate would make the
			// transition disabled: infinitely many tokens are present.
			if ok && IsOmega(cv) {
				return nil, false
			}
		}
		for _, a := range net.InputArcs(t) {
			if IsOmega(next[a.Place]) {
				continue
			}
			next[a.Place] -= a.Weight
		}
		for _, a := range net.OutputArcs(t) {
			if IsOmega(next[a.Place]) {
				continue
			}
			// If place already carries omega it stays omega.
			if v, ok := next[a.Place]; ok && v == Omega {
				continue
			}
			next[a.Place] += a.Weight
		}
		return next, true
	}

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		res.Expanded++

		var enabled []string
		for _, t := range net.Transitions {
			if _, ok := fireOmega(t.ID, cur.marking); ok {
				enabled = append(enabled, t.ID)
			}
		}
		if len(enabled) == 0 {
			// terminal leaf, no successor to push
		}

		for _, t := range enabled {
			next, _ := fireOmega(t, cur.marking)

			// Karp-Miller acceleration against strict ancestors.
			if anc := dominatingStrictAncestor(net, nodes, cur, next); anc != nil {
				for _, p := range net.Places {
					if next[p.ID] != Omega && anc.marking[p.ID] != Omega && next[p.ID] > anc.marking[p.ID] {
						next[p.ID] = Omega
						unbounded[p.ID] = true
					}
				}
			}

			nk := next.key()
			edges = append(edges, CoverEdge{From: cur.key, To: nk, Transition: t})
			if _, seen := nodes[nk]; seen {
				continue
			}
			nf := &coverFrame{key: nk, marking: next, parent: cur.key, via: t, pathLen: cur.pathLen + 1}
			nodes[nk] = nf
			nodeOrder = append(nodeOrder, nk)
			stack = append(stack, nf)
		}

		if budget > 0 && res.Expanded >= budget && len(stack) > 0 {
			res.Status = StatusUnknown
			for _, f := range stack {
				fi := FrontierItem{Marking: petrinet.Marking{}, PathLen: f.pathLen}
				for k, v := range f.marking {
					if v == Omega {
						fi.Marking[k] = Omega
					} else {
						fi.Marking[k] = v
					}
				}
				res.Frontier = append(res.Frontier, fi)
			}
			break
		}
	}

	for _, nk := range nodeOrder {
		f := nodes[nk]
		node := CoverNode{ID: nk, Marking: OmegaMarking{}, PathLen: f.pathLen}
		for k, v := range f.marking {
			node.Marking[k] = v
		}
		res.Nodes = append(res.Nodes, node)
	}
	res.Edges = edges
	for p := range unbounded {
		res.Unbounded = append(res.Unbounded, p)
	}

	if res.Status == StatusUnknown {
		return res
	}

	if target != nil {
		for _, n := range res.Nodes {
			if coversTarget(n.Marking, target) {
				res.Status = StatusFound
				break
			}
		}
		if res.Status == "" {
			res.Status = StatusNotFound
		}
	} else {
		res.Status = StatusFound
	}
	return res
}

// dominatingStrictAncestor walks the parent chain from cur and returns an
// ancestor whose marking is component-wise <= next with at least one strict
// inequality and with a transition firing possible between them; this is
// the standard monotonicity witness used for omega acceleration.
func dominatingStrictAncestor(net *petrinet.Net, nodes map[string]*coverFrame, cur *coverFrame, next OmegaMarking) *coverFrame {
	visited := map[string]bool{cur.key: true}
	parentKey := cur.parent
	for parentKey != "" && !visited[parentKey] {
		visited[parentKey] = true
		anc, ok := nodes[parentKey]
		if !ok {
			return nil
		}
		strict := false
		possible := true
		for _, p := range net.Places {
			av := anc.marking[p.ID]
			nv, present := next[p.ID]
			if !present {
				nv = 0
			}
			if av == Omega {
				if nv != Omega {
					possible = false
					break
				}
				continue
			}
			if nv == Omega {
				strict = true
				continue
			}
			if nv < av {
				possible = false
				break
			}
			if nv > av {
				strict = true
			}
		}
		if possible && strict {
			return anc
		}
		parentKey = anc.parent
	}
	return nil
}

func coversTarget(node OmegaMarking, target petrinet.Marking) bool {
	for k, v := range target {
		nv, ok := node[k]
		if !ok {
			return false
		}
		if nv == Omega {
			continue
		}
		if nv < v {
			return false
		}
	}
	return true
}
