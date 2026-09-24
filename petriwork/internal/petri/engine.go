package petri

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Strategy selects the exploration order of newly reached states.
type Strategy string

const (
	// StrategyBFS explores states breadth-first (shortest firing paths first).
	StrategyBFS Strategy = "bfs"
	// StrategyDFS explores states depth-first.
	StrategyDFS Strategy = "dfs"
)

// Options are the exploration parameters. All three are versioned inputs:
// strategy is the exploration strategy; Budget is the truncation budget in
// "fired-transition" units; MaxStates bounds the number of states expanded.
type Options struct {
	Strategy  Strategy `json:"strategy"`
	Budget    int64    `json:"budget"`     // fired-transition budget
	MaxStates int64    `json:"max_states"` // states-expanded cap
}

// DefaultOptions returns the documented baseline options.
func DefaultOptions() Options {
	return Options{Strategy: StrategyBFS, Budget: 200, MaxStates: 500}
}

// EnabledReason is a machine-readable witness for an enablement decision.
type EnabledReason struct {
	Transition string `json:"transition"`
	Enabled    bool   `json:"enabled"`
	// Check results for the three simultaneous enablement conditions.
	InputsSatisfied    bool     `json:"inputs_satisfied"`
	InhibitorsSatisfied bool   `json:"inhibitors_satisfied"`
	CapacitiesSatisfied bool   `json:"capacities_satisfied"`
	MissingInputs      []string `json:"missing_inputs,omitempty"`
	BlockingInhibitors []string `json:"blocking_inhibitors,omitempty"`
	OverCapacity       []string `json:"over_capacity,omitempty"`
}

// FiredTransition summarizes one enabled firing at a marking.
type FiredTransition struct {
	Trans      string  `json:"trans"`
	Result     Marking `json:"result"`
	Reason     EnabledReason `json:"enablement"`
}

// Enabled reports whether a transition is enabled at marking m.
//
// Enablement requires ALL THREE conditions simultaneously:
//  1. inputs: every normal input arc place holds at least arc weight tokens;
//  2. inhibitors: no inhibitor arc place reaches its threshold;
//  3. capacities: firing must not push any finite-capacity place above capacity.
func Enabled(n *Net, m Marking, transID string) EnabledReason {
	n.Index()
	r := EnabledReason{Transition: transID, Enabled: true}

	// Conditions 1 and 3 need the post-firing marking. Build the delta first.
	delta := map[string]int64{}
	validInputs := true
	for _, a := range n.Arc {
		if a.Trans != transID {
			continue
		}
		switch a.Kind {
		case "in":
			if m[a.Place] < a.Weight {
				validInputs = false
				r.MissingInputs = append(r.MissingInputs,
					fmt.Sprintf("place %s needs %d %s, holds %d", a.Place, a.Weight, UnitToken, m[a.Place]))
			}
			delta[a.Place] -= a.Weight
		case "out":
			delta[a.Place] += a.Weight
		}
	}
	if _, ok := n.trans[transID]; !ok {
		r.Enabled = false
		return r
	}

	// Condition 2: inhibitor arcs.
	validInhib := true
	for _, ih := range n.Inhibitors {
		if ih.Trans != transID {
			continue
		}
		if m[ih.Place] >= ih.Threshold {
			validInhib = false
			r.BlockingInhibitors = append(r.BlockingInhibitors,
				fmt.Sprintf("place %s holds %d %s >= inhibitor threshold %d", ih.Place, m[ih.Place], UnitToken, ih.Threshold))
		}
	}

	// Condition 3: capacity after firing (only meaningful when inputs hold).
	validCap := true
	if validInputs {
		places := make([]string, 0, len(delta))
		for p := range delta {
			places = append(places, p)
		}
		sort.Strings(places)
		for _, pid := range places {
			d := delta[pid]
			if d <= 0 {
				continue
			}
			p, ok := n.place[pid]
			if !ok {
				continue
			}
			if p.Capacity >= 0 && m[pid]+d > p.Capacity {
				validCap = false
				r.OverCapacity = append(r.OverCapacity,
					fmt.Sprintf("place %s would hold %d %s > capacity %d", pid, m[pid]+d, UnitToken, p.Capacity))
			}
		}
	}

	r.InputsSatisfied = validInputs
	r.InhibitorsSatisfied = validInhib
	r.CapacitiesSatisfied = validCap
	r.Enabled = validInputs && validInhib && validCap
	return r
}

// Fire fires an already-enabled transition and returns the new marking.
func Fire(n *Net, m Marking, transID string) Marking {
	out := m.Clone()
	for _, a := range n.Arc {
		if a.Trans != transID {
			continue
		}
		if a.Kind == "in" {
			out[a.Place] -= a.Weight
		} else {
			out[a.Place] += a.Weight
		}
	}
	return out
}

// Successors returns all enabled transitions at m in stable transition-id order,
// each with its resulting marking and full enablement witness. When no
// transition is enabled the returned list is empty (a deadlock candidate).
func Successors(n *Net, m Marking) []FiredTransition {
	ids := make([]string, 0, len(n.Transitions))
	for _, t := range n.Transitions {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	var out []FiredTransition
	for _, id := range ids {
		r := Enabled(n, m, id)
		if !r.Enabled {
			continue
		}
		out = append(out, FiredTransition{Trans: id, Result: Fire(n, m, id), Reason: r})
	}
	return out
}

// Canonical renders a marking as a stable string key: place ids sorted,
// counts rendered in the token unit. Distinct token counts always produce
// distinct keys; zero and absent entries are normalized identically only when
// they are the same place with the same zero count.
func Canonical(m Marking, placeIDs []string) string {
	var b strings.Builder
	for i, pid := range placeIDs {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(pid)
		b.WriteByte('=')
		b.WriteString(strconv.FormatInt(m[pid], 10))
	}
	return b.String()
}

// ParseCanonical reverses Canonical.
func ParseCanonical(key string) (Marking, error) {
	m := Marking{}
	for _, part := range strings.Split(key, "|") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return nil, fmt.Errorf("invalid canonical marking %q", key)
		}
		v, err := strconv.ParseInt(part[eq+1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid token count in %q: %w", key, err)
		}
		m[part[:eq]] = v
	}
	return m, nil
}
