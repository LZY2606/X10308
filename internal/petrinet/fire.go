package petrinet

// DisabledReason is one machine-readable reason a transition cannot fire.
type DisabledReason string

const (
	ReasonInputShort   DisabledReason = "INPUT_TOKENS_SHORT"
	ReasonInhibited    DisabledReason = "INHIBITOR_HOLDS_TOKENS"
	ReasonCapacityFull DisabledReason = "OUTPUT_CAPACITY_EXCEEDED"
)

// InputArc is a consumed arc incident on a transition.
type InputArc struct {
	ArcID  string
	Place  string
	Weight int64
}

// OutputArc is a produced arc incident on a transition.
type OutputArc struct {
	ArcID  string
	Place  string
	Weight int64
}

// InhibitorArc blocks firing while its place holds at least its weight tokens.
type InhibitorArc struct {
	ArcID  string
	Place  string
	Weight int64
}

// Inputs returns consumed arcs for transition t in stable arc-id order.
func (n *Net) Inputs(t string) []InputArc {
	var out []InputArc
	for _, id := range n.sortedArcIDs() {
		a := n.Arcs[id]
		if a.Kind == ArcInput && a.Target == t {
			out = append(out, InputArc{ArcID: a.ID, Place: a.Source, Weight: a.Weight})
		}
	}
	return out
}

// Outputs returns produced arcs for transition t in stable arc-id order.
func (n *Net) Outputs(t string) []OutputArc {
	var out []OutputArc
	for _, id := range n.sortedArcIDs() {
		a := n.Arcs[id]
		if a.Kind == ArcOutput && a.Source == t {
			out = append(out, OutputArc{ArcID: a.ID, Place: a.Target, Weight: a.Weight})
		}
	}
	return out
}

// Inhibitors returns inhibitor arcs for transition t in stable arc-id order.
func (n *Net) Inhibitors(t string) []InhibitorArc {
	var out []InhibitorArc
	for _, id := range n.sortedArcIDs() {
		a := n.Arcs[id]
		if a.Kind == ArcInhibitor && a.Target == t {
			out = append(out, InhibitorArc{ArcID: a.ID, Place: a.Source, Weight: a.Weight})
		}
	}
	return out
}

// BlockDetail explains one failed enablement precondition.
type BlockDetail struct {
	Reason   DisabledReason `json:"reason"`
	ArcID    string         `json:"arc_id"`
	Place    string         `json:"place"`
	Required int64          `json:"required"`
	Actual   int64          `json:"actual"`
}

// Enabled reports whether t is enabled under m, considering inputs, inhibitor
// arcs and output capacities simultaneously. When false, every violated
// precondition is returned so deadlock reasons are fully attributable.
func (n *Net) Enabled(t string, m Marking) (bool, []BlockDetail) {
	var blocks []BlockDetail

	for _, in := range n.Inputs(t) {
		if m.Tokens(in.Place) < in.Weight {
			blocks = append(blocks, BlockDetail{
				Reason:   ReasonInputShort,
				ArcID:    in.ArcID,
				Place:    in.Place,
				Required: in.Weight,
				Actual:   m.Tokens(in.Place),
			})
		}
	}

	for _, inh := range n.Inhibitors(t) {
		if m.Tokens(inh.Place) >= inh.Weight {
			blocks = append(blocks, BlockDetail{
				Reason:   ReasonInhibited,
				ArcID:    inh.ArcID,
				Place:    inh.Place,
				Required: inh.Weight,
				Actual:   m.Tokens(inh.Place),
			})
		}
	}

	// Capacity is checked against the marking that results after consuming
	// inputs and producing outputs, but without mutating m. Self-loops free
	// their own tokens first, which makes capacity-aware firing exact.
	next := n.peekFire(t, m)
	for _, out := range n.Outputs(t) {
		cap := n.Capacity[out.Place]
		if cap >= 0 && next[out.Place] > cap {
			blocks = append(blocks, BlockDetail{
				Reason:   ReasonCapacityFull,
				ArcID:    out.ArcID,
				Place:    out.Place,
				Required: cap,
				Actual:   next[out.Place],
			})
		}
	}

	return len(blocks) == 0, blocks
}

// peekFire computes the post-firing marking without validating enablement.
func (n *Net) peekFire(t string, m Marking) Marking {
	next := m.Clone()
	for _, in := range n.Inputs(t) {
		next[in.Place] -= in.Weight
	}
	for _, out := range n.Outputs(t) {
		next[out.Place] += out.Weight
	}
	return next
}

// Fire enables, then fires t, returning the new marking. It returns false when
// the transition is not enabled.
func (n *Net) Fire(t string, m Marking) (Marking, bool) {
	if ok, _ := n.Enabled(t, m); !ok {
		return nil, false
	}
	return n.peekFire(t, m), true
}

// EnabledTransitions returns all enabled transitions in authored order.
func (n *Net) EnabledTransitions(m Marking) []string {
	var out []string
	for _, t := range n.TransitionIDs {
		if ok, _ := n.Enabled(t, m); ok {
			out = append(out, t)
		}
	}
	return out
}

// DeadlockReasons returns the enablement blockers for every transition under m.
// A non-empty slice over all transitions means m is a deadlock candidate.
func (n *Net) DeadlockReasons(m Marking) map[string][]BlockDetail {
	out := map[string][]BlockDetail{}
	for _, t := range n.TransitionIDs {
		if ok, blocks := n.Enabled(t, m); !ok {
			out[t] = blocks
		}
	}
	return out
}
