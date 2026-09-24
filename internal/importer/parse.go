// Package importer parses versioned source documents.
//
// Parsing keeps the imported representation intact: raw token/capacity/weight
// strings, units and coordinate bases are copied verbatim onto the normalized
// model. Values that cannot be converted stay in place with a diagnostic and
// block exploration; they are never dropped for the sake of drawing.
package importer

import (
	"encoding/json"
	"fmt"

	"petriws/internal/domain"
)

// ModelDoc declares places and transitions.
type ModelDoc struct {
	ModelID string `json:"model_id"`
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Places  []struct {
		ID    string `json:"id"`
		Label string `json:"label,omitempty"`
	} `json:"places"`
	Transitions []struct {
		ID    string `json:"id"`
		Label string `json:"label,omitempty"`
	} `json:"transitions"`
}

// IdentityDoc binds stable identities to display names and coordinates.
type IdentityDoc struct {
	Kind  string `json:"kind"`
	Nodes []struct {
		ID       string `json:"id"`
		Display  string `json:"display,omitempty"`
		CoordRaw string `json:"coord_raw,omitempty"`
		Basis    string `json:"basis,omitempty"`
	} `json:"nodes"`
}

// ArcsDoc carries weighted flow arcs and inhibitor arcs.
type ArcsDoc struct {
	Kind string `json:"kind"`
	Arcs []struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Weight string `json:"weight"`
		Unit   string `json:"unit,omitempty"`
		Kind   string `json:"kind,omitempty"`
	} `json:"arcs"`
}

// MarkingDoc carries an initial or target marking.
type MarkingDoc struct {
	Kind   string `json:"kind"`
	Role   string `json:"role,omitempty"`
	Tokens []struct {
		Place string `json:"place"`
		Raw   string `json:"tokens"`
		Unit  string `json:"unit,omitempty"`
	} `json:"tokens"`
}

// CapacityDoc carries per-place capacity constraints.
type CapacityDoc struct {
	Kind      string `json:"kind"`
	Capacitys []struct {
		Place string `json:"place"`
		Cap   string `json:"cap"`
		Unit  string `json:"unit,omitempty"`
	} `json:"capacities"`
}

// StrategyDoc carries the exploration strategy and an optional target marking.
type StrategyDoc struct {
	Kind     string `json:"kind"`
	Strategy string `json:"strategy"`
	Target   *struct {
		Tokens []struct {
			Place  string `json:"place"`
			Tokens string `json:"tokens"`
			Unit   string `json:"unit,omitempty"`
		} `json:"tokens"`
	} `json:"target,omitempty"`
}

// BudgetDoc carries the truncation budget.
type BudgetDoc struct {
	Kind      string `json:"kind"`
	MaxStates string `json:"max_states"`
	MaxSteps  string `json:"max_steps"`
}

// Parsed is one normalized source document plus raw bytes.
type Parsed struct {
	Kind domain.DocKind
	Raw  string

	ModelID     string
	Nodes       []domain.Node
	Arcs        []domain.Arc
	Marking     []domain.TokenEntry
	Capacity    []domain.CapacityEntry
	Strategy    domain.StrategySpec
	Budget      domain.BudgetSpec
	Diagnostics []domain.Diagnostic
}

