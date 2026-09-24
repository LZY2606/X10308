package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// Interpret builds the derived normalized Net for a scenario. Raw fields on
// the scenario are untouched. Conversion problems become diagnostics and the
// offending value remains at its node (capacity parse failure is treated as
// unbounded only in the derived layer; the raw value stays).
func Interpret(s *Scenario) (*Net, error) {
	n := &Net{
		ScenarioID: s.ID,
		Places:     map[string]*Place{},
		Transitions: map[string]*Transition{},
		Inputs:     map[string][]*Arc{},
		Outputs:    map[string][]*Arc{},
		Inhibitors: map[string][]*Arc{},
		Capacity:   map[string]int{},
		Bounded:    map[string]bool{},
		Init:       Marking{},
	}
	for _, p := range s.Places {
		if p.ID == "" {
			return nil, fmt.Errorf("scenario %s: place with empty id", s.ID)
		}
		if _, dup := n.Places[p.ID]; dup {
			return nil, fmt.Errorf("scenario %s: duplicate place id %s", s.ID, p.ID)
		}
		n.Places[p.ID] = p
		if p.Coord.Unit == "" {
			n.ParseDiagnostics = append(n.ParseDiagnostics, &Diagnosis{
				Code: "missing_coordinate_unit", Field: "place.coord",
				Message: "place coordinate carries no unit; raw coordinate retained", Raw: p.ID,
			})
		}
		if p.Capacity != nil {
			c := p.Capacity
			switch {
			case c.Raw == "" || strings.EqualFold(c.Raw, "omega") || strings.EqualFold(c.Raw, "inf"):
				c.Finite, c.ConvertOK = false, true
			default:
				v, err := strconv.Atoi(strings.TrimSpace(c.Raw))
				if err != nil || v < 0 {
					c.ConvertOK = false
					c.Finite = false
					c.Diag = &Diagnosis{Code: "unconvertible_capacity", Field: "capacity",
						Message: fmt.Sprintf("capacity %q is not a non-negative integer; left at place %s", c.Raw, p.ID),
						Raw: c.Raw}
					n.ParseDiagnostics = append(n.ParseDiagnostics, c.Diag)
				} else {
					c.ConvertOK, c.Finite, c.Value = true, true, v
					n.Capacity[p.ID] = v
					n.Bounded[p.ID] = true
				}
			}
		}
	}
	for _, t := range s.Transitions {
		if t.ID == "" {
			return nil, fmt.Errorf("scenario %s: transition with empty id", s.ID)
		}
		if _, dup := n.Transitions[t.ID]; dup {
			return nil, fmt.Errorf("scenario %s: duplicate transition id %s", s.ID, t.ID)
		}
		n.Transitions[t.ID] = t
	}
	for _, a := range s.Arcs {
		if a.ID == "" {
			return nil, fmt.Errorf("scenario %s: arc with empty id", s.ID)
		}
		if a.RawWeight == "" {
			a.RawWeight = strconv.Itoa(a.Weight)
		}
		w := a.Weight
		if wr := strings.TrimSpace(a.RawWeight); wr != "" {
			v, err := strconv.Atoi(wr)
			if err != nil || v <= 0 {
				n.ParseDiagnostics = append(n.ParseDiagnostics, &Diagnosis{Code: "unconvertible_weight",
					Field: "arc.weight", Message: fmt.Sprintf("arc %s weight %q invalid; arc retained but ignored by engine", a.ID, a.RawWeight), Raw: a.RawWeight})
				continue
			} else {
				w = v
				a.Weight = v
			}
		}
		if w <= 0 {
			n.ParseDiagnostics = append(n.ParseDiagnostics, &Diagnosis{Code: "nonpositive_weight",
				Field: "arc.weight", Message: fmt.Sprintf("arc %s has non-positive weight; retained but ignored", a.ID), Raw: a.RawWeight})
			continue
		}
		_, fromP := n.Places[a.From]
		_, fromT := n.Transitions[a.From]
		_, toP := n.Places[a.To]
		_, toT := n.Transitions[a.To]
		if !(fromP && toT) && !(fromT && toP) {
			return nil, fmt.Errorf("scenario %s: arc %s must connect a place and a transition", s.ID, a.ID)
		}
		if a.Kind == ArcInhibitor {
			if !fromP || !toT {
				return nil, fmt.Errorf("scenario %s: inhibitor arc %s must point place->transition", s.ID, a.ID)
			}
			n.Inhibitors[a.To] = append(n.Inhibitors[a.To], a)
		} else {
			if fromT && toP {
				n.Outputs[a.From] = append(n.Outputs[a.From], a)
			} else {
				n.Inputs[a.To] = append(n.Inputs[a.To], a)
			}
		}
		n.Arcs = append(n.Arcs, a)
	}
	seenToken := map[string]bool{}
	for _, rt := range s.Tokens {
		if rt.Unit == "" {
			rt.Unit = UnitToken
		}
		p, ok := n.Places[rt.PlaceID]
		if !ok {
			return nil, fmt.Errorf("scenario %s: initial token references unknown place %s", s.ID, rt.PlaceID)
		}
		if seenToken[rt.PlaceID] {
			return nil, fmt.Errorf("scenario %s: repeated initial token entry for place %s", s.ID, rt.PlaceID)
		}
		seenToken[rt.PlaceID] = true
		v, err := strconv.Atoi(strings.TrimSpace(rt.Raw))
		if err != nil || v < 0 {
			rt.ConvertOK = false
			rt.Diag = &Diagnosis{Code: "unconvertible_token", Field: "initial_token",
				Message: fmt.Sprintf("initial token %q at %s is not a non-negative integer; left at original place", rt.Raw, p.ID),
				Raw: rt.Raw}
			n.ParseDiagnostics = append(n.ParseDiagnostics, rt.Diag)
			continue
		}
		rt.ConvertOK, rt.Value = true, v
		n.Init[p.ID] = v
	}
	if s.Goal != nil {
		g := Marking{}
		for pid, raw := range s.Goal {
			if _, ok := n.Places[pid]; !ok {
				return nil, fmt.Errorf("scenario %s: goal references unknown place %s", s.ID, pid)
			}
			v, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || v < 0 {
				return nil, fmt.Errorf("scenario %s: goal token %q at %s invalid", s.ID, raw, pid)
			}
			g[pid] = v
		}
		n.Goal = &g
	}
	// capacity vs initial marking is checked as a boundary diagnostic
	for pid, v := range n.Init {
		if n.Bounded[pid] && v > n.Capacity[pid] {
			n.ParseDiagnostics = append(n.ParseDiagnostics, &Diagnosis{
				Code: "initial_exceeds_capacity", Field: "initial_token",
				Message: fmt.Sprintf("place %s starts with %d tokens above capacity %d", pid, v, n.Capacity[pid]),
			})
		}
	}
	return n, nil
}
