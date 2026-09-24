// Package petri implements a Petri net model, normalization from raw
// documents, and a deterministic reachability / state-space explorer.
//
// Rules enforced by the engine (and recorded as machine-readable events):
//   - transition enablement considers inputs, inhibitor arcs and capacities
//     at the same time;
//   - state canonicalization never merges markings with different token
//     counts;
//   - when the exploration budget is exhausted the result is "unknown" and
//     the remaining frontier is preserved.
package petri

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Omega marks an unbounded (ω) token count produced by coverability
// acceleration.
const Omega = -1

// Unbounded is the capacity sentinel for places without a capacity.
const Unbounded = -1

// Quantity is a domain quantity in its original representation: a numeric
// value (when directly usable), the raw text as imported, and a unit.
type Quantity struct {
	Value *float64 `json:"value,omitempty"`
	Raw   string   `json:"raw,omitempty"`
	Unit  string   `json:"unit,omitempty"`
}

// Point is a layout coordinate with its reference frame.
type Point struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Frame string  `json:"frame"`
}

// Diagnostic explains a value that could not be normalized. The original
// value stays in place in the raw source; nothing is dropped for display.
type Diagnostic struct {
	Where  string `json:"where"`
	Raw    string `json:"raw"`
	Reason string `json:"reason"`
}

// ---- Raw document (as imported; the derived layer normalizes it) ----

type PlaceDoc struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Capacity *Quantity `json:"capacity,omitempty"`
	Pos      *Point    `json:"pos,omitempty"`
}

type TransitionDoc struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pos  *Point `json:"pos,omitempty"`
}

type ArcDoc struct {
	ID        string   `json:"id"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Weight    Quantity `json:"weight"`
	Inhibitor bool     `json:"inhibitor,omitempty"`
}

type Strategy struct {
	Order    string `json:"order"`     // only "bfs" is implemented
	TieBreak string `json:"tie_break"` // "transition-id"
}

type ExplorationSpec struct {
	Strategy Strategy `json:"strategy"`
	Budget   int      `json:"budget"`
}

type TargetDoc struct {
	Name    string              `json:"name"`
	Marking map[string]Quantity `json:"marking"`
}

// Doc is the raw model document. It is stored verbatim in the source
// archive; Normalize derives the executable Model from it.
type Doc struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Places      []PlaceDoc          `json:"places"`
	Transitions []TransitionDoc     `json:"transitions"`
	Arcs        []ArcDoc            `json:"arcs"`
	Initial     map[string]Quantity `json:"initial"`
	Exploration ExplorationSpec     `json:"exploration"`
	Targets     []TargetDoc         `json:"targets,omitempty"`
}

// ---- Normalized model ----

type Place struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Capacity    int    `json:"capacity"` // -1 = unbounded
	CapacityRaw string `json:"capacity_raw,omitempty"`
	Pos         *Point `json:"pos,omitempty"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pos  *Point `json:"pos,omitempty"`
}

type Arc struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Weight    int    `json:"weight"`
	WeightOK  bool   `json:"weight_ok"`
	WeightRaw string `json:"weight_raw,omitempty"`
	Inhibitor bool   `json:"inhibitor,omitempty"`
}

type Target struct {
	Name    string         `json:"name"`
	Marking map[string]int `json:"marking"`
	Valid   bool           `json:"valid"`
}

