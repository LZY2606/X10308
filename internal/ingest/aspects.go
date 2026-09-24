package ingest

import (
	"encoding/json"
	"fmt"
	"strings"

	"pnwork/internal/explore"
	"pnwork/internal/pnnet"
)

// MarkingDoc is the versioned raw material for an initial / target marking.
// Every entry keeps the original text, its unit and the normalized token count
// when conversion succeeds.
type MarkingDoc struct {
	MarkingID string         `json:"marking_id"`
	Label     string         `json:"label"`
	Unit      string         `json:"unit"`
	Tokens    []MarkingToken `json:"tokens"`
}

// MarkingToken is one raw place token count.
type MarkingToken struct {
	Place string `json:"place"`
	Raw   string `json:"raw"`
	Unit  string `json:"unit"`
}

// ParsedMarking keeps the derived vector plus per-entry diagnostics. Entries
// that cannot be converted are simply absent from the vector (treated as
// unknown), but their raw text survives in Diagnostics.
type ParsedMarking struct {
	ID          string
	Label       string
	Marking     pnnet.Marking
	Diagnostics []Diagnostic
	Raw         []byte
	SHA256      string
}

// ParseMarking parses a versioned marking aspect document.
func ParseMarking(raw []byte) (*ParsedMarking, error) {
	var doc MarkingDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("fatal: marking json invalid: %w", err)
	}
	if strings.TrimSpace(doc.MarkingID) == "" {
		return nil, fmt.Errorf("fatal: marking_id is required")
	}
	pm := &ParsedMarking{
		ID: doc.MarkingID, Label: doc.Label,
		Marking: pnnet.Marking{}, Raw: raw, SHA256: Digest(raw),
	}
	seen := map[string]bool{}
	for _, t := range doc.Tokens {
		if t.Place == "" {
			return nil, fmt.Errorf("fatal: marking %s token with empty place", doc.MarkingID)
		}
		if seen[t.Place] {
			return nil, fmt.Errorf("fatal: marking %s duplicate place %s", doc.MarkingID, t.Place)
		}
		seen[t.Place] = true
		n, err := parseCount(strings.TrimSpace(t.Raw))
		if err != nil {
			pm.Diagnostics = append(pm.Diagnostics, Diagnostic{
				Object: "marking:" + doc.MarkingID, Field: "tokens",
				Text: t.Raw, Reason: err.Error() + " (place " + t.Place + ")",
			})
			continue
		}
		pm.Marking[t.Place] = n
	}
	return pm, nil
}

// StrategyDoc is the versioned exploration-strategy aspect.
type StrategyDoc struct {
	ID          string `json:"strategy_id"`
	Order       string `json:"order"` // bfs | dfs
	Description string `json:"description"`
}

// ParsedStrategy is the derived exploration strategy.
type ParsedStrategy struct {
	ID          string
	Strategy    explore.Strategy
	Diagnostics []Diagnostic
	Raw         []byte
	SHA256      string
}

// ParseStrategy parses a versioned strategy aspect document.
func ParseStrategy(raw []byte) (*ParsedStrategy, error) {
	var doc StrategyDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("fatal: strategy json invalid: %w", err)
	}
	if strings.TrimSpace(doc.ID) == "" {
		return nil, fmt.Errorf("fatal: strategy_id is required")
	}
	ps := &ParsedStrategy{ID: doc.ID, Raw: raw, SHA256: Digest(raw)}
	switch strings.ToLower(strings.TrimSpace(doc.Order)) {
	case "bfs", "":
		ps.Strategy = explore.StrategyBFS
	case "dfs":
		ps.Strategy = explore.StrategyDFS
	default:
		return nil, fmt.Errorf("fatal: strategy %s unknown order %q", doc.ID, doc.Order)
	}
	return ps, nil
}

// BudgetDoc is the versioned truncation-budget aspect, carrying its unit.
type BudgetDoc struct {
	ID          string `json:"budget_id"`
	Budget      string `json:"budget"` // raw text; "unlimited"/"∞" mean no cap
	Unit        string `json:"unit"`
	Description string `json:"description"`
}

// ParsedBudget is the derived state expansion budget.
type ParsedBudget struct {
	ID          string
	Budget      int64 // 0 == unlimited
	Diagnostics []Diagnostic
	Raw         []byte
	SHA256      string
}

// ParseBudget parses a versioned budget aspect document.
func ParseBudget(raw []byte) (*ParsedBudget, error) {
	var doc BudgetDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("fatal: budget json invalid: %w", err)
	}
	if strings.TrimSpace(doc.ID) == "" {
		return nil, fmt.Errorf("fatal: budget_id is required")
	}
	pb := &ParsedBudget{ID: doc.ID, Raw: raw, SHA256: Digest(raw)}
	txt := strings.ToLower(strings.TrimSpace(doc.Budget))
	if txt == "" || txt == "unlimited" || txt == "∞" || txt == "none" || txt == "-1" {
		return pb, nil
	}
	var n int64
	if _, err := fmt.Sscanf(txt, "%d", &n); err != nil || n < 0 {
		pb.Diagnostics = append(pb.Diagnostics, Diagnostic{
			Object: "budget:" + doc.ID, Field: "budget", Text: doc.Budget,
			Reason: "unconvertible budget, kept raw; treated as unlimited",
		})
		return pb, nil
	}
	pb.Budget = n
	return pb, nil
}
