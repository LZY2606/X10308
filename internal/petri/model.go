// Package petri defines the token-flow domain model: places, transitions,
// weighted arcs (normal and inhibitor), markings with units, and the
// reachability / coverability state-space engine.
//
// Every domain quantity keeps its original representation (Raw*) alongside the
// normalized value used by the engine. Values that cannot be normalized stay
// at their source position with a diagnostic; they are never dropped.
package petri

import (
	"fmt"
	"sort"
	"strings"
)

// DiagnosticSeverity is the severity of an import/normalization diagnostic.
type DiagnosticSeverity string

const (
	SeverityError   DiagnosticSeverity = "error"
	SeverityWarning DiagnosticSeverity = "warning"
)

// Diagnostic explains a value that could not be normalized. The value is kept
// on its source object; nothing is discarded to make the picture render.
type Diagnostic struct {
	ID        string             `json:"id"`
	Severity  DiagnosticSeverity `json:"severity"`
	Code      string             `json:"code"`
	ObjectRef string             `json:"objectRef"`
	Field     string             `json:"field"`
	RawValue  string             `json:"rawValue"`
	Message   string             `json:"message"`
}

// Coordinate is the coordinate basis of a node on the review canvas.
type Coordinate struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Basis  string  `json:"basis"` // e.g. "canvas-unit"
}

// Place is a Petri net place.
type Place struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Capacity     *int64    `json:"capacity"`     // normalized; nil = unbounded
	CapacityRaw  string    `json:"capacityRaw"`  // original representation
	CapacityUnit string    `json:"capacityUnit"` // e.g. "token"
	Coord        Coordinate `json:"coord"`
}

// Transition is a Petri net transition.
type Transition struct {
	ID    string     `json:"id"`
	Name string     `json:"name"`
	Coord Coordinate `json:"coord"`
}

// ArcKind distinguishes weighted flow arcs from inhibitor arcs.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

// Arc is a directed weighted arc. Excluded arcs remain in the model (and on the
// picture) with an ExcludeReason diagnostic id when their weight could not be
// normalized; they take no part in firing semantics.
type Arc struct {
	ID            string  `json:"id"`
	Source        string  `json:"source"`
	Target        string  `json:"target"`
	Kind          ArcKind `json:"kind"`
	Weight        int64   `json:"weight"`        // normalized; valid when !Excluded
	WeightRaw     string  `json:"weightRaw"`     // original representation
	WeightUnit    string  `json:"weightUnit"`    // e.g. "token/fire"
	Excluded      bool    `json:"excluded"`
	ExcludeReason string  `json:"excludeReason,omitempty"`
}

// TokenEntry is one place's initial token count, with raw rep and unit.
type TokenEntry struct {
	PlaceID string `json:"placeId"`
	Count   int64  `json:"count"`
	Raw     string `json:"raw"`
	Unit    string `json:"unit"` // "token"
}

// Net is an immutable validated Petri net plus its diagnostics.
type Net struct {
	ID           string
	PlaceOrder   []string
	Places       map[string]*Place
	Transitions  map[string]*Transition
	TransitionOrder []string
	Arcs         map[string]*Arc
	InitialRaw   []TokenEntry
	Diagnostics  []Diagnostic
}

// InitialMarking returns the normalized initial marking.
func (n *Net) InitialMarking() Marking {
	m := Marking{}
	for _, e := range n.InitialRaw {
		m[e.PlaceID] = e.Count
	}
	return m
}

// incoming/outgoing normal arcs and inhibitor arcs per transition.
type adjacency struct {
	preset  map[string]int64 // place -> summed weight
	postset map[string]int64 // place -> summed weight
	inhibit map[string]int64 // place -> threshold (strict)
}

func (n *Net) adj(tid string) adjacency {
	a := adjacency{
		preset:  map[string]int64{},
		postset: map[string]int64{},
		inhibit: map[string]int64{},
	}
	for _, arc := range n.Arcs {
		if arc.Excluded {
			continue
		}
		switch {
		case arc.Kind == ArcNormal && arc.Target == tid:
			if _, ok := n.Places[arc.Source]; !ok {
				continue
			}
			a.preset[arc.Source] += arc.Weight
		case arc.Kind == ArcNormal && arc.Source == tid:
			if _, ok := n.Places[arc.Target]; !ok {
				continue
			}
			a.postset[arc.Target] += arc.Weight
		case arc.Kind == ArcInhibitor && arc.Target == tid:
			if _, ok := n.Places[arc.Source]; !ok {
				continue
			}
			// multiple inhibitor arcs from the same place: strictest = min
			if cur, ok := a.inhibit[arc.Source]; !ok || arc.Weight < cur {
				a.inhibit[arc.Source] = arc.Weight
			}
		}
	}
	return a
}

