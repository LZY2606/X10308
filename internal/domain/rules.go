package domain

import (
	"fmt"
	"math/big"
	"strings"
)

// Flow is one normalized input/output requirement of a transition.
type Flow struct {
	Place  string `json:"place"`
	Weight int64  `json:"weight"`
}

// TransitionRule is the normalized firing rule of one transition.
type TransitionRule struct {
	ID        string `json:"id"`
	Inputs    []Flow `json:"inputs"`
	Outputs   []Flow `json:"outputs"`
	Inhibitor []Flow `json:"inhibitor"`
}

// Net is the executable, normalized Petri net derived from source documents.
type Net struct {
	Places      []string         `json:"places"`
	Transitions []TransitionRule `json:"transitions"`
	capacity    map[string]int64
	nodeByID    map[string]Node
}

// Capacity returns the capacity and whether a place is bounded.
func (n *Net) Capacity(place string) (int64, bool) {
	c, ok := n.capacity[place]
	return c, ok
}

// Node looks up the source node attached to an id.
func (n *Net) Node(id string) (Node, bool) {
	node, ok := n.nodeByID[id]
	return node, ok
}

// BuildNet compiles a normalized Model into an executable Net. It returns the
// blocking diagnostics if any source value is unresolved or inconsistent.
func BuildNet(m *Model) (*Net, []Diagnostic) {
	var diags []Diagnostic
	net := &Net{capacity: map[string]int64{}, nodeByID: map[string]Node{}}
	seen := map[string]bool{}
	for _, node := range m.Nodes {
		if seen[node.ID] {
			diags = append(diags, Diagnostic{SeverityError, "node:" + node.ID, "duplicate node id"})
			continue
		}
		seen[node.ID] = true
		net.nodeByID[node.ID] = node
		if node.Kind == Place {
			net.Places = append(net.Places, node.ID)
		}
	}
	trans := map[string]*TransitionRule{}
	order := []string{}
	for _, node := range m.Nodes {
		if node.Kind == Transition {
			trans[node.ID] = &TransitionRule{ID: node.ID}
			order = append(order, node.ID)
		}
	}
	for _, arc := range m.Arcs {
		from, fromOK := net.nodeByID[arc.From]
		to, toOK := net.nodeByID[arc.To]
		if !fromOK {
			diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To, "arc source node missing"})
			continue
		}
		if !toOK {
			diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To, "arc target node missing"})
			continue
		}
		w, err := arc.Weight.Int64()
		if err != nil {
			diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To,
				"arc weight unresolved: " + err.Error()})
			continue
		}
		if w <= 0 {
			diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To, "arc weight must be positive"})
			continue
		}
		switch {
		case arc.Kind == ArcInhibitor:
			if from.Kind != Place || to.Kind != Transition {
				diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To,
					"inhibitor arc must go place -> transition"})
				continue
			}
			t := trans[arc.To]
			t.Inhibitor = append(t.Inhibitor, Flow{arc.From, w})
		case from.Kind == Place && to.Kind == Transition:
			trans[arc.To].Inputs = append(trans[arc.To].Inputs, Flow{arc.From, w})
		case from.Kind == Transition && to.Kind == Place:
			trans[arc.From].Outputs = append(trans[arc.From].Outputs, Flow{arc.To, w})
		case from.Kind == Transition && to.Kind == Transition:
			diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To,
				"transition -> transition arcs are not allowed"})
		default: // place -> place
			diags = append(diags, Diagnostic{SeverityError, "arc:" + arc.From + "->" + arc.To,
				"place -> place arcs are not allowed"})
		}
	}
	for _, id := range order {
		net.Transitions = append(net.Transitions, *trans[id])
	}
	for _, cap := range m.Capacity {
		if _, ok := net.nodeByID[cap.PlaceID]; !ok || net.nodeByID[cap.PlaceID].Kind != Place {
			diags = append(diags, Diagnostic{SeverityError, "capacity:" + cap.PlaceID, "capacity references unknown place"})
			continue
		}
		v, err := cap.Cap.Int64()
		if err != nil {
			diags = append(diags, Diagnostic{SeverityError, "capacity:" + cap.PlaceID,
				"capacity unresolved: " + err.Error()})
			continue
		}
		if v < 0 {
			diags = append(diags, Diagnostic{SeverityError, "capacity:" + cap.PlaceID, "capacity must be non-negative"})
			continue
		}
		net.capacity[cap.PlaceID] = v
	}
	return net, diags
}

// InitialMarking resolves the versioned initial marking. Every place must have
// a single resolved entry (a missing place is a blocked diagnostic rather
// than a silent zero).
func InitialMarking(n *Net, m *Model) (Marking, []Diagnostic) {
	var diags []Diagnostic
	out := Marking{}
	seen := map[string]bool{}
	for _, e := range m.Marking {
		node, ok := n.nodeByID[e.PlaceID]
		if !ok || node.Kind != Place {
			diags = append(diags, Diagnostic{SeverityError, "marking:" + e.PlaceID, "marking references unknown place"})
			continue
		}
		if seen[e.PlaceID] {
			diags = append(diags, Diagnostic{SeverityError, "marking:" + e.PlaceID, "duplicate marking entry"})
			continue
		}
		seen[e.PlaceID] = true
		v, err := e.Tokens.Int64()
		if err != nil {
			diags = append(diags, Diagnostic{SeverityError, "marking:" + e.PlaceID,
				"initial tokens unresolved: " + err.Error()})
			continue
		}
		if v < 0 {
			diags = append(diags, Diagnostic{SeverityError, "marking:" + e.PlaceID, "tokens must be non-negative"})
			continue
		}
		out[e.PlaceID] = v
	}
	for _, p := range n.Places {
		if !seen[p] {
			diags = append(diags, Diagnostic{SeverityError, "marking:" + p, "missing initial token entry"})
		}
		if c, bounded := n.capacity[p]; bounded && out[p] > c {
			diags = append(diags, Diagnostic{SeverityError, "marking:" + p,
				fmt.Sprintf("initial tokens %d exceed capacity %d", out[p], c)})
		}
	}
	return out, diags
}

