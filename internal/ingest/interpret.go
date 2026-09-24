// Package ingest turns a versioned SourceBundle into a normalized Net without
// irreversible cleaning. Non-convertible values remain in place as diagnostics;
// a whole-import failure (malformed aspect document) is reported so the caller
// can roll back the transaction.
package ingest

import (
	"encoding/json"
	"fmt"

	"tokenflow/internal/domain"
)

type modelDoc struct {
	Places      []domain.Place      `json:"places"`
	Transitions []domain.Transition `json:"transitions"`
	Arcs        []struct {
		ID   string         `json:"id"`
		From string         `json:"from"`
		To   string         `json:"to"`
		Kind domain.ArcKind `json:"kind"`
		Raw  string         `json:"weight_raw"`
		Unit string         `json:"weight_unit"`
	} `json:"arcs"`
}

type identityDoc struct {
	Nodes []domain.IdentityEntry `json:"nodes"`
}

type tokensDoc struct {
	DefaultUnit string             `json:"unit"`
	Tokens      []domain.TokenCount `json:"tokens"`
}

type capacityDoc struct {
	DefaultUnit string             `json:"unit"`
	Capacities  []rawCapacityInput `json:"capacities"`
}

type rawCapacityInput struct {
	PlaceID string `json:"place_id"`
	Raw     string `json:"raw"`
	Unit    string `json:"unit"`
}

type weightsDoc struct {
	// Weights may be supplied independently of the model arcs. Entries here
	// override model-declared weight_raw by stable arc id.
	DefaultUnit string `json:"unit"`
	Weights     []struct {
		ArcID string `json:"arc_id"`
		Raw   string `json:"raw"`
		Unit  string `json:"unit"`
	} `json:"weights"`
}

