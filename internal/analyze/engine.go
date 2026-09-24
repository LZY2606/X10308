package analyze

import (
	"sort"
	"strconv"

	"petriws/internal/domain"
)

// AdmissibleError indicates the net cannot be explored (bad references or
// unconvertible quantities). Diagnostics remain attached to the source.
type AdmissibleError struct{ Msg string }

func (e *AdmissibleError) Error() string { return e.Msg }

// Build normalizes a NetSpec for exploration. Unconvertible quantities or
// dangling references make the net inadmissible; the raw material and
// diagnostics are still retained by the source layer.
func Build(spec *domain.NetSpec) (*Net, error) {
	if len(spec.Diagnostics) > 0 {
		return nil, &AdmissibleError{Msg: "net has unresolved diagnostics; exploration blocked"}
	}
	n := &Net{
		spec:     spec,
		placeIdx: map[string]int{},
		preset:   map[string][]arcRef{},
		postset:  map[string][]arcRef{},
	}
	for _, p := range spec.PlaceIDs() {
		n.placeIdx[p] = len(n.places)
		n.places = append(n.places, p)
	}
	pm := spec.PlaceMap()
	for _, t := range orderedTransitions(spec) {
		n.transitions = append(n.transitions, t)
		n.preset[t] = nil
		n.postset[t] = nil
	}
	for _, a := range spec.Arcs {
		fromT := spec.TransitionMap()[a.Source] != nil
		toT := spec.TransitionMap()[a.Target] != nil
		w := a.Weight.Value
		if !a.Weight.Valid {
			w = 1
		}
		switch {
		case !fromT && toT: // place -> transition (input)
			n.preset[a.Target] = append(n.preset[a.Target], arcRef{a.Source, w, a.StableID, a.Kind})
		case fromT && !toT: // transition -> place (output)
			n.postset[a.Source] = append(n.postset[a.Source], arcRef{a.Target, w, a.StableID, a.Kind})
		case fromT && toT:
			return nil, &AdmissibleError{Msg: "arc between two transitions not supported"}
		default:
			return nil, &AdmissibleError{Msg: "arc between two places not supported"}
		}
	}
	n.capacity = make([]int64, len(n.places))
	for id, p := range pm {
		if p.Infinite || p.Capacity.Raw == "" {
			n.capacity[n.placeIdx[id]] = Omega
		} else {
			n.capacity[n.placeIdx[id]] = p.Capacity.Value
		}
	}
	return n, nil
}

func orderedTransitions(spec *domain.NetSpec) []string {
	var out []string
	for _, nd := range spec.Nodes {
		if nd.Kind == domain.KindTransition {
			out = append(out, nd.StableID)
		}
	}
	return out
}

// initialMarking builds the initial marking from authored init quantities.
func (n *Net) initialMarking() map[string]int64 {
	m := map[string]int64{}
	pm := n.spec.PlaceMap()
	for _, p := range n.places {
		m[p] = pm[p].Init.Value
	}
	return m
}

// enabled reports whether t may fire, evaluating inputs, inhibitors and
// capacity together. The returned reason is populated when disabled.
func (n *Net) enabled(t string, m map[string]int64) (bool, BlockReason) {
	br := BlockReason{Transition: t}
	ok := true
	for _, in := range n.preset[t] {
		switch in.kind {
		case domain.ArcInhibitor:
			have := m[in.place]
			if have >= in.weight {
				ok = false
				br.Inhibitors = append(br.Inhibitors, Inhibit{
					Place: in.place, Have: have, Limit: in.weight, ArcID: in.arcID,
				})
			}
		default:
			have := m[in.place]
			if have < in.weight {
				ok = false
				br.Inputs = append(br.Inputs, Need{
					Place: in.place, Have: have, Need: in.weight, ArcID: in.arcID, Weight: in.weight,
				})
			}
		}
	}
	// Determine resulting marking for capacity check.
	delta := map[string]int64{}
	for _, in := range n.preset[t] {
		if in.kind == domain.ArcNormal {
			delta[in.place] -= in.weight
		}
	}
	for _, out := range n.postset[t] {
		delta[out.place] += out.weight
	}
	for _, p := range sortedDeltaKeys(delta) {
		idx := n.placeIdx[p]
		res := m[p] + delta[p]
		capv := n.capacity[idx]
		if capv != Omega && res > capv {
			ok = false
			br.Capacity = append(br.Capacity, CapBlock{
				Place: p, ResultTokens: res, Capacity: capv,
			})
		}
	}
	return ok, br
}

// fire produces the successor marking (caller guarantees enabled).
func (n *Net) fire(t string, m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, in := range n.preset[t] {
		if in.kind == domain.ArcNormal {
			out[in.place] -= in.weight
		}
	}
	for _, o := range n.postset[t] {
		out[o.place] += o.weight
	}
	return out
}

func sortedDeltaKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func key(m map[string]int64, places []string) string {
	return domain.Marking(m).CanonicalKey(places)
}

// markingFromKey reconstructs a marking from a canonical key.
func markingFromKey(k string) map[string]int64 {
	m := map[string]int64{}
	for _, part := range splitKey(k) {
		for j := 0; j < len(part); j++ {
			if part[j] == ':' {
				if v, err := strconv.ParseInt(part[j+1:], 10, 64); err == nil {
					m[part[:j]] = v
				}
				break
			}
		}
	}
	return m
}

func splitKey(k string) []string {
	var out []string
	start := 0
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			out = append(out, k[start:i])
			start = i + 1
		}
	}
	out = append(out, k[start:])
	return out
}