// Model is the normalized, executable net. Invalid quantities are kept in
// place (WeightOK=false / raw fields) with diagnostics instead of being
// silently cleaned.
type Model struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Places      []Place        `json:"places"`
	Transitions []Transition   `json:"transitions"`
	Arcs        []Arc          `json:"arcs"`
	Initial     map[string]int `json:"initial"`
	Targets     []Target       `json:"targets,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
}

func tokenQuantity(q Quantity, where string, diags *[]Diagnostic) (int, bool) {
	unit := q.Unit
	if unit == "" {
		unit = "token"
	}
	if unit != "token" && unit != "tokens" {
		*diags = append(*diags, Diagnostic{Where: where, Raw: rawOf(q), Reason: "unsupported unit " + strconv.Quote(q.Unit) + " (expected token)"})
		return 0, false
	}
	var v float64
	switch {
	case q.Value != nil:
		v = *q.Value
	case q.Raw != "":
		parsed, err := strconv.ParseFloat(strings.TrimSpace(q.Raw), 64)
		if err != nil {
			*diags = append(*diags, Diagnostic{Where: where, Raw: rawOf(q), Reason: "unparseable numeric value"})
			return 0, false
		}
		v = parsed
	default:
		*diags = append(*diags, Diagnostic{Where: where, Raw: rawOf(q), Reason: "missing value"})
		return 0, false
	}
	if v != float64(int(v)) || v < 0 {
		*diags = append(*diags, Diagnostic{Where: where, Raw: rawOf(q), Reason: "token quantity must be a non-negative integer"})
		return 0, false
	}
	return int(v), true
}

func rawOf(q Quantity) string {
	if q.Raw != "" {
		return q.Raw
	}
	if q.Value != nil {
		return strconv.FormatFloat(*q.Value, 'g', -1, 64)
	}
	return ""
}

// Normalize derives the executable model from a raw document. It never
// mutates the document and never drops unconvertible values: they remain in
// the model with OK flags cleared and a diagnostic attached.
func Normalize(d *Doc) *Model {
	m := &Model{ID: d.ID, Name: d.Name, Initial: map[string]int{}}
	var diags []Diagnostic

	places := append([]PlaceDoc(nil), d.Places...)
	sort.SliceStable(places, func(i, j int) bool { return places[i].ID < places[j].ID })
	for _, p := range places {
		np := Place{ID: p.ID, Name: p.Name, Capacity: Unbounded, Pos: p.Pos}
		if p.Capacity != nil {
			c, ok := tokenQuantity(*p.Capacity, "place:"+p.ID+".capacity", &diags)
			if ok {
				np.Capacity = c
			} else {
				np.CapacityRaw = rawOf(*p.Capacity)
			}
		}
		m.Places = append(m.Places, np)
	}

	trans := append([]TransitionDoc(nil), d.Transitions...)
	sort.SliceStable(trans, func(i, j int) bool { return trans[i].ID < trans[j].ID })
	for _, t := range trans {
		m.Transitions = append(m.Transitions, Transition{ID: t.ID, Name: t.Name, Pos: t.Pos})
	}

	arcs := append([]ArcDoc(nil), d.Arcs...)
	sort.SliceStable(arcs, func(i, j int) bool { return arcs[i].ID < arcs[j].ID })
	for _, a := range arcs {
		na := Arc{ID: a.ID, From: a.From, To: a.To, Inhibitor: a.Inhibitor}
		w, ok := tokenQuantity(a.Weight, "arc:"+a.ID+".weight", &diags)
		if ok {
			if w == 0 {
				diags = append(diags, Diagnostic{Where: "arc:" + a.ID + ".weight", Raw: rawOf(a.Weight), Reason: "zero-weight arc has no effect"})
			}
			na.Weight, na.WeightOK = w, true
		} else {
			na.WeightRaw = rawOf(a.Weight)
		}
		m.Arcs = append(m.Arcs, na)
	}

	known := map[string]bool{}
	for _, p := range m.Places {
		known[p.ID] = true
	}
	initIDs := make([]string, 0, len(d.Initial))
	for id := range d.Initial {
		initIDs = append(initIDs, id)
	}
	sort.Strings(initIDs)
	for _, id := range initIDs {
		if !known[id] {
			diags = append(diags, Diagnostic{Where: "initial." + id, Raw: rawOf(d.Initial[id]), Reason: "initial marking references unknown place"})
			continue
		}
		v, ok := tokenQuantity(d.Initial[id], "initial."+id, &diags)
		if ok {
			m.Initial[id] = v
		}
	}

	for _, t := range d.Targets {
		nt := Target{Name: t.Name, Marking: map[string]int{}, Valid: true}
		ids := make([]string, 0, len(t.Marking))
		for id := range t.Marking {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if !known[id] {
				diags = append(diags, Diagnostic{Where: "target:" + t.Name + "." + id, Raw: rawOf(t.Marking[id]), Reason: "target references unknown place"})
				nt.Valid = false
				continue
			}
			v, ok := tokenQuantity(t.Marking[id], "target:"+t.Name+"."+id, &diags)
			if !ok {
				nt.Valid = false
				continue
			}
			nt.Marking[id] = v
		}
		m.Targets = append(m.Targets, nt)
	}

	m.Diagnostics = diags
	return m
}

// PlaceByID returns the normalized place, if any.
func (m *Model) PlaceByID(id string) *Place {
	for i := range m.Places {
		if m.Places[i].ID == id {
			return &m.Places[i]
		}
	}
	return nil
}

// CanonicalKey renders a marking canonically. Token-distinct markings always
// produce different keys; identical markings always produce the same key.
func (m *Model) CanonicalKey(mk map[string]int) string {
	parts := make([]string, 0, len(m.Places))
	for _, p := range m.Places {
		v := mk[p.ID]
		if v == Omega {
			parts = append(parts, p.ID+"=ω")
		} else {
			parts = append(parts, fmt.Sprintf("%s=%d", p.ID, v))
		}
	}
	return strings.Join(parts, ";")
}
