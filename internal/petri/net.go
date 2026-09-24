// Package petri implements the Petri net domain model: places, transitions,
// weighted arcs (input / output / inhibitor), capacity constraints, and
// markings. Raw imported material is preserved verbatim; normalization into
// canonical units happens in the derived layer (see units.go).
package petri

import (
	"encoding/json"
	"fmt"
	"sort"
)

const (
	ArcInput     = "input"
	ArcOutput    = "output"
	ArcInhibitor = "inhibitor"
)

// Place is a node holding tokens. Capacity 0 means unbounded. Unit names the
// quantity unit of tokens/capacity ("token" is canonical); CoordUnit names
// the coordinate basis of X/Y ("px" is canonical, "mm" is converted).
type Place struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Capacity  int     `json:"capacity"`
	Unit      string  `json:"unit"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	CoordUnit string  `json:"coord_unit"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Arc struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"`
	Kind   string `json:"kind"` // input | output | inhibitor
}

// Marking maps place id to token count.
type Marking map[string]int

// Net is the raw model exactly as imported. No irreversible cleaning is
// applied at this layer.
type Net struct {
	Name           string       `json:"name"`
	Places         []Place      `json:"places"`
	Transitions    []Transition `json:"transitions"`
	Arcs           []Arc        `json:"arcs"`
	InitialMarking Marking      `json:"initial_marking"`
}

func ParseNet(raw []byte) (*Net, error) {
	var n Net
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("parse net: %w", err)
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return &n, nil
}

func (n *Net) Validate() error {
	if n.Name == "" {
		return fmt.Errorf("net: missing name")
	}
	seen := map[string]bool{}
	for _, p := range n.Places {
		if p.ID == "" {
			return fmt.Errorf("net %s: place with empty id", n.Name)
		}
		if seen[p.ID] {
			return fmt.Errorf("net %s: duplicate place id %q", n.Name, p.ID)
		}
		seen[p.ID] = true
		if p.Capacity < 0 {
			return fmt.Errorf("net %s: place %s negative capacity", n.Name, p.ID)
		}
	}
	tseen := map[string]bool{}
	for _, t := range n.Transitions {
		if t.ID == "" {
			return fmt.Errorf("net %s: transition with empty id", n.Name)
		}
		if tseen[t.ID] {
			return fmt.Errorf("net %s: duplicate transition id %q", n.Name, t.ID)
		}
		tseen[t.ID] = true
	}
	for _, a := range n.Arcs {
		if a.Weight <= 0 {
			return fmt.Errorf("net %s: arc %s non-positive weight %d", n.Name, a.ID, a.Weight)
		}
		switch a.Kind {
		case ArcInput, ArcInhibitor:
			if !seen[a.From] || !tseen[a.To] {
				return fmt.Errorf("net %s: arc %s (%s) must go place->transition", n.Name, a.ID, a.Kind)
			}
		case ArcOutput:
			if !tseen[a.From] || !seen[a.To] {
				return fmt.Errorf("net %s: arc %s (output) must go transition->place", n.Name, a.ID)
			}
		default:
			return fmt.Errorf("net %s: arc %s unknown kind %q", n.Name, a.ID, a.Kind)
		}
	}
	for pid, tok := range n.InitialMarking {
		if !seen[pid] {
			return fmt.Errorf("net %s: initial marking references unknown place %q", n.Name, pid)
		}
		if tok < 0 {
			return fmt.Errorf("net %s: initial marking of %s negative", n.Name, pid)
		}
	}
	return nil
}

func (n *Net) PlaceByID(id string) *Place {
	for i := range n.Places {
		if n.Places[i].ID == id {
			return &n.Places[i]
		}
	}
	return nil
}

func (n *Net) TransitionByID(id string) *Transition {
	for i := range n.Transitions {
		if n.Transitions[i].ID == id {
			return &n.Transitions[i]
		}
	}
	return nil
}

// PlaceIDs returns place ids in sorted order; this is the canonical ordering
// used by state canonicalization.
func (n *Net) PlaceIDs() []string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// Canonical renders a marking as a stable string over sorted place ids.
// Two markings with different token counts for any place always produce
// different canonical strings: canonicalization never merges markings that
// differ in tokens.
func Canonical(m Marking, placeIDs []string) string {
	var b []byte
	for i, pid := range placeIDs {
		if i > 0 {
			b = append(b, ';')
		}
		b = append(b, pid...)
		b = append(b, '=')
		b = append(b, []byte(fmt.Sprintf("%d", m[pid]))...)
	}
	return string(b)
}

// StrictlyCovers reports whether a componentwise-dominates b and differs in
// at least one place.
func StrictlyCovers(a, b Marking) bool {
	strict := false
	for pid, av := range a {
		bv := b[pid]
		if av < bv {
			return false
		}
		if av > bv {
			strict = true
		}
	}
	for pid, bv := range b {
		if _, ok := a[pid]; !ok && bv > 0 {
			return false
		}
	}
	return strict
}

// MatchesTarget reports whether m agrees with target on every place named in
// target (target may be partial).
func MatchesTarget(m, target Marking) bool {
	for pid, v := range target {
		if m[pid] != v {
			return false
		}
	}
	return true
}
