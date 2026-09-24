package petri

import (
	"fmt"
	"strconv"
	"strings"
)

// Raw layer types. These mirror what the process-modeling team imports; every
// quantity carries a unit or coordinate basis and the raw representation is
// preserved by the store. Only the derived Interpret call normalizes.

// RawQ is a quantity with its original textual form and unit basis.
type RawQ struct {
	Raw  string `json:"raw"`
	Unit string `json:"unit,omitempty"`
}

// RawCoord is a layout coordinate with its basis (e.g. "grid", "svg-px").
type RawCoord struct {
	Raw   string `json:"raw,omitempty"`
	Basis string `json:"basis,omitempty"`
}

// RawPlace is the source representation of a place.
type RawPlace struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Capacity RawQ     `json:"capacity"`
	X        RawCoord `json:"x"`
	Y        RawCoord `json:"y"`
}

// RawTransition is the source representation of a transition.
type RawTransition struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	X    RawCoord `json:"x"`
	Y    RawCoord `json:"y"`
}

// RawArc is the source representation of a weighted arc.
type RawArc struct {
	ID     string  `json:"id"`
	Kind   ArcKind `json:"kind"`
	Src    string  `json:"src"`
	Dst    string  `json:"dst"`
	Weight RawQ    `json:"weight"`
}

// RawMark is the source representation of a marking entry.
type RawMark struct {
	PlaceID string `json:"place_id"`
	Tokens RawQ  `json:"tokens"`
}

// RawModel is the complete source layer of one interpretation.
type RawModel struct {
	Schema      string        `json:"schema"`
	NetID       string        `json:"net_id"`
	NetName     string        `json:"net_name"`
	RulesVersion string       `json:"rules_version"`
	Places      []RawPlace    `json:"places"`
	Transitions []RawTransition `json:"transitions"`
	Arcs        []RawArc      `json:"arcs"`
	Initial     []RawMark     `json:"initial_marking"`
}

func diag(path, code, msg, raw string) Diagnostic {
	return Diagnostic{Path: path, Severity: "warning", Code: code, Message: msg, RawValue: raw}
}

