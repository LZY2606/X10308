package petri

import (
	"fmt"
	"sort"
	"strings"
)

// ArcKind distinguishes the three supported arc roles.
type ArcKind string

const (
	ArcInput     ArcKind = "input"
	ArcOutput    ArcKind = "output"
	ArcInhibitor ArcKind = "inhibitor"
)

// Arc is a weighted edge. Input/inhibitor arcs go place->transition,
// output arcs go transition->place. RawWeight preserves the original
// textual representation exactly as imported.
type Arc struct {
	ID        string  `json:"id"`
	Kind      ArcKind `json:"kind"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Weight    int     `json:"weight"`
	RawWeight string  `json:"raw_weight"`
	Unit      string  `json:"unit"`
}

// Place carries an optional capacity. Capacity is only meaningful when
// HasCapacity is true; RawCapacity keeps the imported representation.
type Place struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Capacity    int     `json:"capacity"`
	HasCapacity bool    `json:"has_capacity"`
	RawCapacity string  `json:"raw_capacity"`
	Unit        string  `json:"unit"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Basis       string  `json:"basis"`
}

type Transition struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Basis string  `json:"basis"`
}

// Marking maps place ID -> token count. Canonicalization never merges
// markings whose token vectors differ.
type Marking map[string]int

type Net struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Places      []Place           `json:"places"`
	Transitions []Transition      `json:"transitions"`
	Arcs        []Arc             `json:"arcs"`
	Initial     Marking           `json:"initial"`
	RawInitial  map[string]string `json:"raw_initial"`
}

func (n *Net) placeByID(id string) *Place {
	for i := range n.Places {
		if n.Places[i].ID == id {
			return &n.Places[i]
		}
	}
	return nil
}

// Canonical renders a marking as a stable, order-independent string.
// Two markings with different token counts always produce different
// canonical forms; no token-distinct markings are ever merged.
func (n *Net) Canonical(m Marking) string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s=%d", id, m[id]))
	}
	return strings.Join(parts, ";")
}

// Enabled reports whether transition tID may fire at marking m.
// Enablement simultaneously considers input arcs (sufficient tokens),
// inhibitor arcs (place tokens strictly below the inhibitor weight) and
// place capacities (post-fire tokens must not exceed capacity).
// When disabled, human/machine readable reasons are returned.
func (n *Net) Enabled(tID string, m Marking) (bool, []string) {
	var reasons []string
	for _, a := range n.Arcs {
		if a.To != tID {
			continue
		}
		switch a.Kind {
		case ArcInput:
			if m[a.From] < a.Weight {
				reasons = append(reasons, fmt.Sprintf("input %s requires %d %s, has %d", a.From, a.Weight, unitOr(a.Unit, "token"), m[a.From]))
			}
		case ArcInhibitor:
			if m[a.From] >= a.Weight {
				reasons = append(reasons, fmt.Sprintf("inhibitor %s blocks: %d %s >= threshold %d", a.From, m[a.From], unitOr(a.Unit, "token"), a.Weight))
			}
		}
	}
	if len(reasons) > 0 {
		return false, reasons
	}
	post := n.Fire(tID, m)
	for _, p := range n.Places {
		if p.HasCapacity && post[p.ID] > p.Capacity {
			reasons = append(reasons, fmt.Sprintf("capacity %s would hold %d %s > limit %d", p.ID, post[p.ID], unitOr(p.Unit, "token"), p.Capacity))
		}
	}
	return len(reasons) == 0, reasons
}

// Fire returns the post-fire marking. It assumes the transition is
// enabled with respect to inputs and inhibitors; capacity is checked
// separately by Enabled so callers can report precise reasons.
func (n *Net) Fire(tID string, m Marking) Marking {
	post := make(Marking, len(m))
	for k, v := range m {
		post[k] = v
	}
	for _, a := range n.Arcs {
		if a.Kind == ArcInput && a.To == tID {
			post[a.From] -= a.Weight
		}
		if a.Kind == ArcOutput && a.From == tID {
			post[a.To] += a.Weight
		}
	}
	return post
}

// Dominates reports whether a covers b: componentwise >= and strictly
// greater in at least one place.
func Dominates(a, b Marking, places []Place) bool {
	strict := false
	for _, p := range places {
		av, bv := a[p.ID], b[p.ID]
		if av < bv {
			return false
		}
		if av > bv {
			strict = true
		}
	}
	return strict
}

func unitOr(u, def string) string {
	if u == "" {
		return def
	}
	return u
}
