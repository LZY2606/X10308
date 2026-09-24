// Package importx parses authored Petri net documents while preserving
// their original representation. The source layer stores raw bytes and
// per-field original text; the derived layer normalizes to integer token
// counts. Values that cannot be converted stay at their original position
// with diagnostics instead of being silently dropped.
package importx

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"petriworkspace/internal/petrinet"
	"petriworkspace/internal/reach"
)

// Severity of an import diagnostic.
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
)

// Diagnostic pinpoints an unconvertible or suspicious value. The raw
// value is retained in the payload and never deleted for drawing purposes.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Path     string   `json:"path"` // JSON-ish path, e.g. places[0].capacity
	Raw      string   `json:"raw"`
	Message  string   `json:"message"`
}

// RawPlace keeps authored fields including the original text forms.
type RawPlace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity string `json:"capacity_raw"`
	Unit     string `json:"unit"`
	X        string `json:"x_raw"`
	Y        string `json:"y_raw"`
}

// RawTransition keeps authored id, name and layout basis.
type RawTransition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    string `json:"x_raw"`
	Y    string `json:"y_raw"`
}

// RawArc keeps authored weights as text.
type RawArc struct {
	ID         string           `json:"id"`
	Place      string           `json:"place"`
	Transition string           `json:"transition"`
	Kind       petrinet.ArcKind `json:"kind"`
	Weight     string           `json:"weight_raw"`
}

// Document is the authored exchange format. Numeric domain quantities use
// string fields carrying units or coordinate basis, e.g. "3 tokens".
type Document struct {
	Schema        string            `json:"schema"`
	ModelID       string            `json:"model_id"`
	Name          string            `json:"name"`
	Places        []RawPlace        `json:"places"`
	Transitions   []RawTransition   `json:"transitions"`
	Arcs          []RawArc          `json:"arcs"`
	InitialMarking map[string]string `json:"initial_marking"`
	TargetMarking  map[string]string `json:"target_marking,omitempty"`
	Strategy      RawStrategy       `json:"strategy"`
}

// RawStrategy is the authored exploration configuration.
type RawStrategy struct {
	Order        string `json:"order"`
	StateBudget  string `json:"state_budget_raw"`
}

// ParsedModel is the result of interpreting a document: the normalized
// net and strategy plus the original document and diagnostics.
type ParsedModel struct {
	Document    *Document
	RawBytes    []byte
	Net         *petrinet.Net
	Initial     petrinet.Marking
	Target      petrinet.Marking
	Strategy    reach.Strategy
	Diagnostics []Diagnostic
	// Blocking is true when at least one error diagnostic exists; derived
	// analyses must not run and the transaction rolls back.
	Blocking bool
}

func (p *ParsedModel) add(sev Severity, path, raw, msg string) {
	p.Diagnostics = append(p.Diagnostics, Diagnostic{Severity: sev, Path: path, Raw: raw, Message: msg})
	if sev == SevError {
		p.Blocking = true
	}
}

