package petri

import (
	"fmt"
	"strings"
)

// Marking is token counts per place. A nil entry and an explicit zero are
// different inputs; canonicalization never merges markings whose tokens
// differ.
type Marking map[string]int

// Canonical renders a marking over the fixed place order, so identical
// markings share identity and distinct markings never do.
func (c *Compiled) Canonical(m Marking) string {
	var b strings.Builder
	for i, p := range c.PlaceOrder {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(fmt.Sprintf("%s=%d", p, m[p]))
	}
	return b.String()
}

func (c *Compiled) parseCanonical(s string) Marking {
	m := Marking{}
	for _, kv := range strings.Split(s, "|") {
		var p string
		var n int
		if _, err := fmt.Sscanf(kv, "%[^=]=%d", &p, &n); err == nil {
			m[p] = n
		}
	}
	return m
}

// Violation describes why capacity would be exceeded by a firing.
type Violation struct {
	Place    string `json:"place"`
	After    int    `json:"after"`
	Capacity int    `json:"capacity"`
}

// Enabled reports enablement considering inputs, inhibitor arcs and capacity
// simultaneously. The returned reasons are the machine-readable judgement
// basis for disabled transitions.
func (c *Compiled) Enabled(t string, m Marking) (bool, []map[string]any) {
	var reasons []map[string]any
	for _, a := range c.InputArc[t] {
		if a.Inhibitor {
			if m[a.From] >= a.Weight {
				reasons = append(reasons, map[string]any{
					"kind": "inhibitor-blocked", "arc": a.ID, "place": a.From,
					"tokens": m[a.From], "threshold": a.Weight,
				})
			}
			continue
		}
		if m[a.From] < a.Weight {
			reasons = append(reasons, map[string]any{
				"kind": "input-insufficient", "arc": a.ID, "place": a.From,
				"tokens": m[a.From], "required": a.Weight,
			})
		}
	}
	after := map[string]int{}
	for _, a := range c.OutputArc[t] {
		after[a.To] += a.Weight
	}
	for _, a := range c.InputArc[t] {
		if !a.Inhibitor {
			after[a.From] -= a.Weight
		}
	}
	for _, pid := range c.PlaceOrder {
		pi := c.Places[pid]
		if pi.Capacity == nil {
			continue
		}
		v := m[pid] + after[pid]
		if v > *pi.Capacity {
			reasons = append(reasons, map[string]any{
				"kind": "capacity-exceeded", "place": pid,
				"after": v, "capacity": *pi.Capacity,
			})
		}
	}
	return len(reasons) == 0, reasons
}

// Fire assumes enablement and returns the successor marking.
func (c *Compiled) Fire(t string, m Marking) Marking {
	n := Marking{}
	for _, p := range c.PlaceOrder {
		n[p] = m[p]
	}
	for _, a := range c.InputArc[t] {
		if !a.Inhibitor {
			n[a.From] -= a.Weight
		}
	}
	for _, a := range c.OutputArc[t] {
		n[a.To] += a.Weight
	}
	return n
}

// Successors returns deterministically ordered enabled transitions and successors.
func (c *Compiled) Successors(m Marking) []Successor {
	var out []Successor
	for _, t := range c.TransOrder {
		ok, _ := c.Enabled(t, m)
		if ok {
			out = append(out, Successor{Transition: t, Marking: c.Fire(t, m)})
		}
	}
	return out
}

type Successor struct {
	Transition string
	Marking    Marking
}
