package petrinet

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// This file is the import boundary. It never performs irreversible cleaning:
// values it cannot convert stay at their origin (Raw) with a diagnostic, and
// the graph/object is still imported. Callers may roll back the whole import
// as a single transaction (see store), but nothing here silently drops data.

// rawCaseFile uses the verbatim (un-normalized) scalar/point representation.
type rawCaseFile struct {
	Schema    string          `json:"schema"`
	CaseID    string          `json:"case_id"`
	Title     string          `json:"title"`
	Domain    string          `json:"domain"`
	Revision  string          `json:"revision"`
	Strategy  Strategy        `json:"strategy"`
	Budget    interface{}     `json:"budget"`
	Net       rawNet          `json:"net"`
	Initial   map[string]any  `json:"initial_marking"`
	Targets   []string        `json:"targets"`
	Scenarios []rawScenario   `json:"scenarios"`
	Narrative []NarrativeStep `json:"narrative"`
}

type rawNet struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Places      []rawPlace   `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []rawArc     `json:"arcs"`
}

type rawPlace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity any    `json:"capacity"`
	Pos      any    `json:"pos"`
}

type rawArc struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Weight any    `json:"weight"`
	Kind   string `json:"kind"`
}

type rawScenario struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Features    []string       `json:"features"`
	Scope       []string       `json:"scope"`
	Marking     map[string]any `json:"marking"`
	Targets     []string       `json:"targets"`
	Budget      any            `json:"budget"`
	Cover       bool           `json:"cover"`
	ExpectNote  string         `json:"expect_note"`
}

var numRe = regexp.MustCompile(`-?\d+`)

// ParseScalar reads numbers, {"value","unit"} objects, numeric strings with
// unit suffixes, and the "infty"/"unbounded" sentinel. On failure it keeps
// the raw text, sets ConvertOK=false and attaches a diagnostic.
func ParseScalar(in any, defaultUnit string) Scalar {
	s := Scalar{Unit: defaultUnit, ConvertOK: false}
	switch v := in.(type) {
	case nil:
		s.Raw, s.ConvertOK, s.Value, s.Diagnostic = "", true, 0, ""
		return s
	case float64:
		s.Value, s.ConvertOK, s.Raw = int64(v), true, strconv.FormatInt(int64(v), 10)
		return s
	case json.Number:
		if n, err := v.Int64(); err == nil {
			s.Value, s.ConvertOK, s.Raw = n, true, v.String()
			return s
		}
		s.Raw = v.String()
	case string:
		s.Raw = v
		t := strings.ToLower(strings.TrimSpace(v))
		if t == "infty" || t == "infinity" || t == "ω" || t == "unbounded" || t == "inf" {
			s.Value, s.ConvertOK = -1, true
			return s
		}
		m := numRe.FindString(t)
		if m != "" {
			if n, err := strconv.ParseInt(m, 10, 64); err == nil {
				s.Value, s.ConvertOK = n, true
				u := strings.TrimSpace(strings.TrimSuffix(t, m))
				u = strings.TrimSpace(strings.Trim(u, " ,"))
				if u != "" {
					s.Unit = u
				}
				return s
			}
		}
		s.Diagnostic = "cannot interpret scalar: " + t
		return s
	case map[string]any:
		raw, _ := json.Marshal(v)
		s.Raw = string(raw)
		if val, ok := v["value"]; ok {
			if f, ok := val.(float64); ok {
				s.Value = int64(f)
			} else if jn, ok := val.(json.Number); ok {
				if n, err := jn.Int64(); err == nil {
					s.Value = n
				}
			} else if st, ok := val.(string); ok {
				t := strings.ToLower(strings.TrimSpace(st))
				if t == "infty" || t == "unbounded" || t == "ω" || t == "inf" {
					s.Value = -1
				}
			}
		}
		if u, ok := v["unit"].(string); ok && u != "" {
			s.Unit = u
		}
		if _, hasVal := v["value"]; hasVal {
			s.ConvertOK = true
		} else {
			s.Diagnostic = "scalar object missing numeric value"
		}
		return s
	}
	b, _ := json.Marshal(in)
	s.Raw = string(b)
	s.Diagnostic = "unsupported scalar representation"
	return s
}

func ParsePoint(in any) Point {
	p := Point{Basis: "svg-user-units", ConvertOK: false}
	switch v := in.(type) {
	case nil:
		p.ConvertOK = true
		return p
	case map[string]any:
		raw, _ := json.Marshal(v)
		p.Raw = string(raw)
		xf, okx := numField(v, "x")
		yf, oky := numField(v, "y")
		p.X, p.Y = xf, yf
		if b, ok := v["basis"].(string); ok && b != "" {
			p.Basis = b
		}
		p.ConvertOK = okx && oky
		if !p.ConvertOK {
			p.Diagnostic = "coordinate missing numeric x/y; kept for raw inspection"
		}
		return p
	case []any:
		raw, _ := json.Marshal(v)
		p.Raw = string(raw)
		if len(v) >= 2 {
			xn, xok := v[0].(float64)
			yn, yok := v[1].(float64)
			p.X, p.Y = xn, yn
			p.ConvertOK = xok && yok
		}
		if !p.ConvertOK {
			p.Diagnostic = "coordinate pair must have two numeric components"
		}
		return p
	case string:
		p.Raw = v
		p.Diagnostic = "string coordinates not converted; kept in place"
		return p
	}
	b, _ := json.Marshal(in)
	p.Raw = string(b)
	p.Diagnostic = "unsupported coordinate representation"
	return p
}

func numField(m map[string]any, k string) (float64, bool) {
	switch t := m[k].(type) {
	case float64:
		return t, true
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// ParseCaseFile converts verbatim JSON into a normalized CaseFile while
// retaining every raw representation. Invalid values are reported and kept.
func ParseCaseFile(data []byte) (*CaseFile, []ImportDiag, error) {
	var raw rawCaseFile
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("case file is not valid JSON: %w", err)
	}
	diags := []ImportDiag{}

	cf := &CaseFile{
		Schema:    raw.Schema,
		CaseID:    raw.CaseID,
		Title:     raw.Title,
		Domain:    raw.Domain,
		Revision:  raw.Revision,
		Strategy:  raw.Strategy,
		Targets:   raw.Targets,
		Narrative: raw.Narrative,
	}
	b := ParseScalar(raw.Budget, "fires")
	if !b.ConvertOK || b.Value < 0 {
		diags = append(diags, ImportDiag{ObjectID: raw.CaseID, Field: "budget", Raw: b.Raw, Reason: "budget not a non-negative integer", Kept: true})
	}
	cf.Budget = b.Value

	cf.Net = Net{ID: raw.Net.ID, Name: raw.Net.Name}
	for _, rp := range raw.Net.Places {
		capS := ParseScalar(rp.Capacity, "tokens")
		if !capS.ConvertOK {
			diags = append(diags, ImportDiag{ObjectID: rp.ID, Field: "capacity", Raw: capS.Raw, Reason: capS.Diagnostic, Kept: true})
		}
		pt := ParsePoint(rp.Pos)
		if !pt.ConvertOK {
			diags = append(diags, ImportDiag{ObjectID: rp.ID, Field: "pos", Raw: pt.Raw, Reason: pt.Diagnostic, Kept: true})
		}
		cf.Net.Places = append(cf.Net.Places, Place{ID: rp.ID, Name: rp.Name, Capacity: capS, Pos: pt})
	}
	cf.Net.Transitions = raw.Net.Transitions
	for _, ta := range raw.Net.Transitions {
		if !ta.Pos.ConvertOK {
			pt := ParsePoint(ta.Pos.Raw)
			if !pt.ConvertOK {
				diags = append(diags, ImportDiag{ObjectID: ta.ID, Field: "pos", Raw: ta.Pos.Raw, Reason: pt.Diagnostic, Kept: true})
			}
		}
	}
	for _, ra := range raw.Net.Arcs {
		w := ParseScalar(ra.Weight, "tokens")
		if !w.ConvertOK {
			diags = append(diags, ImportDiag{ObjectID: ra.ID, Field: "weight", Raw: w.Raw, Reason: w.Diagnostic, Kept: true})
			w.ConvertOK = true
			w.Value = 1 // derived fallback; Raw remains the authoritative origin
			w.Diagnostic = "unconvertible weight retained in raw; exploration uses fallback weight 1"
		}
		kind := ra.Kind
		if kind == "" {
			kind = "normal"
		}
		cf.Net.Arcs = append(cf.Net.Arcs, Arc{ID: ra.ID, Source: ra.Source, Target: ra.Target, Weight: w, Kind: kind})
	}
	cf.Initial, diags = parseMarking(raw.Initial, "initial_marking", diags)

	for _, rs := range raw.Scenarios {
		m, d := parseMarking(rs.Marking, "scenario:"+rs.ID, diags)
		diags = d
		sb := ParseScalar(rs.Budget, "fires")
		if !sb.ConvertOK {
			diags = append(diags, ImportDiag{ObjectID: rs.ID, Field: "budget", Raw: sb.Raw, Reason: sb.Diagnostic, Kept: true})
		}
		cf.Scenarios = append(cf.Scenarios, Scenario{
			ID: rs.ID, Name: rs.Name, Description: rs.Description, Features: rs.Features,
			Scope: rs.Scope, Marking: m, Targets: rs.Targets, Budget: sb.Value, Cover: rs.Cover, ExpectNote: rs.ExpectNote,
		})
	}
	if err := cf.Net.validate(); err != nil {
		return cf, diags, err
	}
	return cf, diags, nil
}

func parseMarking(in map[string]any, owner string, diags []ImportDiag) (Marking, []ImportDiag) {
	m := Marking{}
	for pid, raw := range in {
		s := ParseScalar(raw, "tokens")
		if !s.ConvertOK {
			diags = append(diags, ImportDiag{ObjectID: owner + "/" + pid, Field: "tokens", Raw: s.Raw, Reason: s.Diagnostic, Kept: true})
			continue
		}
		m[pid] = s.Value
	}
	return m, diags
}

func (n Net) validate() error {
	pids := map[string]bool{}
	for _, p := range n.Places {
		if p.ID == "" {
			return fmt.Errorf("place without stable id")
		}
		if pids[p.ID] {
			return fmt.Errorf("duplicate place id %q", p.ID)
		}
		pids[p.ID] = true
	}
	tids := map[string]bool{}
	for _, t := range n.Transitions {
		if t.ID == "" {
			return fmt.Errorf("transition without stable id")
		}
		if tids[t.ID] {
			return fmt.Errorf("duplicate transition id %q", t.ID)
		}
		tids[t.ID] = true
	}
	for _, a := range n.Arcs {
		if a.ID == "" {
			return fmt.Errorf("arc without stable id")
		}
		if !pids[a.Source] || !tids[a.Target] {
			return fmt.Errorf("arc %q must run place -> transition, got %q -> %q", a.ID, a.Source, a.Target)
		}
		if a.Kind != "normal" && a.Kind != "inhibitor" {
			return fmt.Errorf("arc %q has unknown kind %q", a.ID, a.Kind)
		}
		if a.Weight.Value < 0 {
			return fmt.Errorf("arc %q has negative weight", a.ID)
		}
	}
	return nil
}

// AsInt64 converts a possibly JSON-number interface (used by revisions).
func AsInt64(in any) (int64, bool) {
	switch v := in.(type) {
	case float64:
		return int64(v), math.Trunc(v) == v
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case int64:
		return v, true
	case int:
		return int64(v), true
	}
	return 0, false
}