// NotEnabledReason records why a transition failed the enablement check.
type NotEnabledReason struct {
	Transition string `json:"transition"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
}

// Enabled reports whether a transition is enabled under rule enablement.v1:
// inputs must hold enough tokens, inhibitor places must be below threshold,
// and the post-firing marking must respect every place capacity.
func (n *Net) Enabled(mk Marking, tID string) (bool, []NotEnabledReason) {
	var reasons []NotEnabledReason
	var tr *TransitionRule
	for i := range n.Transitions {
		if n.Transitions[i].ID == tID {
			tr = &n.Transitions[i]
			break
		}
	}
	if tr == nil {
		return false, []NotEnabledReason{{tID, "unknown", "transition not in net"}}
	}
	for _, in := range tr.Inputs {
		if mk[in.Place] < in.Weight {
			reasons = append(reasons, NotEnabledReason{tID, "input",
				fmt.Sprintf("place %s has %d tokens, needs %d", in.Place, mk[in.Place], in.Weight)})
		}
	}
	for _, inh := range tr.Inhibitor {
		if mk[inh.Place] >= inh.Weight {
			reasons = append(reasons, NotEnabledReason{tID, "inhibitor",
				fmt.Sprintf("place %s holds %d tokens >= inhibitor threshold %d", inh.Place, mk[inh.Place], inh.Weight)})
		}
	}
	if len(reasons) == 0 {
		next := mk.Clone()
		for _, in := range tr.Inputs {
			next[in.Place] -= in.Weight
		}
		for _, out := range tr.Outputs {
			next[out.Place] += out.Weight
		}
		for _, out := range tr.Outputs {
			if c, bounded := n.capacity[out.Place]; bounded && next[out.Place] > c {
				reasons = append(reasons, NotEnabledReason{tID, "capacity",
					fmt.Sprintf("firing would take place %s to %d, over capacity %d", out.Place, next[out.Place], c)})
			}
		}
	}
	return len(reasons) == 0, reasons
}

// Fire produces the successor marking; the caller must check Enabled first.
func (n *Net) Fire(mk Marking, tID string) Marking {
	for i := range n.Transitions {
		tr := &n.Transitions[i]
		if tr.ID != tID {
			continue
		}
		next := mk.Clone()
		for _, in := range tr.Inputs {
			next[in.Place] -= in.Weight
		}
		for _, out := range tr.Outputs {
			next[out.Place] += out.Weight
		}
		return next
	}
	return mk.Clone()
}

// EnabledTransitions lists all enabled transitions in declaration order;
// concurrent enabled transitions are all returned.
func (n *Net) EnabledTransitions(mk Marking) []string {
	out := []string{}
	for _, tr := range n.Transitions {
		if ok, _ := n.Enabled(mk, tr.ID); ok {
			out = append(out, tr.ID)
		}
	}
	return out
}

// ParseScalar converts an imported quantity into a Scalar, accepting a decimal
// integer with an optional unit suffix (e.g. "3 token", "2 slots"). The raw
// text and unit are always preserved.
func ParseScalar(raw, expectUnit string) Scalar {
	s := Scalar{Raw: raw}
	text := strings.TrimSpace(raw)
	if text == "" {
		s.Diagnostic = "empty numeric value"
		return s
	}
	unit := ""
	if i := strings.IndexAny(text, " \t"); i >= 0 {
		unit = strings.TrimSpace(text[i:])
		text = strings.TrimSpace(text[:i])
	}
	v, ok := new(big.Int).SetString(text, 10)
	if !ok {
		s.Diagnostic = "not an integer: " + raw
		return s
	}
	if expectUnit != "" && unit != "" && !unitAllowed(unit, expectUnit) {
		s.Diagnostic = fmt.Sprintf("unit %q is not compatible with %s", unit, expectUnit)
		s.Value = v
		s.Unit = unit
		return s
	}
	s.Value = v
	s.Unit = unit
	s.Resolved = true
	return s
}

var unitAliases = map[string][]string{
	"token": {"token", "tokens", "枚", "个"},
	"slot":  {"slot", "slots", "capacity", "token", "tokens"},
	"arc":   {"arc", "arcs", "weight", "token", "tokens"},
	"state": {"state", "states"},
	"step":  {"step", "steps"},
}

func unitAllowed(unit, expect string) bool {
	aliases, ok := unitAliases[expect]
	if !ok {
		return unit == expect
	}
	for _, a := range aliases {
		if unit == a {
			return true
		}
	}
	return false
}

// ParseCoord parses "x,y" coordinates preserving the coordinate basis.
func ParseCoord(rawX, rawY, basis string) *Coord {
	c := &Coord{RawX: strings.TrimSpace(rawX), RawY: strings.TrimSpace(rawY), Basis: basis}
	vx, okx := new(big.Int).SetString(c.RawX, 10)
	vy, oky := new(big.Int).SetString(c.RawY, 10)
	if !okx || !oky {
		c.Diagnostic = "coordinate components must be integers"
		return c
	}
	if !vx.IsInt64() || !vy.IsInt64() {
		c.Diagnostic = "coordinate out of range"
		return c
	}
	c.X, c.Y = vx.Int64(), vy.Int64()
	c.Resolved = true
	return c
}
