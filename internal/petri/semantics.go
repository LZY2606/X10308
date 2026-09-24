package petri

import "sort"
import "strconv"

// Firing describes one transition enabled at a marking.
type Firing struct {
	TransitionID string
	Result       Marking
}

// Enablement explains why a transition is enabled or blocked. All three
// conditions are evaluated explicitly: input tokens, inhibitor threshold and
// post-fire capacity boundary.
type Enablement struct {
	TransitionID string   `json:"transition_id"`
	Enabled      bool     `json:"enabled"`
	InputOK      bool     `json:"input_ok"`
	InhibitorOK  bool     `json:"inhibitor_ok"`
	CapacityOK   bool     `json:"capacity_ok"`
	Reasons      []string `json:"reasons,omitempty"`
}

// Preset groups the structural relations of a transition once.
type Preset struct {
	ID         string
	Inputs     []inputArc // normal place->transition
	Inhibitors []inputArc // inhibitor place->transition
	Outputs    []outputArc
}

type inputArc struct {
	Place *Place
	Arc   *Arc
}

type outputArc struct {
	Place *Place
	Arc   *Arc
}

// Presets builds deterministic transition presets in declared order.
func (n *Net) Presets() map[string]Preset {
	placeByID := map[string]*Place{}
	for _, p := range n.Places {
		placeByID[p.ID] = p
	}
	presets := map[string]Preset{}
	for _, t := range n.Transitions {
		presets[t.ID] = Preset{ID: t.ID}
	}
	for _, a := range n.Arcs {
		switch {
		case a.IsPT():
			p := presets[a.To]
			p.Inputs = append(p.Inputs, inputArc{Place: placeByID[a.From], Arc: a})
			presets[a.To] = p
		case a.IsInhibitor():
			p := presets[a.To]
			p.Inhibitors = append(p.Inhibitors, inputArc{Place: placeByID[a.From], Arc: a})
			presets[a.To] = p
		case a.IsTP():
			p := presets[a.From]
			p.Outputs = append(p.Outputs, outputArc{Place: placeByID[a.To], Arc: a})
			presets[a.From] = p
		}
	}
	for id, p := range presets {
		sort.Slice(p.Inputs, func(i, j int) bool { return p.Inputs[i].Place.ID < p.Inputs[j].Place.ID })
		sort.Slice(p.Inhibitors, func(i, j int) bool { return p.Inhibitors[i].Place.ID < p.Inhibitors[j].Place.ID })
		sort.Slice(p.Outputs, func(i, j int) bool { return p.Outputs[i].Place.ID < p.Outputs[j].Place.ID })
		presets[id] = p
	}
	return presets
}

// Evaluate checks enablement under input + inhibitor + capacity rules and
// returns the resulting marking when enabled.
func (n *Net) Evaluate(p Preset, m Marking) (Enablement, Marking) {
	idx := n.PlaceIndex()
	e := Enablement{TransitionID: p.ID, InputOK: true, InhibitorOK: true, CapacityOK: true}

	for _, in := range p.Inputs {
		need := in.Arc.Weight.Count
		have := m[idx[in.Place.ID]]
		if have < need {
			e.InputOK = false
			e.Reasons = append(e.Reasons, "input "+in.Place.ID+" has "+strconv.Itoa(have)+" < weight "+strconv.Itoa(need))
		}
	}

	for _, inh := range p.Inhibitors {
		threshold := inh.Arc.Weight.Count
		have := m[idx[inh.Place.ID]]
		if have >= threshold {
			e.InhibitorOK = false
			e.Reasons = append(e.Reasons, "inhibitor "+inh.Place.ID+" holds "+strconv.Itoa(have)+" >= threshold "+strconv.Itoa(threshold))
		}
	}

	// Compute the post-fire marking from inputs; capacity is judged on the
	// real successor (this also covers self-loops that refill a preset place).
	successor := m.Clone()
	if e.InputOK {
		for _, in := range p.Inputs {
			successor[idx[in.Place.ID]] -= in.Arc.Weight.Count
		}
	}
	for _, out := range p.Outputs {
		successor[idx[out.Place.ID]] += out.Arc.Weight.Count
	}

	if e.InputOK {
		seen := map[string]bool{}
		check := func(place *Place) {
			key := place.ID
			if seen[key] {
				return
			}
			seen[key] = true
			if place.Unbounded || place.Capacity == nil {
				return
			}
			got := successor[idx[place.ID]]
			if got > place.Capacity.Count {
				e.CapacityOK = false
				e.Reasons = append(e.Reasons, "capacity "+place.ID+" would reach "+strconv.Itoa(got)+" > "+strconv.Itoa(place.Capacity.Count))
			}
		}
		for _, out := range p.Outputs {
			check(out.Place)
		}
		for _, in := range p.Inputs {
			check(in.Place)
		}
	}

	e.Enabled = e.InputOK && e.InhibitorOK && e.CapacityOK
	if !e.Enabled {
		return e, nil
	}
	return e, successor
}

// EnabledFirings returns every legal transition in stable transition order,
// preserving concurrent candidates rather than choosing one.
func (n *Net) EnabledFirings(m Marking) []Firing {
	out := make([]Firing, 0, len(n.Transitions))
	presets := n.Presets()
	for _, t := range n.Transitions {
		e, succ := n.Evaluate(presets[t.ID], m)
		if e.Enabled {
			out = append(out, Firing{TransitionID: t.ID, Result: succ})
		}
	}
	return out
}

// Enablements returns the full input/inhibitor/capacity verdict for every
// transition, used by deadlock explanations and the review UI.
func (n *Net) Enablements(m Marking) []Enablement {
	out := make([]Enablement, 0, len(n.Transitions))
	presets := n.Presets()
	for _, t := range n.Transitions {
		e, _ := n.Evaluate(presets[t.ID], m)
		out = append(out, e)
	}
	return out
}