// Interpret parses every aspect. It returns a fatal error only when an aspect
// document cannot be parsed at all (caller must roll the import back);
// semantically bad values are diagnostics on the Net, never discarded.
func Interpret(b domain.SourceBundle) (*domain.Net, error) {
	var md modelDoc
	if err := strictJSON(b.ModelRaw, &md); err != nil {
		return nil, fmt.Errorf("aspect %s: %w", domain.AspectModel, err)
	}
	var idd identityDoc
	if err := strictJSON(b.IdentityRaw, &idd); err != nil {
		return nil, fmt.Errorf("aspect %s: %w", domain.AspectIdentity, err)
	}
	var wd weightsDoc
	if len(b.WeightsRaw) > 0 {
		if err := strictJSON(b.WeightsRaw, &wd); err != nil {
			return nil, fmt.Errorf("aspect %s: %w", domain.AspectWeights, err)
		}
	}
	var td tokensDoc
	if err := strictJSON(b.TokensRaw, &td); err != nil {
		return nil, fmt.Errorf("aspect %s: %w", domain.AspectTokens, err)
	}
	var cd capacityDoc
	if err := strictJSON(b.CapacityRaw, &cd); err != nil {
		return nil, fmt.Errorf("aspect %s: %w", domain.AspectCapacity, err)
	}
	var st domain.Strategy
	if err := strictJSON(b.StrategyRaw, &st); err != nil {
		return nil, fmt.Errorf("aspect %s: %w", domain.AspectStrategy, err)
	}
	if !st.Valid() {
		return nil, fmt.Errorf("aspect %s: unknown strategy kind %q", domain.AspectStrategy, st.Kind)
	}
	var bg domain.Budget
	if err := strictJSON(b.BudgetRaw, &bg); err != nil {
		return nil, fmt.Errorf("aspect %s: %w", domain.AspectBudget, err)
	}
	if bg.MaxStates <= 0 || bg.MaxDepth <= 0 {
		return nil, fmt.Errorf("aspect %s: max_states and max_depth must be positive", domain.AspectBudget)
	}

	n := &domain.Net{
		Places:      md.Places,
		Transitions: md.Transitions,
		Initial:     map[string]int64{},
		Capacity:    map[string]domain.Capacity{},
		Strategy:    st,
		Budget:      bg,
	}

	placeSet := map[string]bool{}
	for _, p := range md.Places {
		if p.ID == "" {
			return nil, fmt.Errorf("aspect %s: place with empty id", domain.AspectModel)
		}
		if placeSet[p.ID] {
			return nil, fmt.Errorf("aspect %s: duplicate place id %q", domain.AspectModel, p.ID)
		}
		placeSet[p.ID] = true
	}
	transSet := map[string]bool{}
	for _, t := range md.Transitions {
		if t.ID == "" {
			return nil, fmt.Errorf("aspect %s: transition with empty id", domain.AspectModel)
		}
		if transSet[t.ID] {
			return nil, fmt.Errorf("aspect %s: duplicate transition id %q", domain.AspectModel, t.ID)
		}
		transSet[t.ID] = true
	}

	// Identity must mention exactly the model nodes; mismatch is fatal for this
	// import so node identity is never silently rewritten.
	identitySeen := map[string]string{}
	for _, e := range idd.Nodes {
		if e.NodeID == "" {
			return nil, fmt.Errorf("aspect %s: empty node id", domain.AspectIdentity)
		}
		if identitySeen[e.NodeID] != "" {
			return nil, fmt.Errorf("aspect %s: duplicate identity %q", domain.AspectIdentity, e.NodeID)
		}
		identitySeen[e.NodeID] = e.Kind
		var ok bool
		switch e.Kind {
		case "place":
			ok = placeSet[e.NodeID]
		case "transition":
			ok = transSet[e.NodeID]
		default:
			return nil, fmt.Errorf("aspect %s: node %q has unknown kind %q", domain.AspectIdentity, e.NodeID, e.Kind)
		}
		if !ok {
			return nil, fmt.Errorf("aspect %s: identity %q not present in model", domain.AspectIdentity, e.NodeID)
		}
	}
	for _, p := range md.Places {
		if identitySeen[p.ID] != "place" {
			return nil, fmt.Errorf("aspect %s: place %q missing identity", domain.AspectIdentity, p.ID)
		}
	}
	for _, t := range md.Transitions {
		if identitySeen[t.ID] != "transition" {
			return nil, fmt.Errorf("aspect %s: transition %q missing identity", domain.AspectIdentity, t.ID)
		}
	}

	// Arcs + weights. Weight aspect overrides model-embedded raw by arc id.
	weightOverride := map[string]struct {
		raw, unit string
	}{}
	for _, w := range wd.Weights {
		weightOverride[w.ArcID] = struct{ raw, unit string }{w.Raw, w.Unit}
	}
	arcSet := map[string]bool{}
	for _, a := range md.Arcs {
		if a.ID == "" {
			return nil, fmt.Errorf("aspect %s: arc with empty id", domain.AspectModel)
		}
		if arcSet[a.ID] {
			return nil, fmt.Errorf("aspect %s: duplicate arc id %q", domain.AspectModel, a.ID)
		}
		arcSet[a.ID] = true
		kind := a.Kind
		if kind == "" {
			kind = domain.ArcNormal
		}
		if kind != domain.ArcNormal && kind != domain.ArcInhibitor {
			return nil, fmt.Errorf("aspect %s: arc %q unknown kind %q", domain.AspectModel, a.ID, a.Kind)
		}
		if !placeSet[a.From] || !transSet[a.To] {
			if !(transSet[a.From] && placeSet[a.To]) {
				return nil, fmt.Errorf("aspect %s: arc %q must connect place↔transition", domain.AspectModel, a.ID)
			}
		}
		raw, unit := a.Raw, a.Unit
		if o, ok := weightOverride[a.ID]; ok {
			raw, unit = o.raw, o.unit
		}
		if raw == "" {
			raw = "1"
		}
		w := domain.ParseWeight(raw, unit)
		if !w.Convertible && kind == domain.ArcNormal {
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: a.ID, Aspect: domain.AspectWeights, Level: "error", Message: w.Diagnostic,
			})
		}
		n.Arcs = append(n.Arcs, domain.Arc{ID: a.ID, From: a.From, To: a.To, Kind: kind, Weight: w})
	}
	for _, w := range wd.Weights {
		if !arcSet[w.ArcID] {
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: w.ArcID, Aspect: domain.AspectWeights, Level: "warning",
				Message: fmt.Sprintf("weight references unknown arc %q; retained but not drawn", w.ArcID),
			})
		}
	}

	// Initial tokens.
	for _, t := range td.Tokens {
		tc := t
		if tc.Unit == "" {
			tc.Unit = td.DefaultUnit
		}
		if !placeSet[t.PlaceID] {
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: t.PlaceID, Aspect: domain.AspectTokens, Level: "warning",
				Message: fmt.Sprintf("initial tokens reference unknown place %q; retained at source, not normalized", t.PlaceID),
			})
			continue
		}
		parsed := domain.ParseToken(t.PlaceID, t.Raw, tc.Unit)
		if !parsed.Convertible {
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: t.PlaceID, Aspect: domain.AspectTokens, Level: "error", Message: parsed.Diagnostic,
			})
			continue
		}
		n.Initial[t.PlaceID] = parsed.Value
	}
	// Default 0 for places absent from the token aspect is intentional, but we
	// materialize them lazily via Marking lookup (0 default), preserving the
	// raw fact that the place was unmentioned.

	// Capacities.
	for _, c := range cd.Capacities {
		if !placeSet[c.PlaceID] {
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: c.PlaceID, Aspect: domain.AspectCapacity, Level: "warning",
				Message: fmt.Sprintf("capacity references unknown place %q; retained at source, not normalized", c.PlaceID),
			})
			continue
		}
		pc := domain.ParseCapacity(c.PlaceID, c.Raw, c.Unit)
		if !pc.Convertible {
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: c.PlaceID, Aspect: domain.AspectCapacity, Level: "error", Message: pc.Diagnostic,
			})
			pc.Raw = c.Raw
			n.Capacity[c.PlaceID] = pc
			continue
		}
		if !pc.Unbounded && pc.Value < 0 {
			pc.Convertible = false
			pc.Diagnostic = fmt.Sprintf("negative capacity for %s", c.PlaceID)
			n.Diagnostics = append(n.Diagnostics, domain.Diagnostic{
				ObjectID: c.PlaceID, Aspect: domain.AspectCapacity, Level: "error", Message: pc.Diagnostic,
			})
		}
		n.Capacity[c.PlaceID] = pc
	}

	return n, nil
}

// Parseable reports whether a net has fatal normalized-value diagnostics that
// disable firing logic (bad weights) — such arcs are kept but excluded from
// execution, which is itself recorded as a diagnostic.
func Parseable(n *domain.Net) bool {
	for _, d := range n.Diagnostics {
		if d.Level == "error" {
			return false
		}
	}
	return true
}

func strictJSON(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty aspect document")
	}
	dec := json.NewDecoder(newReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}
