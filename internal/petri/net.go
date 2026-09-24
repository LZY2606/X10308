// Package petri implements the domain core: Petri net models with weighted
// arcs, inhibitor arcs, place capacities, and marking canonicalization.
//
// Rules encoded here (domain invariants):
//   - transition enablement considers inputs, inhibitor arcs and capacities
//     at the same time;
//   - state canonicalization never merges markings that differ in tokens;
//   - quantities keep their raw representation and unit/coordinate basis;
//     normalization happens only in the derived layer.
package petri

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Quantity is a domain value carrying its original representation plus its
// unit or coordinate basis. Raw is always preserved; Value/OK are the
// derived, normalized view. Unconvertible values stay in place with a
// diagnostic instead of being dropped.
type Quantity struct {
	Raw        string  `json:"raw"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Basis      string  `json:"basis,omitempty"`
	OK         bool    `json:"ok"`
	Diagnostic string  `json:"diagnostic,omitempty"`
}

// ParseQuantity builds a Quantity from a raw representation. The raw string
// is preserved verbatim; normalization failures attach a diagnostic.
func ParseQuantity(raw, unit, basis string) Quantity {
	q := Quantity{Raw: raw, Unit: unit, Basis: basis}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		q.OK = false
		q.Diagnostic = fmt.Sprintf("unconvertible value %q (unit %s): kept in place, excluded from derived views", raw, unit)
		return q
	}
	q.Value = v
	q.OK = true
	return q
}

// Int returns the derived integer value (token counts, weights, capacities).
func (q Quantity) Int() int64 { return int64(q.Value) }

type Place struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Initial  Quantity  `json:"initial"`
	Capacity *Quantity `json:"capacity,omitempty"`
	X        Quantity  `json:"x"`
	Y        Quantity  `json:"y"`
}

type Transition struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	X    Quantity `json:"x"`
	Y    Quantity `json:"y"`
}

type Arc struct {
	ID        string   `json:"id"`
	From      string   `json:"from"` // place or transition id
	To        string   `json:"to"`   // place or transition id
	Weight    Quantity `json:"weight"`
	Inhibitor bool     `json:"inhibitor,omitempty"`
}

type Model struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Places      []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []Arc        `json:"arcs"`
}

// Marking maps place id to token count. Canonicalization below never merges
// markings that differ in any token count.
type Marking map[string]int64

func (m *Model) PlaceByID(id string) *Place {
	for i := range m.Places {
		if m.Places[i].ID == id {
			return &m.Places[i]
		}
	}
	return nil
}

func (m *Model) InitialMarking() Marking {
	mk := Marking{}
	for _, p := range m.Places {
		mk[p.ID] = p.Initial.Int()
	}
	return mk
}

// CanonicalKey is the identity of a state: every place's token count, sorted
// by place id. Two markings with different tokens always produce different
// keys, so distinct markings are never merged.
func CanonicalKey(mk Marking) string {
	keys := make([]string, 0, len(mk))
	for k := range mk {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%s=%d", k, mk[k])
	}
	return b.String()
}

// DisableReason explains one concrete reason a transition is not enabled.
type DisableReason struct {
	ArcID  string `json:"arc_id"`
	Kind   string `json:"kind"` // "input" | "inhibitor" | "capacity"
	Detail string `json:"detail"`
}

// Enablement evaluates a transition against a marking, considering input
// arcs, inhibitor arcs and capacity constraints simultaneously. It returns
// whether the transition is enabled plus every reason it is not (empty when
// enabled), which the review UI surfaces as deadlock causes.
func (m *Model) Enablement(t Transition, mk Marking) (bool, []DisableReason) {
	var reasons []DisableReason
	// simulate the post-fire marking for capacity checks
	delta := map[string]int64{}
	for _, a := range m.Arcs {
		if a.From == t.ID {
			// transition -> place (output)
			if p := m.PlaceByID(a.To); p != nil {
				delta[a.To] += a.Weight.Int()
			}
			continue
		}
		if a.To != t.ID {
			continue
		}
		w := a.Weight.Int()
		have := mk[a.From]
		if a.Inhibitor {
			// inhibitor arc: enabled only while tokens < weight
			if have >= w {
				reasons = append(reasons, DisableReason{
					ArcID:  a.ID,
					Kind:   "inhibitor",
					Detail: fmt.Sprintf("inhibitor arc %s: place %s holds %d tokens >= threshold %d", a.ID, a.From, have, w),
				})
			}
			continue
		}
		if have < w {
			reasons = append(reasons, DisableReason{
				ArcID:  a.ID,
				Kind:   "input",
				Detail: fmt.Sprintf("input arc %s: place %s holds %d tokens < required %d", a.ID, a.From, have, w),
			})
		}
		delta[a.From] -= w
	}
	for pid, d := range delta {
		p := m.PlaceByID(pid)
		if p == nil || p.Capacity == nil || !p.Capacity.OK {
			continue
		}
		if after := mk[pid] + d; after > p.Capacity.Int() {
			reasons = append(reasons, DisableReason{
				ArcID:  "capacity:" + pid,
				Kind:   "capacity",
				Detail: fmt.Sprintf("capacity: place %s would hold %d tokens > capacity %d", pid, after, p.Capacity.Int()),
			})
		}
	}
	return len(reasons) == 0, reasons
}

// Fire produces the successor marking. Caller must check Enablement first.
func (m *Model) Fire(t Transition, mk Marking) Marking {
	next := Marking{}
	for k, v := range mk {
		next[k] = v
	}
	for _, a := range m.Arcs {
		if a.To == t.ID && !a.Inhibitor {
			next[a.From] -= a.Weight.Int()
		}
		if a.From == t.ID {
			next[a.To] += a.Weight.Int()
		}
	}
	return next
}
