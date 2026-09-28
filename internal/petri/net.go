// Package petri implements the token-flow domain kernel: Petri net structure,
// transition enablement (inputs + inhibitor arcs + capacities), firing,
// bounded state-space exploration with budgets, coverability and candidates.
package petri

import (
	"fmt"
	"sort"
	"strings"
)

// ArcKind distinguishes weighted flow arcs from inhibitor arcs.
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

type Place struct {
	ID           string
	Name         string
	X, Y         int
	Capacity     *int64 // nil means unbounded
	CapacityUnit string
}

type Transition struct {
	ID     string
	Name   string
	X, Y   int
}

type Arc struct {
	ID     string
	From   string
	To     string
	Weight int64
	Kind   ArcKind
}

type Net struct {
	Places      []*Place
	Transitions []*Transition
	Arcs        []*Arc

	place map[string]*Place
	trans map[string]*Transition
	in    map[string][]*Arc // transition -> incoming arcs
	out   map[string][]*Arc // transition -> outgoing arcs
}

// Marking is a token vector over place IDs. Canonicalization never merges
// markings that differ in token counts (see Key).
type Marking map[string]int64

// Diagnostic keeps an unconvertible value at its original location instead
// of dropping it to make the picture drawable.
type Diagnostic struct {
	Section string `json:"section"`
	Ref     string `json:"ref"`
	Raw     string `json:"raw"`
	Message string `json:"message"`
}

func Build(places []*Place, transitions []*Transition, arcs []*Arc) (*Net, error) {
	n := &Net{
		Places: places, Transitions: transitions, Arcs: arcs,
		place: map[string]*Place{}, trans: map[string]*Transition{},
		in: map[string][]*Arc{}, out: map[string][]*Arc{},
	}
	for _, p := range places {
		if p.ID == "" {
			return nil, fmt.Errorf("place with empty id")
		}
		if _, dup := n.place[p.ID]; dup {
			return nil, fmt.Errorf("duplicate place id %q", p.ID)
		}
		n.place[p.ID] = p
	}
	for _, t := range transitions {
		if t.ID == "" {
			return nil, fmt.Errorf("transition with empty id")
		}
		if _, dup := n.trans[t.ID]; dup {
			return nil, fmt.Errorf("duplicate transition id %q", t.ID)
		}
		n.trans[t.ID] = t
	}
	for _, a := range arcs {
		if a.ID == "" {
			return nil, fmt.Errorf("arc with empty id")
		}
		if a.Weight < 0 {
			return nil, fmt.Errorf("arc %q has negative weight %d", a.ID, a.Weight)
		}
		pIsPlace := n.place[a.From] != nil
		tIsTrans := n.trans[a.To] != nil
		if a.Kind == ArcNormal && pIsPlace && tIsTrans {
			n.in[a.To] = append(n.in[a.To], a)
		} else if a.Kind == ArcNormal && n.trans[a.From] != nil && n.place[a.To] != nil {
			n.out[a.From] = append(n.out[a.From], a)
		} else if a.Kind == ArcInhibitor && pIsPlace && tIsTrans {
			n.in[a.To] = append(n.in[a.To], a)
		} else {
			return nil, fmt.Errorf("arc %q (%s->%s, %s) has invalid endpoint kinds", a.ID, a.From, a.To, a.Kind)
		}
	}
	return n, nil
}

func (n *Net) Place(id string) *Place { return n.place[id] }

// BlockKind is why a transition is not enabled.
type BlockKind string

const (
	BlockMissingInput BlockKind = "missing_input"
	BlockInhibitor    BlockKind = "inhibitor"
	BlockCapacity     BlockKind = "capacity"
)

// BlockReason is an explicit witness that a transition cannot fire.
type BlockReason struct {
	Transition string    `json:"transition"`
	Kind       BlockKind `json:"kind"`
	Place      string    `json:"place"`
	Have       int64     `json:"have"`
	Need       int64     `json:"need"`
	Capacity   int64     `json:"capacity"` // -1 when unbounded / not applicable
	Message    string    `json:"message"`
}

