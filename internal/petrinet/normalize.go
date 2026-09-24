package petrinet

import (
	"fmt"
	"strconv"
	"strings"
)

// Normalize derives the analysis-ready layer from a raw snapshot. It never
// mutates the raw input. Unconvertible quantities are left at their authored
// object with a Diagnostic and are *not* silently rewritten; such an object is
// excluded from the normalized view so analysis never runs on invented data,
// while the raw bytes stay readable in the archive.
func Normalize(raw *RawNet) (*NormalizedNet, error) {
	if raw == nil {
		return nil, fmt.Errorf("nil raw net")
	}
	n := &NormalizedNet{
		ModelID:   raw.ModelID,
		VersionID: raw.VersionID,
	}

	placeByID := map[string]struct{}{}
	for _, rp := range raw.Places {
		n.PlaceOrder = append(n.PlaceOrder, rp.ID)
		placeByID[rp.ID] = struct{}{}

		initQ := rp.Initial
		diag := &n.Diagnostics
		if !initQ.ValueOK {
			d := badQuantity(rp.ID, "initial", initQ)
			*diag = append(*diag, d)
			continue // keep raw object; do not enter a guessed place
		}
		cap := int64(-1)
		if rp.Capacity.Text != "" || rp.Capacity.ValueOK {
			if !rp.Capacity.ValueOK {
				d := badQuantity(rp.ID, "capacity", rp.Capacity)
				*diag = append(*diag, d)
				continue
			}
			cap = rp.Capacity.Value
		}
		if cap >= 0 && initQ.Value > cap {
			*diag = append(*diag, Diagnostic{
				ObjectID: rp.ID, Field: "initial", Severity: "error",
				Message: fmt.Sprintf("initial tokens %d exceed capacity %d", initQ.Value, cap),
				Raw:     initQ.Text,
			})
			continue
		}
		n.Places = append(n.Places, Place{
			ID:       rp.ID,
			Name:     rp.Name,
			Initial:  initQ.Value,
			Capacity: cap,
			Coord:    rp.Coord,
		})
	}

	transByID := map[string]struct{}{}
	for _, rt := range raw.Transitions {
		n.TransitionOrder = append(n.TransitionOrder, rt.ID)
		transByID[rt.ID] = struct{}{}
		n.Transitions = append(n.Transitions, Transition{ID: rt.ID, Name: rt.Name, Coord: rt.Coord})
	}

	normalizedPlace := map[string]bool{}
	for _, p := range n.Places {
		normalizedPlace[p.ID] = true
	}
	for _, ra := range raw.Arcs {
		kind, err := parseArcKind(ra.Kind)
		if err != nil {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{
				ObjectID: ra.ID, Field: "kind", Severity: "error",
				Message: err.Error(), Raw: ra.Kind,
			})
			continue
		}
		if !ra.Weight.ValueOK {
			n.Diagnostics = append(n.Diagnostics, badQuantity(ra.ID, "weight", ra.Weight))
			continue
		}
		if ra.Weight.Value < 0 {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{
				ObjectID: ra.ID, Field: "weight", Severity: "error",
				Message: "negative arc weight", Raw: ra.Weight.Text,
			})
			continue
		}
		// Endpoints must exist; inhibitor arcs point place->transition.
		if _, ok := placeByID[ra.From]; !ok {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{
				ObjectID: ra.ID, Field: "from", Severity: "error",
				Message: "unknown source node: " + ra.From,
			})
			continue
		}
		if _, ok := transByID[ra.To]; !ok {
			n.Diagnostics = append(n.Diagnostics, Diagnostic{
				ObjectID: ra.ID, Field: "to", Severity: "error",
				Message: "unknown target node: " + ra.To,
			})
			continue
		}
		if kind == ArcOutput {
			if _, ok := transByID[ra.From]; !ok {
				n.Diagnostics = append(n.Diagnostics, Diagnostic{
					ObjectID: ra.ID, Field: "from", Severity: "error",
					Message: "output arc must start at a transition",
				})
				continue
			}
			if _, ok := placeByID[ra.To]; !ok {
				n.Diagnostics = append(n.Diagnostics, Diagnostic{
					ObjectID: ra.ID, Field: "to", Severity: "error",
					Message: "output arc must end at a place",
				})
				continue
			}
		} else {
			// input and inhibitor: place -> transition
			if _, ok := transByID[ra.From]; ok || !normalizedPlace[ra.From] {
				if !normalizedPlace[ra.From] {
					n.Diagnostics = append(n.Diagnostics, Diagnostic{
						ObjectID: ra.ID, Field: "from", Severity: "error",
						Message: "source place failed normalization: " + ra.From,
					})
					continue
				}
			}
		}
		n.Arcs = append(n.Arcs, Arc{
			ID: ra.ID, From: ra.From, To: ra.To, Kind: kind, Weight: ra.Weight.Value,
		})
	}

	return n, nil
}

func parseArcKind(k string) (ArcKind, error) {
	switch ArcKind(strings.TrimSpace(strings.ToLower(k))) {
	case ArcInput:
		return ArcInput, nil
	case ArcOutput:
		return ArcOutput, nil
	case ArcInhibitor:
		return ArcInhibitor, nil
	default:
		return "", fmt.Errorf("unknown arc kind %q", k)
	}
}

func badQuantity(id, field string, q Quantity) Diagnostic {
	_, err := strconv.ParseInt(strings.TrimSpace(q.Text), 10, 64)
	msg := "value is not a non-negative integer token count"
	if err != nil {
		msg = fmt.Sprintf("cannot interpret %q as tokens: %v", q.Text, err)
	}
	return Diagnostic{
		ObjectID: id, Field: field, Severity: "error",
		Message: msg, Raw: q.Text,
	}
}

// ParseQuantity converts an authored token string into a Quantity, retaining
// unit/basis. Empty text means "unbounded" for capacity and is left ValueOK
// false with empty text (caller decides the semantics).
func ParseQuantity(text, unit, basis string) Quantity {
	q := Quantity{Text: text, Unit: unit, Basis: basis}
	v, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || v < 0 {
		q.Diagnostic = badQuantity("", "", q).Message
		return q
	}
	q.Value = v
	q.ValueOK = true
	return q
}

// InitialMarking returns the normalized initial marking keyed by place id.
func (n *NormalizedNet) InitialMarking() Marking {
	m := Marking{Tokens: map[string]int64{}}
	for _, p := range n.Places {
		m.Tokens[p.ID] = p.Initial
	}
	return m
}

// Place returns the normalized place by id.
func (n *NormalizedNet) Place(id string) (Place, bool) {
	for _, p := range n.Places {
		if p.ID == id {
			return p, true
		}
	}
	return Place{}, false
}
