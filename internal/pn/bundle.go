package pn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Bundle is the versioned, raw representation of a case revision.
// Importing a bundle is an all-or-nothing transaction at the storage layer;
// values that cannot be normalized stay in their raw position with a
// diagnostic instead of being silently dropped.
type Bundle struct {
	Schema       string `json:"schema"`
	CaseID       string `json:"case_id"`
	CaseName     string `json:"case_name"`
	Revision     string `json:"revision"`
	ParentRev    string `json:"parent_revision,omitempty"`
	RevisionNote string `json:"revision_note,omitempty"`
	RuleVersion  string `json:"rule_version"`
	CoordBasis   string `json:"coordinate_basis"` // e.g. "canvas px, origin top-left"

	Places      []RawPlace      `json:"places"`
	Transitions []RawTransition `json:"transitions"`
	Arcs        []RawArc        `json:"arcs"`
	Initial     []RawToken      `json:"initial_marking"`
	Goal        []RawToken      `json:"goal_marking"`
	// AltMarkings supplies "same graph, different marking" narratives.
	AltMarkings []RawAltMarking `json:"alt_markings"`
	Params      RawParams       `json:"params"`
	Narrative   []RawNarrative  `json:"narrative"`
}

// RawVal preserves the source JSON representation of a domain value.
type RawVal struct {
	Raw json.RawMessage `json:"raw"`
}

// UnmarshalJSON preserves the exact source representation of any scalar.
func (v *RawVal) UnmarshalJSON(data []byte) error {
	v.Raw = append(json.RawMessage(nil), data...)
	return nil
}

func (v RawVal) String() string {
	s := strings.TrimSpace(string(v.Raw))
	if len(s) >= 2 && s[0] == '"' {
		var q string
		if err := json.Unmarshal(v.Raw, &q); err == nil {
			return q
		}
	}
	return s
}

type RawPlace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity RawVal `json:"capacity"` // null => unbounded; unparseable => diagnostic
	Unit     string `json:"unit"`
	X        RawVal `json:"x"`
	Y        RawVal `json:"y"`
}

type RawTransition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    RawVal `json:"x"`
	Y    RawVal `json:"y"`
}

type RawArc struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // input | output | inhibitor
	From   string `json:"from"`
	To     string `json:"to"`
	Weight RawVal `json:"weight"`
}

type RawToken struct {
	Place  string `json:"place"`
	Tokens RawVal `json:"tokens"`
}

type RawAltMarking struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Tokens []RawToken `json:"tokens"`
}

type RawParams struct {
	Strategy      string `json:"strategy"`
	Budget        RawVal `json:"budget"`
	MaxCandidates int    `json:"max_candidates"`
	CoverBudget   RawVal `json:"cover_budget"`
}

