package petri

// BlockReason describes a single failed enablement clause.
type BlockReason struct {
	Code    string `json:"code"`
	PlaceID string `json:"place_id,omitempty"`
	ArcID   string `json:"arc_id,omitempty"`
	Have    int64  `json:"have,omitempty"`
	Need    int64  `json:"need,omitempty"`
	Detail  string `json:"detail"`
}

// EnablementResult is the full three-clause verdict for a transition.
// Input tokens, inhibitor thresholds and post-fire capacities are evaluated
// together; one missing clause is enough to disable the transition.
type EnablementResult struct {
	TransitionID string        `json:"transition_id"`
	Enabled      bool          `json:"enabled"`
	Reasons      []BlockReason `json:"reasons,omitempty"`
}

// CheckEnablement evaluates input arcs, inhibitor arcs and capacities.
//
// A transition t is enabled at marking M iff:
//  1. inputs:   for every place->t arc of weight w,     M(p) >= w
//  2. inhibitor: for every inhibitor place->t threshold h, M(p) <  h
//  3. capacity:  for every t->place arc of weight w and finite cap c,
//                M(p) - consumed(p) + produced(p) <= c
//
// A place with an unconvertible (unknown) capacity is treated as unbounded,
// the corresponding diagnostic stays attached to the model.
func (m *Model) CheckEnablement(tID string, marking Marking) EnablementResult {
	res := EnablementResult{TransitionID: tID, Enabled: true}
	next := map[string]int64{}
	for _, p := range m.placeOrder {
		next[p] = marking[p]
	}
	for _, a := range m.inArc[tID] {
		next[a.Src] -= a.Weight
	}
	for _, a := range m.outArc[tID] {
		next[a.Dst] += a.Weight
	}
	for _, a := range m.inArc[tID] {
		if marking[a.Src] < a.Weight {
			res.Enabled = false
			res.Reasons = append(res.Reasons, BlockReason{
				Code: "input_unsatisfied", PlaceID: a.Src, ArcID: a.ID,
				Have: marking[a.Src], Need: a.Weight,
				Detail: "input place holds fewer tokens than the weighted arc requires",
			})
		}
	}
	for _, a := range m.inhibArc[tID] {
		if marking[a.Src] >= a.Weight {
			res.Enabled = false
			res.Reasons = append(res.Reasons, BlockReason{
				Code: "inhibitor_blocked", PlaceID: a.Src, ArcID: a.ID,
				Have: marking[a.Src], Need: a.Weight,
				Detail: "inhibitor place reaches or exceeds the threshold",
			})
		}
	}
	caps := map[string]*int64{}
	for _, p := range m.Places {
		caps[p.ID] = p.Cap
	}
	for _, a := range m.outArc[tID] {
		if c := caps[a.Dst]; c != nil && next[a.Dst] > *c {
			res.Enabled = false
			res.Reasons = append(res.Reasons, BlockReason{
				Code: "capacity_exceeded", PlaceID: a.Dst, ArcID: a.ID,
				Have: next[a.Dst], Need: *c,
				Detail: "firing would push the place beyond its capacity",
			})
		}
	}
	return res
}

// EnabledTransitions returns the full verdict for every transition.
func (m *Model) EnabledTransitions(marking Marking) []EnablementResult {
	out := make([]EnablementResult, 0, len(m.Transitions))
	for _, t := range m.Transitions {
		out = append(out, m.CheckEnablement(t.ID, marking))
	}
	return out
}

// Fire produces the successor marking. Callers must check enablement first;
// the returned marking is a fresh map.
func (m *Model) Fire(tID string, marking Marking) Marking {
	next := Marking{}
	for _, p := range m.placeOrder {
		next[p] = marking[p]
	}
	for _, a := range m.inArc[tID] {
		next[a.Src] -= a.Weight
	}
	for _, a := range m.outArc[tID] {
		next[a.Dst] += a.Weight
	}
	return next
}

// Canonical serializes a marking without merging places whose tokens differ.
// The output is stable across map iteration and includes every place that
// ever held a token, so the same graph with different markings yields
// different canonical keys.
func (m *Model) Canonical(marking Marking) string {
	ids := make([]string, 0, len(marking))
	known := map[string]bool{}
	for _, id := range m.placeOrder {
		if marking[id] != 0 {
			ids = append(ids, id)
			known[id] = true
		}
	}
	extra := []string{}
	for id := range marking {
		if !known[id] && marking[id] != 0 {
			extra = append(extra, id)
		}
	}
	sortStrings(extra)
	ids = append(ids, extra...)
	buf := make([]byte, 0, 8*len(ids))
	for i, id := range ids {
		if i > 0 {
			buf = append(buf, '|')
		}
		buf = append(buf, id...)
		buf = append(buf, ':')
		buf = appendInt(buf, marking[id])
	}
	return string(buf)
}

// IsSelfLoop reports whether an input arc is paired with an output arc on the
// same place/transition pair (the place is tested/refilled by the firing).
func (m *Model) IsSelfLoop(tID, placeID string) bool {
	in, out := false, false
	for _, a := range m.inArc[tID] {
		if a.Src == placeID {
			in = true
		}
	}
	for _, a := range m.outArc[tID] {
		if a.Dst == placeID {
			out = true
		}
	}
	return in && out
}

func appendInt(b []byte, v int64) []byte {
	if v == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	return append(b, tmp[i:]...)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
