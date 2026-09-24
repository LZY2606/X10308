// Package petri implements the Petri net domain model and the reachability
// engine. The two hard rules of the workspace live here:
//
//  1. Transition enablement considers inputs, inhibitor arcs and place
//     capacities at the same time.
//  2. State canonicalization never merges markings that differ in tokens;
//     budget exhaustion returns "unknown" and keeps the frontier.
package petri

import (
	"fmt"
	"sort"
	"strings"
)

// ArcKind distinguishes normal arcs from inhibitor arcs.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

// Place is a net place. Capacity is in unit "token"; nil means unbounded.
// X/Y are layout coordinates on the "px" basis used by the SVG renderer.
type Place struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity *int64 `json:"capacity,omitempty"` // unit: token
	X        int    `json:"x"`                  // basis: px
	Y        int    `json:"y"`                  // basis: px
}

// Transition is a net transition.
type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    int    `json:"x"` // basis: px
	Y    int    `json:"y"` // basis: px
}

// Arc is a weighted arc. Weight carries unit "token". Inhibitor arcs run
// from a place to a transition and block firing while tokens >= Weight.
type Arc struct {
	ID     string  `json:"id"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Kind   ArcKind `json:"kind"`
	Weight int64   `json:"weight"` // unit: token
}

// Net is a fully normalized Petri net with an initial marking.
type Net struct {
	Places      []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []Arc        `json:"arcs"`
	Initial     Marking      `json:"initial"` // unit: token per place
}

// Marking maps place ID to token count (unit: token).
type Marking map[string]int64

// Key canonicalizes a marking. Two markings with different token counts
// always produce different keys: canonicalization never merges them.
func (n *Net) Key(m Marking) string {
	var b strings.Builder
	for i, p := range n.Places {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(&b, "%s=%d", p.ID, m[p.ID])
	}
	return b.String()
}

// Equal reports token-level equality over all places of the net.
func (n *Net) Equal(a, b Marking) bool {
	for _, p := range n.Places {
		if a[p.ID] != b[p.ID] {
			return false
		}
	}
	return true
}

// Leq reports componentwise a <= b over all places.
func (n *Net) Leq(a, b Marking) bool {
	for _, p := range n.Places {
		if a[p.ID] > b[p.ID] {
			return false
		}
	}
	return true
}

// Clone copies a marking.
func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (n *Net) place(id string) *Place {
	for i := range n.Places {
		if n.Places[i].ID == id {
			return &n.Places[i]
		}
	}
	return nil
}

// Rules is a versioned rule set. Version 1 is the legacy baseline kept for
// history (inputs only); version 2 is the workspace rule: enablement
// considers inputs + inhibitor arcs + capacities simultaneously.
type Rules struct {
	Version   int  `json:"version"`
	Inhibitor bool `json:"inhibitor"`
	Capacity  bool `json:"capacity"`
}

// RulesFor returns the rule set for a version number.
func RulesFor(version int) Rules {
	switch version {
	case 1:
		return Rules{Version: 1}
	default:
		return Rules{Version: 2, Inhibitor: true, Capacity: true}
	}
}

// Fireable reports whether t is enabled at m under rules, and if not, a
// machine-readable reason listing every blocking condition.
func (n *Net) Fireable(t *Transition, m Marking, rules Rules) (bool, string) {
	var blocks []string
	for _, a := range n.Arcs {
		if a.To != t.ID {
			continue
		}
		switch a.Kind {
		case ArcNormal:
			if m[a.From] < a.Weight {
				blocks = append(blocks, fmt.Sprintf("input:%s needs=%d has=%d", a.From, a.Weight, m[a.From]))
			}
		case ArcInhibitor:
			if rules.Inhibitor && m[a.From] >= a.Weight {
				blocks = append(blocks, fmt.Sprintf("inhibitor:%s tokens=%d threshold=%d", a.From, m[a.From], a.Weight))
			}
		}
	}
	if len(blocks) > 0 {
		return false, strings.Join(blocks, ",")
	}
	if rules.Capacity {
		next := n.Fire(t, m)
		for _, p := range n.Places {
			if p.Capacity != nil && next[p.ID] > *p.Capacity {
				blocks = append(blocks, fmt.Sprintf("capacity:%s would=%d cap=%d", p.ID, next[p.ID], *p.Capacity))
			}
		}
	}
	if len(blocks) > 0 {
		return false, strings.Join(blocks, ",")
	}
	return true, ""
}

// Fire returns the successor marking of firing t at m. It does not check
// enablement; callers use Fireable first.
func (n *Net) Fire(t *Transition, m Marking) Marking {
	next := m.Clone()
	for _, a := range n.Arcs {
		if a.Kind != ArcNormal {
			continue
		}
		if a.To == t.ID {
			next[a.From] -= a.Weight
		}
		if a.From == t.ID {
			next[a.To] += a.Weight
		}
	}
	return next
}

// Validate checks structural integrity of the net.
func (n *Net) Validate() error {
	seen := map[string]bool{}
	for _, p := range n.Places {
		if p.ID == "" || seen[p.ID] {
			return fmt.Errorf("place id %q empty or duplicated", p.ID)
		}
		seen[p.ID] = true
		if p.Capacity != nil && *p.Capacity < 0 {
			return fmt.Errorf("place %s: negative capacity", p.ID)
		}
	}
	for _, t := range n.Transitions {
		if t.ID == "" || seen[t.ID] {
			return fmt.Errorf("transition id %q empty or duplicated", t.ID)
		}
		seen[t.ID] = true
	}
	for _, a := range n.Arcs {
		if !seen[a.From] || !seen[a.To] {
			return fmt.Errorf("arc %s references unknown endpoint", a.ID)
		}
		if a.Weight <= 0 {
			return fmt.Errorf("arc %s: weight must be positive", a.ID)
		}
		if a.Kind == ArcInhibitor {
			if n.place(a.From) == nil {
				return fmt.Errorf("inhibitor arc %s must start at a place", a.ID)
			}
		}
	}
	for id, tok := range n.Initial {
		p := n.place(id)
		if p == nil {
			return fmt.Errorf("initial marking references unknown place %q", id)
		}
		if tok < 0 {
			return fmt.Errorf("initial marking of %s is negative", id)
		}
		if p.Capacity != nil && tok > *p.Capacity {
			return fmt.Errorf("initial marking of %s exceeds capacity", id)
		}
	}
	return nil
}

// SortedPlaceIDs returns place IDs in canonical (sorted) order.
func (n *Net) SortedPlaceIDs() []string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}