// Marking assigns a finite token count to every place; absent = 0.
type Marking map[string]int64

// Canonical serializes a marking deterministically. Two markings with
// different token counts are never merged: the full place order participates
// in the form, so "same graph, different marking" yields distinct identities.
func (n *Net) Canonical(m Marking) string {
	var b strings.Builder
	for i, pid := range n.PlaceOrder {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%s=%d%s", pid, m[pid], tokenUnitSuffix(n))
	}
	return b.String()
}

func tokenUnitSuffix(_ *Net) string { return "token" }

// Clone returns a copy that can be mutated independently.
func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Ge reports whether m componentwise covers o (m[p] >= o[p] for every place).
func (m Marking) Ge(o Marking) bool {
	seen := map[string]bool{}
	for p, v := range o {
		if m[p] < v {
			return false
		}
		seen[p] = true
	}
	for p, v := range m {
		if !seen[p] && v < 0 {
			return false
		}
	}
	return true
}

// Equals compares finite markings over the net's place order.
func (n *Net) MarkingsEqual(a, b Marking) bool {
	for _, pid := range n.PlaceOrder {
		if a[pid] != b[pid] {
			return false
		}
	}
	return true
}

// EnableReason explains a transition's enablement decision. All three pillars
// are evaluated: input tokens, inhibitor thresholds, and place capacities.
type EnableReason struct {
	TransitionID string `json:"transitionId"`
	Enabled      bool   `json:"enabled"`
	InputOK      bool   `json:"inputOk"`
	InhibitorOK  bool   `json:"inhibitorOk"`
	CapacityOK   bool   `json:"capacityOk"`
	Blocking     []string `json:"blocking,omitempty"`
}

// BlockReason codes used in EnableReason.Blocking and deadlock explanations.
const (
	BlockInput     = "input"
	BlockInhibitor = "inhibitor"
	BlockCapacity  = "capacity"
)

// Evaluate returns the structured enablement decision at marking m.
func (n *Net) Evaluate(tid string, m Marking) EnableReason {
	a := n.adj(tid)
	r := EnableReason{TransitionID: tid}

	r.InputOK = true
	for pid, w := range a.preset {
		if m[pid] < w {
			r.InputOK = false
			r.Blocking = append(r.Blocking,
				fmt.Sprintf("%s:%s:need=%d%s,have=%d%s", BlockInput, pid, w, "token", m[pid], "token"))
		}
	}

	r.InhibitorOK = true
	for pid, threshold := range a.inhibit {
		if m[pid] >= threshold { // inhibitor arc is strict: tokens < weight
			r.InhibitorOK = false
			r.Blocking = append(r.Blocking,
				fmt.Sprintf("%s:%s:threshold<%d%s,have=%d%s", BlockInhibitor, pid, threshold, "token", m[pid], "token"))
		}
	}

	r.CapacityOK = true
	if r.InputOK && r.InhibitorOK {
		for pid, produced := range a.postset {
			consumed := a.preset[pid]
			need := m[pid] + produced - consumed
			if cap := n.Places[pid].Capacity; cap != nil && need > *cap {
				r.CapacityOK = false
				r.Blocking = append(r.Blocking,
					fmt.Sprintf("%s:%s:cap=%d%s,would=%d%s", BlockCapacity, pid, *cap, "token", need, "token"))
			}
		}
	}
	r.Enabled = r.InputOK && r.InhibitorOK && r.CapacityOK
	sort.Strings(r.Blocking)
	return r
}

// EnabledTransitions returns transition ids enabled at m, in declared order.
func (n *Net) EnabledTransitions(m Marking) []string {
	var out []string
	for _, tid := range n.TransitionOrder {
		if n.Evaluate(tid, m).Enabled {
			out = append(out, tid)
		}
	}
	return out
}

// Fire computes the successor marking. Callers must check Evaluate first.
func (n *Net) Fire(tid string, m Marking) (Marking, error) {
	r := n.Evaluate(tid, m)
	if !r.Enabled {
		return nil, fmt.Errorf("transition %s not enabled: %s", tid, strings.Join(r.Blocking, ";"))
	}
	a := n.adj(tid)
	next := m.Clone()
	for pid, w := range a.preset {
		next[pid] -= w
	}
	for pid, w := range a.postset {
		next[pid] += w
	}
	return next, nil
}
