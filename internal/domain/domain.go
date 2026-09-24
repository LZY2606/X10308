// Package domain contains the versioned source model for the offline
// "token-flow net" (Petri net reachability / coverability / counterexample)
// workspace. Source data (raw representation) is kept separately from the
// normalized interpretation derived from it.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Aspect names are the seven versioned direct sources. They are part of the
// stable on-disk contract and must never be renamed silently.
const (
	AspectModel     = "model"      // places/transitions/arcs graph
	AspectIdentity  = "identity"   // node identity mapping
	AspectWeights   = "arc_weight" // arc weights (kept as their own aspect)
	AspectTokens    = "initial_tokens"
	AspectCapacity  = "capacity"
	AspectStrategy  = "exploration_strategy"
	AspectBudget    = "truncation_budget"
)

var AspectOrder = []string{
	AspectModel, AspectIdentity, AspectWeights, AspectTokens,
	AspectCapacity, AspectStrategy, AspectBudget,
}

// RuleVersion versions the enablement/canonicalization/budget judgments.
// Changing a rule must bump this; audit events always record the version used.
const RuleVersion = "enablement-1.0.0"

// Place / Transition coordinates are the coordinate datum used by SVG views.
type Place struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Units    string  `json:"units,omitempty"`
}

type Transition struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// ArcKind is "normal" (weighted token flow) or "inhibitor" (zero-test gate).
type ArcKind string

const (
	ArcNormal    ArcKind = "normal"
	ArcInhibitor ArcKind = "inhibitor"
)

type Arc struct {
	ID     string  `json:"id"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Kind   ArcKind `json:"kind"`
	Weight Weight  `json:"weight"`
}

// Weight preserves the imported representation. Raw is always the original
// token; Value is the normalized count only when Convertible is true.
type Weight struct {
	Raw         string `json:"raw"`
	Value       int64  `json:"value,omitempty"`
	Convertible bool   `json:"convertible"`
	Unit        string `json:"unit,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

func ParseWeight(raw, unit string) Weight {
	w := Weight{Raw: raw, Unit: unit}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		w.Diagnostic = fmt.Sprintf("weight %q is not a non-negative integer: %v", raw, err)
		return w
	}
	if v < 0 {
		w.Convertible = false
		w.Diagnostic = fmt.Sprintf("weight %q is negative", raw)
		return w
	}
	w.Convertible = true
	w.Value = v
	return w
}

// Capacity is per-place. Capacity < 0 with Convertible means unbounded.
// A non-convertible capacity stays in place with a diagnostic: it is never
// silently dropped to make a picture drawable.
type Capacity struct {
	PlaceID     string `json:"place_id"`
	Raw         string `json:"raw"`
	Value       int64  `json:"value,omitempty"`
	Unbounded   bool   `json:"unbounded"`
	Convertible bool   `json:"convertible"`
	Unit        string `json:"unit,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

func ParseCapacity(placeID, raw, unit string) Capacity {
	c := Capacity{PlaceID: placeID, Raw: raw, Unit: unit}
	s := strings.TrimSpace(raw)
	if s == "ω" || s == "*" || strings.EqualFold(s, "inf") || strings.EqualFold(s, "unbounded") {
		c.Convertible = true
		c.Unbounded = true
		return c
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		c.Diagnostic = fmt.Sprintf("capacity %q for %s is not an integer or ω: %v", raw, placeID, err)
		return c
	}
	c.Convertible = true
	c.Value = v
	return c
}

// TokenCount is an initial token value: raw representation is preserved.
type TokenCount struct {
	PlaceID     string `json:"place_id"`
	Raw         string `json:"raw"`
	Value       int64  `json:"value,omitempty"`
	Convertible bool   `json:"convertible"`
	Unit        string `json:"unit,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

func ParseToken(placeID, raw, unit string) TokenCount {
	t := TokenCount{PlaceID: placeID, Raw: raw, Unit: unit}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		t.Diagnostic = fmt.Sprintf("initial tokens %q for %s: %v", raw, placeID, err)
		return t
	}
	if v < 0 {
		t.Diagnostic = fmt.Sprintf("initial tokens %q for %s are negative", raw, placeID)
		return t
	}
	t.Convertible = true
	t.Value = v
	return t
}

