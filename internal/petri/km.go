package petri

import "fmt"

type omegaMark struct{ n []int }

const omegaVal = -1

func (c *Compiled) toOmega(m Marking) omegaMark {
	o := omegaMark{n: make([]int, len(c.PlaceOrder))}
	for i, p := range c.PlaceOrder {
		o.n[i] = m[p]
	}
	return o
}

func (c *Compiled) omegaCanon(o omegaMark) string {
	s := ""
	for i, p := range c.PlaceOrder {
		if i > 0 {
			s += "|"
		}
		if o.n[i] == omegaVal {
			s += p + "=ω"
		} else {
			s += fmt.Sprintf("%s=%d", p, o.n[i])
		}
	}
	return s
}

// omegaLE returns a<=b component-wise (ω compatible) and whether it is strict.
func omegaLE(a, b omegaMark) (bool, bool) {
	strict := false
	for i := range a.n {
		if a.n[i] == omegaVal && b.n[i] == omegaVal {
			continue
		}
		if a.n[i] == omegaVal { // ω <= finite is false
			return false, false
		}
		if b.n[i] == omegaVal { // finite < ω
			strict = true
			continue
		}
		if a.n[i] > b.n[i] {
			return false, false
		}
		if a.n[i] < b.n[i] {
			strict = true
		}
	}
	return true, strict
}

// enabledOmega treats ω input places as unlimited; ω never appears on a
// capacity-bounded place because firing clamps those places.
func (c *Compiled) enabledOmega(t string, o omegaMark) bool {
	m := Marking{}
	for i, p := range c.PlaceOrder {
		if o.n[i] == omegaVal {
			m[p] = 1 << 30
		} else {
			m[p] = o.n[i]
		}
	}
	ok, _ := c.Enabled(t, m)
	return ok
}

func (c *Compiled) fireOmega(t string, o omegaMark) omegaMark {
	no := omegaMark{n: append([]int{}, o.n...)}
	for _, a := range c.InputArc[t] {
		if a.Inhibitor {
			continue
		}
		idx := c.placeIndex(a.From)
		if idx >= 0 && no.n[idx] != omegaVal {
			no.n[idx] -= a.Weight
		}
	}
	for _, a := range c.OutputArc[t] {
		idx := c.placeIndex(a.To)
		if idx < 0 || no.n[idx] == omegaVal {
			continue
		}
		no.n[idx] += a.Weight
		if cap := c.Places[a.To].Capacity; cap != nil && no.n[idx] > *cap {
			no.n[idx] = *cap
		}
	}
	return no
}

func (c *Compiled) placeIndex(p string) int {
	for i, q := range c.PlaceOrder {
		if q == p {
			return i
		}
	}
	return -1
}

type kmFrame struct {
	marking omegaMark
	depth   int
}

// karpMiller builds the Karp-Miller coverability tree with strict on-path
// ancestor comparisons, so each ω promotion reflects genuine unbounded growth.
func (c *Compiled) karpMiller(budget int) ([]Promotion, bool) {
	var promotions []Promotion
	unbounded := false
	globalSeen := map[string]bool{}
	steps := 0

	var dfs func(node kmFrame, path []omegaMark)
	dfs = func(node kmFrame, path []omegaMark) {
		if steps > budget*128 || node.depth > budget*8 {
			return
		}
		key := c.omegaCanon(node.marking)
		if globalSeen[key] {
			return
		}
		promoted := false
		for k := len(path) - 1; k >= 0; k-- {
			anc := path[k]
			if le, strict := omegaLE(anc, node.marking); le && strict {
				for i := range node.marking.n {
					if anc.n[i] != omegaVal && node.marking.n[i] != omegaVal && anc.n[i] < node.marking.n[i] {
						node.marking.n[i] = omegaVal
						unbounded = true
						promoted = true
						promotions = append(promotions, Promotion{
							Place:      c.PlaceOrder[i],
							Ancestor:   StateID(c.Canonical(c.finiteFromOmega(anc))),
							Current:    "km-" + sha8(key),
							OmegaAfter: c.omegaCanon(node.marking),
						})
					}
				}
				break
			}
		}
		key = c.omegaCanon(node.marking)
		if promoted && globalSeen[key] {
			return
		}
		globalSeen[key] = true
		nextPath := append(append([]omegaMark{}, path...), node.marking)
		for _, t := range c.TransOrder {
			steps++
			if !c.enabledOmega(t, node.marking) {
				continue
			}
			succ := c.fireOmega(t, node.marking)
			dfs(kmFrame{marking: succ, depth: node.depth + 1}, nextPath)
		}
	}
	dfs(kmFrame{marking: c.toOmega(c.Initial)}, nil)
	return promotions, unbounded
}

func (c *Compiled) finiteFromOmega(o omegaMark) Marking {
	m := Marking{}
	for i, p := range c.PlaceOrder {
		if o.n[i] == omegaVal {
			m[p] = 0
		} else {
			m[p] = o.n[i]
		}
	}
	return m
}