type RawNarrative struct {
	Stage  string `json:"stage"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Diagnostic keeps an unconvertible value at its source position.
type Diagnostic struct {
	Level   string `json:"level"` // error | warning
	Subject string `json:"subject"`
	Field   string `json:"field"`
	Raw     string `json:"raw"`
	Message string `json:"message"`
}

// Parsed is the result of raw interpretation. Diagnostics of level "error"
// describe nodes that remain in the model but are excluded from
// normalization; warnings are usable-but-noteworthy.
type Parsed struct {
	Bundle      *Bundle
	Net         *Net
	Initial     Marking
	Goal        Marking
	Alts        map[string]Marking
	Params      Config
	CoverBudget int64
	Diagnostics []Diagnostic
	// RawJSON is the exact source bytes (provenance anchor).
	RawJSON []byte
}

func parseNonNegInt(v RawVal, diag *[]Diagnostic, subject, field string) (int64, bool) {
	s := strings.TrimSpace(v.String())
	if s == "" {
		*diag = append(*diag, Diagnostic{Level: "error", Subject: subject, Field: field, Raw: s, Message: "missing numeric value; left at source position"})
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		*diag = append(*diag, Diagnostic{Level: "error", Subject: subject, Field: field, Raw: s, Message: fmt.Sprintf("cannot interpret %q as a non-negative integer; original representation retained", s)})
		return 0, false
	}
	return n, true
}

func parseCoord(v RawVal, diag *[]Diagnostic, subject, field string) (float64, bool) {
	s := strings.TrimSpace(v.String())
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*diag = append(*diag, Diagnostic{Level: "warning", Subject: subject, Field: field, Raw: s, Message: fmt.Sprintf("coordinate %q not on the numeric canvas basis; layout value retained raw", s)})
		return 0, false
	}
	return f, true
}

// ParseBundle interprets raw JSON into the normalized engine layer without
// mutating or discarding raw values.
func ParseBundle(data []byte) (*Parsed, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("bundle is not valid JSON: %w", err)
	}
	if b.CaseID == "" || b.Revision == "" {
		return nil, fmt.Errorf("bundle requires case_id and revision")
	}
	if b.Schema == "" {
		return nil, fmt.Errorf("bundle requires schema")
	}

	var diags []Diagnostic

	places := make([]Place, 0, len(b.Places))
	usablePlaces := map[string]bool{}
	for _, rp := range b.Places {
		if rp.ID == "" {
			return nil, fmt.Errorf("place without id (hard failure; whole import rolls back)")
		}
		p := Place{ID: rp.ID, Name: rp.Name, Unit: rp.Unit}
		capRawBytes := strings.TrimSpace(string(rp.Capacity.Raw))
		usable := true
		if capRawBytes == "" || capRawBytes == "null" {
			p.Capacity = nil // unbounded
		} else if n, ok := parseNonNegInt(rp.Capacity, &diags, rp.ID, "capacity"); ok {
			p.Capacity = &n
		} else {
			// Unusable node: identity is retained but arcs attached to it
			// and its capacity are excluded from the normalized engine.
			p.Capacity = nil
			usable = false
		}
		if x, ok := parseCoord(rp.X, &diags, rp.ID, "x"); ok {
			p.X = x
		}
		if y, ok := parseCoord(rp.Y, &diags, rp.ID, "y"); ok {
			p.Y = y
		}
		if p.Unit == "" {
			diags = append(diags, Diagnostic{Level: "warning", Subject: rp.ID, Field: "unit", Message: "no unit declared for token counts"})
		}
		places = append(places, p)
		if usable {
			usablePlaces[rp.ID] = true
		}
	}

	allTrans := map[string]bool{}
	type rawT struct {
		t  Transition
		id string
	}
	var rawTrans []rawT
	for _, rt := range b.Transitions {
		if rt.ID == "" {
			return nil, fmt.Errorf("transition without id (hard failure; whole import rolls back)")
		}
		t := Transition{ID: rt.ID, Name: rt.Name}
		if x, ok := parseCoord(rt.X, &diags, rt.ID, "x"); ok {
			t.X = x
		}
		if y, ok := parseCoord(rt.Y, &diags, rt.ID, "y"); ok {
			t.Y = y
		}
		rawTrans = append(rawTrans, rawT{t: t, id: rt.ID})
		allTrans[rt.ID] = true
	}

	arcs := make([]Arc, 0, len(b.Arcs))
	connectedTrans := map[string]bool{}
	for _, ra := range b.Arcs {
		if ra.ID == "" {
			return nil, fmt.Errorf("arc without id (hard failure; whole import rolls back)")
		}
		kind := ArcKind(ra.Kind)
		if kind != ArcInput && kind != ArcOutput && kind != ArcInhibitor {
			diags = append(diags, Diagnostic{Level: "error", Subject: ra.ID, Field: "kind", Raw: ra.Kind, Message: "unknown arc kind; arc kept raw, excluded from engine"})
			continue
		}
		_, fromP := usablePlaces[ra.From]
		_, fromT := allTrans[ra.From]
		toPlace, toP := usablePlaces[ra.To]
		_, toT := allTrans[ra.To]
		_ = toPlace
		if !fromP && !fromT {
			diags = append(diags, Diagnostic{Level: "error", Subject: ra.ID, Field: "from", Raw: ra.From, Message: "arc source unknown or unusable; arc excluded"})
			continue
		}
		if !toP && !toT {
			diags = append(diags, Diagnostic{Level: "error", Subject: ra.ID, Field: "to", Raw: ra.To, Message: "arc target unknown or unusable; arc excluded"})
			continue
		}
		w, ok := parseNonNegInt(ra.Weight, &diags, ra.ID, "weight")
		if !ok {
			continue
		}
		if w == 0 {
			diags = append(diags, Diagnostic{Level: "error", Subject: ra.ID, Field: "weight", Raw: "0", Message: "arc weight must be >= 1; arc excluded"})
			continue
		}
		arcs = append(arcs, Arc{ID: ra.ID, Kind: kind, From: ra.From, To: ra.To, Weight: w})
		if fromT {
			connectedTrans[ra.From] = true
		}
		if toT {
			connectedTrans[ra.To] = true
		}
	}

	trans := make([]Transition, 0, len(rawTrans))
	for _, rt := range rawTrans {
		if !connectedTrans[rt.id] {
			diags = append(diags, Diagnostic{Level: "error", Subject: rt.id, Field: "arcs",
				Message: "transition has no normalized arc (attached values unconvertible); identity kept raw, excluded from engine"})
			continue
		}
		trans = append(trans, rt.t)
	}
	net, err := Build(places, trans, arcs)
	if err != nil {
		return nil, fmt.Errorf("normalization failed (hard failure; whole import rolls back): %w", err)
	}

	parseMarking := func(rts []RawToken, label string) (Marking, map[string]bool) {
		m := Marking{}
		usable := map[string]bool{}
		for _, rt := range rts {
			n, ok := parseNonNegInt(rt.Tokens, &diags, rt.Place, label+".tokens")
			if !ok {
				continue
			}
			if _, known := net.PlaceAt(rt.Place); !known {
				diags = append(diags, Diagnostic{Level: "error", Subject: rt.Place, Field: label, Message: "marking references unknown/unusable place; entry retained raw, ignored by engine"})
				continue
			}
			m[rt.Place] = n
			usable[rt.Place] = true
		}
		return m, usable
	}
	initial, _ := parseMarking(b.Initial, "initial_marking")
	goal, _ := parseMarking(b.Goal, "goal_marking")
	alts := map[string]Marking{}
	for _, am := range b.AltMarkings {
		m, _ := parseMarking(am.Tokens, "alt_marking:"+am.ID)
		alts[am.ID] = m
	}

	cfg := Config{Strategy: StrategyBFS}
	if b.Params.Strategy != "" {
		cfg.Strategy = Strategy(b.Params.Strategy)
	}
	if n, ok := parseNonNegInt(b.Params.Budget, &diags, "params", "budget"); ok {
		cfg.Budget = n
	} else {
		cfg.Budget = 0
	}
	cfg.MaxCandidates = b.Params.MaxCandidates
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = 6
	}
	coverBudget := int64(10000)
	if len(b.Params.CoverBudget.Raw) > 0 {
		if n, ok := parseNonNegInt(b.Params.CoverBudget, &diags, "params", "cover_budget"); ok {
			coverBudget = n
		}
	}

	return &Parsed{
		Bundle: &b, Net: net, Initial: initial, Goal: goal, Alts: alts,
		Params: cfg, CoverBudget: coverBudget, Diagnostics: diags, RawJSON: data,
	}, nil
}

// ApplyDelta merges a parameter revision or a local source patch onto a
// parent bundle, producing a new full bundle (parents stay immutable).
func ApplyDelta(parent *Bundle, delta *Bundle) *Bundle {
	out := &Bundle{
		Schema:       parent.Schema,
		CaseID:       parent.CaseID,
		CaseName:     parent.CaseName,
		RuleVersion:  firstNonEmpty(delta.RuleVersion, parent.RuleVersion),
		CoordBasis:   parent.CoordBasis,
		Places:       append([]RawPlace(nil), parent.Places...),
		Transitions:  append([]RawTransition(nil), parent.Transitions...),
		Arcs:         append([]RawArc(nil), parent.Arcs...),
		Initial:      append([]RawToken(nil), parent.Initial...),
		Goal:         append([]RawToken(nil), parent.Goal...),
		AltMarkings:  append([]RawAltMarking(nil), parent.AltMarkings...),
		Params:       parent.Params,
		Narrative:    append([]RawNarrative(nil), parent.Narrative...),
		Revision:     delta.Revision,
		ParentRev:    parent.Revision,
		RevisionNote: delta.RevisionNote,
	}
	if delta.Params.Strategy != "" {
		out.Params.Strategy = delta.Params.Strategy
	}
	if len(delta.Params.Budget.Raw) > 0 {
		out.Params.Budget = delta.Params.Budget
	}
	if delta.Params.MaxCandidates > 0 {
		out.Params.MaxCandidates = delta.Params.MaxCandidates
	}
	if len(delta.Params.CoverBudget.Raw) > 0 {
		out.Params.CoverBudget = delta.Params.CoverBudget
	}
	for _, a := range delta.Arcs {
		found := false
		for i := range out.Arcs {
			if out.Arcs[i].ID == a.ID {
				out.Arcs[i] = a
				found = true
				break
			}
		}
		if !found {
			out.Arcs = append(out.Arcs, a)
		}
	}
	for _, p := range delta.Places {
		for i := range out.Places {
			if out.Places[i].ID == p.ID {
				out.Places[i] = p
				break
			}
		}
	}
	out.Narrative = append(out.Narrative, delta.Narrative...)
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