// Interpret builds the derived Model without mutating the raw layer.
// Unconvertible values remain in their raw fields, emit diagnostics and are
// treated conservatively by the engine (unknown capacity => unbounded,
// unknown weight/coordinate => zero weight / no fixed coordinate).
func Interpret(raw *RawModel) *Model {
	m := &Model{ID: raw.NetID, RulesVersion: raw.RulesVersion}
	knownPlaces := map[string]bool{}
	knownTrans := map[string]bool{}
	for _, rp := range raw.Places {
		p := Place{ID: rp.ID, Name: rp.Name, CapRaw: rp.Capacity.Raw, CapUnit: rp.Capacity.Unit}
		if v, d := parseCount(rp.Capacity, fmt.Sprintf("places[%s].capacity", rp.ID), "capacity"); d == nil {
			p.Cap = &v
		} else {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
		if v, d := parseCoord(rp.X, fmt.Sprintf("places[%s].x", rp.ID)); d == nil {
			p.X = &v
		} else {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
		if v, d := parseCoord(rp.Y, fmt.Sprintf("places[%s].y", rp.ID)); d == nil {
			p.Y = &v
		} else {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
		m.Places = append(m.Places, p)
		knownPlaces[rp.ID] = true
	}
	for _, rt := range raw.Transitions {
		t := Transition{ID: rt.ID, Name: rt.Name}
		if v, d := parseCoord(rt.X, fmt.Sprintf("transitions[%s].x", rt.ID)); d == nil {
			t.X = &v
		} else {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
		if v, d := parseCoord(rt.Y, fmt.Sprintf("transitions[%s].y", rt.ID)); d == nil {
			t.Y = &v
		} else {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
		m.Transitions = append(m.Transitions, t)
		knownTrans[rt.ID] = true
	}
	for i, ra := range raw.Arcs {
		a := Arc{ID: ra.ID, Kind: ra.Kind, Src: ra.Src, Dst: ra.Dst, WeightRaw: ra.Weight.Raw}
		v, d := parseCount(ra.Weight, fmt.Sprintf("arcs[%d].weight", i), "weight")
		if d != nil {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
		if v <= 0 {
			v = 0
		}
		a.Weight = v
		path := fmt.Sprintf("arcs[%d]", i)
		switch ra.Kind {
		case ArcInput, ArcInhibitor:
			if !knownPlaces[ra.Src] || !knownTrans[ra.Dst] {
				m.Diagnostics = append(m.Diagnostics, diag(path, "dangling_arc",
					"arc endpoint does not reference a place->transition pair", ra.ID))
			}
		case ArcOutput:
			if !knownTrans[ra.Src] || !knownPlaces[ra.Dst] {
				m.Diagnostics = append(m.Diagnostics, diag(path, "dangling_arc",
					"arc endpoint does not reference a transition->place pair", ra.ID))
			}
		default:
			m.Diagnostics = append(m.Diagnostics, diag(path, "unknown_arc_kind",
				"arc kind is not input/output/inhibitor", string(ra.Kind)))
		}
		m.Arcs = append(m.Arcs, a)
	}
	m.Index()
	for _, rm := range raw.Initial {
		path := fmt.Sprintf("initial_marking[%s]", rm.PlaceID)
		if !knownPlaces[rm.PlaceID] {
			m.Diagnostics = append(m.Diagnostics, diag(path, "unknown_place",
				"initial marking references an unknown place", rm.PlaceID))
		}
		if _, d := parseCount(rm.Tokens, path, "tokens"); d != nil {
			m.Diagnostics = append(m.Diagnostics, *d)
		}
	}
	return m
}

// InitialMarking extracts and normalizes the initial marking. Entries that
// fail to parse are omitted from the derived marking but stay in the raw
// layer with a diagnostic already attached to the model.
func (m *Model) InitialMarking(raw *RawModel) Marking {
	marking := Marking{}
	for _, p := range m.placeOrder {
		marking[p] = 0
	}
	for _, rm := range raw.Initial {
		v, d := parseCount(rm.Tokens, rm.PlaceID, "tokens")
		if d != nil {
			continue
		}
		marking[rm.PlaceID] = v
	}
	return marking
}

// parseCount accepts integer counts with optional token-ish units.
func parseCount(q RawQ, path, field string) (int64, *Diagnostic) {
	s := strings.TrimSpace(q.Raw)
	if s == "" {
		return 0, &Diagnostic{Path: path, Severity: "warning", Code: "empty_" + field,
			Message: "value is empty; kept at source, derived value defaults to zero", RawValue: q.Raw}
	}
	var num string
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		num, _ = strings.TrimSpace(s[:i]), strings.TrimSpace(s[i:])
	} else {
		num = s
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, &Diagnostic{Path: path, Severity: "warning", Code: "unconvertible_" + field,
			Message: "value cannot be converted to an integer count; raw value retained in place", RawValue: q.Raw}
	}
	if v < 0 {
		return 0, &Diagnostic{Path: path, Severity: "warning", Code: "negative_" + field,
			Message: "negative counts are invalid; raw value retained in place", RawValue: q.Raw}
	}
	return v, nil
}

func parseCoord(c RawCoord, path string) (float64, *Diagnostic) {
	s := strings.TrimSpace(c.Raw)
	if s == "" {
		return 0, &Diagnostic{Path: path, Severity: "info", Code: "missing_coordinate",
			Message: "no coordinate supplied; auto layout applies", RawValue: c.Raw}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, &Diagnostic{Path: path, Severity: "warning", Code: "unconvertible_coordinate",
			Message: "coordinate basis value cannot be parsed; raw value retained in place", RawValue: c.Raw}
	}
	return v, nil
}
