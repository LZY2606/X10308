package petri

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Document is the raw, source-level representation of a model exactly
// as imported. All quantities stay textual so the original bytes are
// never cleaned irreversibly; normalization happens in the derived
// layer (Normalize) only.
type Document struct {
	ModelID     string            `json:"model_id"`
	Name        string            `json:"name"`
	Version     int               `json:"version"`
	Source      string            `json:"source"`
	Strategy    string            `json:"strategy"`
	Budget      string            `json:"budget"`
	Goal        map[string]string `json:"goal"`
	Places      []PlaceDoc        `json:"places"`
	Transitions []TransitionDoc   `json:"transitions"`
	Arcs        []ArcDoc          `json:"arcs"`
	Initial     map[string]string `json:"initial"`
}

type PlaceDoc struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Capacity *string  `json:"capacity"`
	Unit     string   `json:"unit"`
	X        *float64 `json:"x"`
	Y        *float64 `json:"y"`
	Basis    string   `json:"basis"`
}

type TransitionDoc struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	X     *float64 `json:"x"`
	Y     *float64 `json:"y"`
	Basis string   `json:"basis"`
}

type ArcDoc struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight string  `json:"weight"`
	Unit   string  `json:"unit"`
}

// Diagnostic is attached to a raw value that the derived layer could
// not convert. The raw value stays in place; nothing is dropped for
// the sake of rendering.
type Diagnostic struct {
	Field   string `json:"field"`
	Raw     string `json:"raw"`
	Message string `json:"message"`
}

// Normalized is the derived layer: a runnable Net plus the parsed
// exploration defaults and goal marking.
type Normalized struct {
	Net      *Net    `json:"net"`
	Strategy string  `json:"strategy"`
	Budget   int     `json:"budget"`
	Goal     Marking `json:"goal"`
}

// ParseDocument decodes raw bytes into a Document without altering any
// value. A syntax error here is a hard import failure.
func ParseDocument(raw []byte) (*Document, error) {
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse model document: %w", err)
	}
	if doc.ModelID == "" {
		return nil, fmt.Errorf("parse model document: missing model_id")
	}
	return &doc, nil
}

// Normalize derives the runnable net. Values that fail conversion are
// left in their raw position and reported as diagnostics; they are
// never silently rewritten.
func Normalize(doc *Document) (*Normalized, []Diagnostic) {
	var diags []Diagnostic
	net := &Net{
		ID:         doc.ModelID,
		Name:       doc.Name,
		Initial:    Marking{},
		RawInitial: map[string]string{},
	}
	for _, pd := range doc.Places {
		p := Place{ID: pd.ID, Name: pd.Name, Unit: unitOr(pd.Unit, "token"), Basis: pd.Basis}
		if pd.X != nil {
			p.X = *pd.X
		}
		if pd.Y != nil {
			p.Y = *pd.Y
		}
		if pd.Capacity != nil {
			p.RawCapacity = *pd.Capacity
			if v, err := strconv.Atoi(*pd.Capacity); err == nil && v >= 0 {
				p.Capacity = v
				p.HasCapacity = true
			} else {
				diags = append(diags, Diagnostic{
					Field:   "places." + pd.ID + ".capacity",
					Raw:     *pd.Capacity,
					Message: "capacity not convertible to a non-negative integer; treated as unbounded, raw value kept",
				})
			}
		}
		net.Places = append(net.Places, p)
	}
	for _, td := range doc.Transitions {
		t := Transition{ID: td.ID, Name: td.Name, Basis: td.Basis}
		if td.X != nil {
			t.X = *td.X
		}
		if td.Y != nil {
			t.Y = *td.Y
		}
		net.Transitions = append(net.Transitions, t)
	}
	for _, ad := range doc.Arcs {
		a := Arc{ID: ad.ID, Kind: ArcKind(ad.Kind), From: ad.From, To: ad.To,
			RawWeight: ad.Weight, Unit: unitOr(ad.Unit, "token")}
		if v, err := strconv.Atoi(ad.Weight); err == nil && v > 0 {
			a.Weight = v
		} else {
			a.Weight = 1
			diags = append(diags, Diagnostic{
				Field:   "arcs." + ad.ID + ".weight",
				Raw:     ad.Weight,
				Message: "arc weight not convertible to a positive integer; defaulted to 1, raw value kept",
			})
		}
		switch a.Kind {
		case ArcInput, ArcOutput, ArcInhibitor:
		default:
			diags = append(diags, Diagnostic{
				Field:   "arcs." + ad.ID + ".kind",
				Raw:     string(a.Kind),
				Message: "unknown arc kind; arc kept but ignored by enablement",
			})
		}
		net.Arcs = append(net.Arcs, a)
	}
	for pid, raw := range doc.Initial {
		net.RawInitial[pid] = raw
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			net.Initial[pid] = v
		} else {
			diags = append(diags, Diagnostic{
				Field:   "initial." + pid,
				Raw:     raw,
				Message: "initial token count not convertible; treated as 0, raw value kept",
			})
		}
	}
	norm := &Normalized{Net: net, Strategy: doc.Strategy, Budget: 0, Goal: Marking{}}
	if norm.Strategy == "" {
		norm.Strategy = "bfs"
	}
	if doc.Budget == "" {
		norm.Budget = 500
	} else if v, err := strconv.Atoi(doc.Budget); err == nil && v > 0 {
		norm.Budget = v
	} else {
		norm.Budget = 500
		diags = append(diags, Diagnostic{
			Field:   "budget",
			Raw:     doc.Budget,
			Message: "budget not convertible to a positive integer; defaulted to 500, raw value kept",
		})
	}
	for pid, raw := range doc.Goal {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			norm.Goal[pid] = v
		} else {
			diags = append(diags, Diagnostic{
				Field:   "goal." + pid,
				Raw:     raw,
				Message: "goal token count not convertible; place dropped from goal, raw value kept",
			})
		}
	}
	return norm, diags
}