// Parse converts raw JSON of one document kind into its normalized shape.
func Parse(kind domain.DocKind, raw string) (*Parsed, error) {
	p := &Parsed{Kind: kind, Raw: raw}
	switch kind {
	case domain.DocModel:
		var d ModelDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("model document: %w", err)
		}
		p.ModelID = d.ModelID
		for _, pl := range d.Places {
			p.Nodes = append(p.Nodes, domain.Node{ID: pl.ID, Kind: domain.Place, Label: label(pl.ID, pl.Label)})
		}
		for _, tr := range d.Transitions {
			p.Nodes = append(p.Nodes, domain.Node{ID: tr.ID, Kind: domain.Transition, Label: label(tr.ID, tr.Label)})
		}
	case domain.DocIdentities:
		var d IdentityDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("identities document: %w", err)
		}
		for _, n := range d.Nodes {
			node := domain.Node{ID: n.ID, Label: label(n.ID, n.Display)}
			if n.CoordRaw != "" {
				x, y, ok := splitCoord(n.CoordRaw)
				if !ok {
					node.Coord = &domain.Coord{RawX: n.CoordRaw, Basis: n.Basis, Diagnostic: "coordinate must look like \"x,y\""}
					p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityWarning, "node:" + n.ID, "unparsable coordinate kept in place"})
				} else {
					node.Coord = domain.ParseCoord(x, y, n.Basis)
					if !node.Coord.Resolved {
						p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityWarning, "node:" + n.ID, "unparsable coordinate kept in place"})
					}
				}
			}
			p.Nodes = append(p.Nodes, node)
		}
	case domain.DocArcs:
		var d ArcsDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("arcs document: %w", err)
		}
		for i, a := range d.Arcs {
			k := domain.ArcFlow
			if a.Kind == "inhibitor" {
				k = domain.ArcInhibitor
			} else if a.Kind != "" && a.Kind != "flow" {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError,
					fmt.Sprintf("arc[%d]:%s->%s", i, a.From, a.To), "unknown arc kind: " + a.Kind})
			}
			w := domain.ParseScalar(joinUnit(a.Weight, a.Unit), "arc")
			if !w.Resolved {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError,
					fmt.Sprintf("arc[%d]:%s->%s", i, a.From, a.To), w.Diagnostic})
			}
			p.Arcs = append(p.Arcs, domain.Arc{From: a.From, To: a.To, Weight: w, Kind: k})
		}
	case domain.DocMarking:
		var d MarkingDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("marking document: %w", err)
		}
		for _, t := range d.Tokens {
			s := domain.ParseScalar(joinUnit(t.Raw, t.Unit), "token")
			if !s.Resolved {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "marking:" + t.Place, s.Diagnostic})
			}
			p.Marking = append(p.Marking, domain.TokenEntry{PlaceID: t.Place, Tokens: s})
		}
	case domain.DocCapacity:
		var d CapacityDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("capacity document: %w", err)
		}
		for _, c := range d.Capacitys {
			s := domain.ParseScalar(joinUnit(c.Cap, c.Unit), "slot")
			if !s.Resolved {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "capacity:" + c.Place, s.Diagnostic})
			}
			p.Capacity = append(p.Capacity, domain.CapacityEntry{PlaceID: c.Place, Cap: s})
		}
	case domain.DocStrategy:
		var d StrategyDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("strategy document: %w", err)
		}
		switch domain.Strategy(d.Strategy) {
		case domain.BFS, domain.DFS:
		default:
			p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "strategy", "unknown strategy: " + d.Strategy})
		}
		p.Strategy = domain.StrategySpec{Strategy: domain.Strategy(d.Strategy)}
		if d.Target != nil {
			for _, t := range d.Target.Tokens {
				s := domain.ParseScalar(joinUnit(t.Tokens, t.Unit), "token")
				if !s.Resolved {
					p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "target:" + t.Place, s.Diagnostic})
				}
				p.Strategy.TargetMarking = append(p.Strategy.TargetMarking, domain.TokenEntry{PlaceID: t.Place, Tokens: s})
			}
		}
	case domain.DocBudget:
		var d BudgetDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("budget document: %w", err)
		}
		ms := domain.ParseScalar(d.MaxStates, "state")
		sp := domain.ParseScalar(d.MaxSteps, "step")
		b := domain.BudgetSpec{RawStates: d.MaxStates, RawSteps: d.MaxSteps}
		if ms.Resolved && sp.Resolved {
			mv, _ := ms.Int64()
			sv, _ := sp.Int64()
			if mv <= 0 || sv <= 0 {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "budget", "budget limits must be positive"})
			}
			b.MaxStates, b.MaxSteps, b.Resolved = mv, sv, true
		} else {
			if !ms.Resolved {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "budget:max_states", ms.Diagnostic})
			}
			if !sp.Resolved {
				p.Diagnostics = append(p.Diagnostics, domain.Diagnostic{domain.SeverityError, "budget:max_steps", sp.Diagnostic})
			}
		}
		p.Budget = b
	default:
		return nil, fmt.Errorf("unknown document kind %q", kind)
	}
	return p, nil
}

func label(id, display string) string {
	if display != "" {
		return display
	}
	return id
}

func joinUnit(raw, unit string) string {
	if unit == "" {
		return raw
	}
	return raw + " " + unit
}

func splitCoord(raw string) (string, string, bool) {
	for i := 0; i < len(raw); i++ {
		if raw[i] == ',' {
			return raw[:i], raw[i+1:], true
		}
	}
	return "", "", false
}