// Parse decodes raw JSON into a ParsedModel. Import is transactional at
// the store layer: Parse itself never mutates external state.
func Parse(raw []byte) (*ParsedModel, error) {
	var doc Document
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("document is not valid schema JSON: %w", err)
	}
	p := &ParsedModel{Document: &doc, RawBytes: append([]byte(nil), raw...)}

	places := make([]petrinet.Place, 0, len(doc.Places))
	for i, rp := range doc.Places {
		pl := petrinet.Place{ID: rp.ID, Name: nameOr(rp.Name, rp.ID), Unit: normalizeUnit(rp.Unit)}
		path := fmt.Sprintf("places[%d].capacity", i)
		if strings.TrimSpace(rp.Capacity) == "" || strings.EqualFold(strings.TrimSpace(rp.Capacity), "inf") || strings.EqualFold(strings.TrimSpace(rp.Capacity), "unbounded") {
			pl.Capacity = 0
		} else if v, ok := parseCount(rp.Capacity); ok {
			pl.Capacity = v
		} else {
			p.add(SevError, path, rp.Capacity, "capacity is not a non-negative integer with a known unit; value kept in raw form")
			pl.Capacity = 0
		}
		pl.X = parseCoord(p, fmt.Sprintf("places[%d].x", i), rp.X)
		pl.Y = parseCoord(p, fmt.Sprintf("places[%d].y", i), rp.Y)
		places = append(places, pl)
	}

	trans := make([]petrinet.Transition, 0, len(doc.Transitions))
	for i, rt := range doc.Transitions {
		tr := petrinet.Transition{ID: rt.ID, Name: nameOr(rt.Name, rt.ID)}
		tr.X = parseCoord(p, fmt.Sprintf("transitions[%d].x", i), rt.X)
		tr.Y = parseCoord(p, fmt.Sprintf("transitions[%d].y", i), rt.Y)
		trans = append(trans, tr)
	}

	arcs := make([]petrinet.Arc, 0, len(doc.Arcs))
	for i, ra := range doc.Arcs {
		a := petrinet.Arc{ID: ra.ID, Place: ra.Place, Trans: ra.Transition, Kind: ra.Kind}
		path := fmt.Sprintf("arcs[%d].weight", i)
		if v, ok := parseCount(ra.Weight); ok {
			a.Weight = v
		} else {
			p.add(SevError, path, ra.Weight, "arc weight is not a positive integer; value kept in raw form")
			a.Weight = 0
		}
		arcs = append(arcs, a)
	}

	initial := petrinet.Marking{}
	for k, rawV := range doc.InitialMarking {
		path := fmt.Sprintf("initial_marking[%q]", k)
		if v, ok := parseCount(rawV); ok {
			initial[k] = v
		} else {
			p.add(SevError, path, rawV, "initial token count is not a non-negative integer; value kept in raw form")
		}
	}
	target := petrinet.Marking{}
	for k, rawV := range doc.TargetMarking {
		path := fmt.Sprintf("target_marking[%q]", k)
		if v, ok := parseCount(rawV); ok {
			target[k] = v
		} else {
			p.add(SevError, path, rawV, "target token count is not a non-negative integer; value kept in raw form")
		}
	}

	if !p.Blocking {
		net, err := petrinet.Build(doc.ModelID, doc.Name, places, trans, arcs)
		if err != nil {
			p.add(SevError, "$.net", "", err.Error())
		} else {
			p.Net = net
		}
		// Referential checks between markings and places.
		for k := range doc.InitialMarking {
			if p.Net != nil {
				if _, ok := p.Net.PlaceByID(k); !ok {
					p.add(SevError, fmt.Sprintf("initial_marking[%q]", k), k, "marking references unknown place")
				}
			}
		}
		for k := range doc.TargetMarking {
			if p.Net != nil {
				if _, ok := p.Net.PlaceByID(k); !ok {
					p.add(SevError, fmt.Sprintf("target_marking[%q]", k), k, "marking references unknown place")
				}
			}
		}
		if p.Blocking {
			p.Net = nil
		}
	}

	p.Initial = initial
	if len(target) > 0 {
		p.Target = target
	}
	p.Strategy = reach.Strategy{Order: normalizeOrder(doc.Strategy.Order), Budget: 0}
	if b := strings.TrimSpace(doc.Strategy.StateBudget); b != "" {
		if v, ok := parseCount(b); ok {
			p.Strategy.StateBudget = int(v)
		} else {
			p.add(SevWarning, "strategy.state_budget", b, "budget is not an integer; treated as unlimited")
		}
	}
	return p, nil
}

func parseCount(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	// Split off an optional unit suffix such as "3 tokens" or "2/token".
	fields := strings.Fields(s)
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func parseCoord(p *ParsedModel, path, raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.Fields(s)[0])
	if err != nil {
		p.add(SevWarning, path, raw, "coordinate is not an integer in svg user units; drawn at origin, raw kept")
		return 0
	}
	return n
}

func normalizeUnit(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return "tokens"
	}
	return u
}

func normalizeOrder(o string) string {
	switch strings.ToLower(strings.TrimSpace(o)) {
	case "dfs":
		return "dfs"
	default:
		return "bfs"
	}
}

func nameOr(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return id
}
