package petri

import (
	"strings"
)

// Omega is the coverability symbol "unbounded".
const Omega int64 = -1

// OmegaMarking labels coverability-tree nodes; a component may be Omega.
type OmegaMarking map[string]int64

// FromMarking lifts a finite marking into an omega marking.
func FromMarking(m Marking) OmegaMarking {
	o := OmegaMarking{}
	for k, v := range m {
		o[k] = v
	}
	return o
}

// Finite reports whether the label has no omega component.
func (o OmegaMarking) Finite() bool {
	for _, v := range o {
		if v == Omega {
			return false
		}
	}
	return true
}

// ToMarking returns the finite marking; ok is false if any component is omega.
func (o OmegaMarking) ToMarking() (Marking, bool) {
	if !o.Finite() {
		return nil, false
	}
	m := Marking{}
	for k, v := range o {
		m[k] = v
	}
	return m, true
}

// Canonical serializes an omega label deterministically over place order.
func (n *Net) CanonicalOmega(o OmegaMarking) string {
	var b strings.Builder
	for i, pid := range n.PlaceOrder {
		if i > 0 {
			b.WriteByte('|')
		}
		if o[pid] == Omega {
			b.WriteString(pid)
			b.WriteString("=omega")
		} else {
			b.WriteString(pid)
			b.WriteByte("=")
			writeInt(&b, o[pid])
			b.WriteString("token")
		}
	}
	return b.String()
}

// Covers reports a >= b componentwise (omega covers everything).
func (a OmegaMarking) Covers(b OmegaMarking) bool {
	places := map[string]bool{}
	for p := range a {
		places[p] = true
	}
	for p := range b {
		places[p] = true
	}
	for p := range places {
		av, bv := a[p], b[p]
		if av == Omega {
			continue
		}
		if bv == Omega || av < bv {
			return false
		}
	}
	return true
}

// StrictlyDominates reports a > b: a covers b, is unequal, and carries an
// omega where b is finite (the Karp-Miller acceleration precondition).
func (a OmegaMarking) StrictlyDominates(b OmegaMarking) bool {
	if !a.Covers(b) {
		return false
	}
	places := map[string]bool{}
	for p := range a {
		places[p] = true
	}
	for p := range b {
		places[p] = true
	}
	equal, accelerated := true, false
	for p := range places {
		switch {
		case a[p] == Omega && b[p] != Omega:
			equal = false
			accelerated = true
		case a[p] != b[p]:
			equal = false
		}
	}
	return !equal && accelerated
}

// Clone copies the label.
func (o OmegaMarking) Clone() OmegaMarking {
	c := OmegaMarking{}
	for k, v := range o {
		c[k] = v
	}
	return c
}
