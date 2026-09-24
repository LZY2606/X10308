package petri

import (
	"fmt"
	"strconv"
	"strings"
)

// RawDoc is the imported representation of a model. Quantities keep their
// original textual form with units ("2 token"); coordinates keep their
// basis ("px"). Nothing is cleaned irreversibly at import: the raw bytes
// are stored as-is and only the derived layer (Normalize) interprets them.
type RawDoc struct {
	Places []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Capacity string `json:"capacity"` // e.g. "2 token", "unbounded"
		X        int    `json:"x"`        // basis: px
		Y        int    `json:"y"`        // basis: px
	} `json:"places"`
	Transitions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		X    int    `json:"x"`
		Y    int    `json:"y"`
	} `json:"transitions"`
	Arcs []struct {
		ID     string  `json:"id"`
		From   string  `json:"from"`
		To     string  `json:"to"`
		Kind   ArcKind `json:"kind"`
		Weight string  `json:"weight"` // e.g. "1 token"
	} `json:"arcs"`
	Initial []struct {
		Place  string `json:"place"`
		Tokens string `json:"tokens"` // e.g. "3 token"
	} `json:"initial"`
}

// Diagnostic records a value that could not be converted. The raw value
// stays in place in the stored document; the diagnostic only annotates.
type Diagnostic struct {
	Field    string `json:"field"`
	RawValue string `json:"raw_value"`
	Message  string `json:"message"`
}

// parseQuantity parses "N token" (or "unbounded" for capacities).
func parseQuantity(raw string) (int64, bool, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false, fmt.Errorf("empty quantity")
	}
	parts := strings.Fields(s)
	if len(parts) == 2 {
		if parts[1] != "token" {
			return 0, false, fmt.Errorf("unsupported unit %q (expected token)", parts[1])
		}
		s = parts[0]
	} else if len(parts) != 1 {
		return 0, false, fmt.Errorf("cannot parse quantity %q", raw)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("cannot parse quantity %q", raw)
	}
	return v, true, nil
}

// Normalize derives the canonical net from a raw document. Values that
// cannot be converted stay in the raw document and produce diagnostics;
// they are never silently dropped or rewritten.
func Normalize(doc *RawDoc) (*Net, []Diagnostic, error) {
	var diags []Diagnostic
	net := &Net{Initial: Marking{}}
	for _, p := range doc.Places {
		np := Place{ID: p.ID, Name: p.Name, X: p.X, Y: p.Y}
		c := strings.TrimSpace(p.Capacity)
		switch {
		case c == "" || c == "unbounded":
			np.Capacity = nil
		default:
			v, _, err := parseQuantity(c)
			if err != nil {
				diags = append(diags, Diagnostic{
					Field: "place." + p.ID + ".capacity", RawValue: p.Capacity,
					Message: err.Error() + "; treated as unbounded, raw value kept",
				})
			} else {
				np.Capacity = &v
			}
		}
		net.Places = append(net.Places, np)
	}
	for _, t := range doc.Transitions {
		net.Transitions = append(net.Transitions, Transition{ID: t.ID, Name: t.Name, X: t.X, Y: t.Y})
	}
	for _, a := range doc.Arcs {
		kind := a.Kind
		if kind == "" {
			kind = ArcNormal
		}
		w, _, err := parseQuantity(a.Weight)
		if err != nil {
			diags = append(diags, Diagnostic{
				Field: "arc." + a.ID + ".weight", RawValue: a.Weight,
				Message: err.Error() + "; arc excluded from derived net, raw value kept",
			})
			continue
		}
		net.Arcs = append(net.Arcs, Arc{ID: a.ID, From: a.From, To: a.To, Kind: kind, Weight: w})
	}
	for _, im := range doc.Initial {
		v, _, err := parseQuantity(im.Tokens)
		if err != nil {
			diags = append(diags, Diagnostic{
				Field: "initial." + im.Place, RawValue: im.Tokens,
				Message: err.Error() + "; treated as 0 token, raw value kept",
			})
			continue
		}
		net.Initial[im.Place] = v
	}
	if err := net.Validate(); err != nil {
		return nil, diags, err
	}
	return net, diags, nil
}