// post computes the marking after firing; consumption first, then production,
// so self loops and read arcs behave correctly.
func (n *Net) post(m Marking, tID string) Marking {
	next := Marking{}
	for pid := range n.place {
		next[pid] = m[pid]
	}
	for _, a := range n.in[tID] {
		if a.Kind == ArcNormal {
			next[a.From] -= a.Weight
		}
	}
	for _, a := range n.out[tID] {
		next[a.To] += a.Weight
	}
	return next
}

// EnabledReasons reports enablement and, when disabled, every witness
// (missing input tokens, active inhibitor arc, violated capacity bound).
func (n *Net) EnabledReasons(m Marking, tID string) (bool, []BlockReason) {
	t := n.trans[tID]
	if t == nil {
		return false, []BlockReason{{Transition: tID, Kind: BlockMissingInput, Message: "transition does not exist"}}
	}
	var reasons []BlockReason
	for _, a := range n.in[tID] {
		switch a.Kind {
		case ArcInhibitor:
			// Inhibitor of weight w blocks while M(p) >= w.
			if m[a.From] >= a.Weight {
				reasons = append(reasons, BlockReason{
					Transition: tID, Kind: BlockInhibitor, Place: a.From,
					Have: m[a.From], Need: a.Weight, Capacity: -1,
					Message: fmt.Sprintf("inhibitor arc %s blocks: place %s holds %d >= %d", a.ID, a.From, m[a.From], a.Weight),
				})
			}
		default:
			if m[a.From] < a.Weight {
				reasons = append(reasons, BlockReason{
					Transition: tID, Kind: BlockMissingInput, Place: a.From,
					Have: m[a.From], Need: a.Weight, Capacity: -1,
					Message: fmt.Sprintf("input arc %s starved: place %s holds %d, needs %d", a.ID, a.From, m[a.From], a.Weight),
				})
			}
		}
	}
	candidate := n.post(m, tID)
	for _, a := range n.out[tID] {
		p := n.place[a.To]
		if p.Capacity != nil && candidate[a.To] > *p.Capacity {
			reasons = append(reasons, BlockReason{
				Transition: tID, Kind: BlockCapacity, Place: a.To,
				Have: candidate[a.To], Need: 0, Capacity: *p.Capacity,
				Message: fmt.Sprintf("capacity bound violated at %s: firing yields %d > cap %d", a.To, candidate[a.To], *p.Capacity),
			})
		}
	}
	return len(reasons) == 0, reasons
}

func (n *Net) Enabled(m Marking, tID string) bool {
	ok, _ := n.EnabledReasons(m, tID)
	return ok
}

// Fire fires t if enabled and returns the successor marking. Self loops are
// ordinary paired arcs, so they return to the same token vector.
func (n *Net) Fire(m Marking, tID string) (Marking, error) {
	if ok, reasons := n.EnabledReasons(m, tID); !ok {
		msgs := make([]string, len(reasons))
		for i, r := range reasons {
			msgs[i] = r.Message
		}
		return nil, fmt.Errorf("transition %s not enabled: %s", tID, strings.Join(msgs, "; "))
	}
	return n.post(m, tID), nil
}

// Key is the canonical state identity: every place (including zero-token
// places) contributes, so distinct token vectors never share a key.
func (n *Net) Key(m Marking) string {
	ids := make([]string, 0, len(n.place))
	for id := range n.place {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(id)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%d", m[id]))
	}
	return b.String()
}

// Covers reports whether a componentwise covers b (a >= b everywhere).
func Covers(a, b Marking) bool {
	for pid, v := range b {
		if a[pid] < v {
			return false
		}
	}
	return true
}

// Equal compares two token vectors over the supplied universe of places.
func Equal(a, b Marking) bool {
	seen := map[string]bool{}
	for k, v := range a {
		if b[k] != v {
			return false
		}
		seen[k] = true
	}
	for k, v := range b {
		if !seen[k] && a[k] != v {
			return false
		}
	}
	return true
}

// Clone returns an independent copy of a marking.
func Clone(m Marking) Marking {
	c := Marking{}
	for k, v := range m {
		c[k] = v
	}
	return c
}