// IdentityEntry records the stable node identity and where it came from.
type IdentityEntry struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"` // place | transition
	Origin string `json:"origin"`
}

// Strategy selects the exploration algorithm.
type Strategy struct {
	Kind        string `json:"kind"` // bfs | coverability
	TieBreak    string `json:"tie_break"` // transition_id_asc
	Description string `json:"description,omitempty"`
}

func (s Strategy) Valid() bool { return s.Kind == "bfs" || s.Kind == "coverability" }

type Budget struct {
	MaxStates  int    `json:"max_states"`
	MaxDepth   int    `json:"max_depth"`
	Unit       string `json:"unit,omitempty"` // "states"/"steps"; semantic datum
	Raw        string `json:"raw,omitempty"`  // original representation
}

// Net is a normalized interpretation assembled from the seven aspects.
type Net struct {
	Places      []Place
	Transitions []Transition
	Arcs        []Arc
	Initial     map[string]int64
	Capacity    map[string]Capacity // place -> capacity; missing => unbounded
	Strategy    Strategy
	Budget      Budget
	Diagnostics []Diagnostic
}

type Diagnostic struct {
	ObjectID string `json:"object_id"`
	Aspect   string `json:"aspect"`
	Level    string `json:"level"` // error | warning
	Message  string `json:"message"`
}

// Marking is a token assignment. Places with zero tokens are still explicit
// after canonicalization only when present in the net; absent keys mean 0, but
// the canonical key always lists every net place so different token counts can
// never be merged.
type Marking map[string]int64

// Key returns the canonical state key. Every net place appears exactly once in
// a fixed order, so M1 != M2 token-wise implies Key(M1) != Key(M2).
func (m Marking) Key(placeOrder []string) string {
	var b strings.Builder
	for i, p := range placeOrder {
		if i > 0 {
			b.WriteByte('|')
		}
		v, ok := m[p]
		if ok {
			b.WriteString(strconv.FormatInt(v, 10))
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Covers implements the Petri net covering order: a covers b iff every place
// has at least as many tokens, and a != b.
func (m Marking) Covers(o Marking, places []string) bool {
	strict := false
	for _, p := range places {
		if m[p] > o[p] {
			strict = true
		}
		if m[p] < o[p] {
			return false
		}
	}
	return strict
}

// SourceBundle is one imported versioned snapshot of the seven aspects.
// Every field is the original representation; RawBytes is the untouched input.
type SourceBundle struct {
	ModelRaw     json.RawMessage `json:"model"`
	IdentityRaw  json.RawMessage `json:"identity"`
	WeightsRaw   json.RawMessage `json:"arc_weights"`
	TokensRaw    json.RawMessage `json:"initial_tokens"`
	CapacityRaw  json.RawMessage `json:"capacity"`
	StrategyRaw  json.RawMessage `json:"exploration_strategy"`
	BudgetRaw    json.RawMessage `json:"truncation_budget"`
}

func (s SourceBundle) AspectRaw(aspect string) (json.RawMessage, bool) {
	switch aspect {
	case AspectModel:
		return s.ModelRaw, true
	case AspectIdentity:
		return s.IdentityRaw, true
	case AspectWeights:
		return s.WeightsRaw, true
	case AspectTokens:
		return s.TokensRaw, true
	case AspectCapacity:
		return s.CapacityRaw, true
	case AspectStrategy:
		return s.StrategyRaw, true
	case AspectBudget:
		return s.BudgetRaw, true
	}
	return nil, false
}

func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SortedPlaceIDs / SortedTransitionIDs give deterministic iteration order.
func SortedPlaceIDs(ps []Place) []string {
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}
