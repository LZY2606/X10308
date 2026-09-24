package pnnet

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Marking is a token assignment over place ids. Absent id means zero tokens.
// Markings are never merged when their tokens differ: each distinct token
// vector is its own state.
type Marking map[string]int64

// Clone returns an independent copy.
func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Get with zero default.
func (m Marking) Get(pid string) int64 { return m[pid] }

// Canonical renders the marking as a stable "p1=n,p2=m" string over the net's
// sorted place id order. Two markings share a key iff every place has the same
// token count; token-different markings never canonicalize together.
func (m Marking) Canonical(placeIDs []string) string {
	parts := make([]string, 0, len(placeIDs))
	for _, pid := range placeIDs {
		parts = append(parts, pid+"="+strconv.FormatInt(m[pid], 10))
	}
	// Include any unknown place ids defensively, sorted.
	known := map[string]bool{}
	for _, pid := range placeIDs {
		known[pid] = true
	}
	var extra []string
	for pid, v := range m {
		if !known[pid] {
			extra = append(extra, pid+"="+strconv.FormatInt(v, 10))
		}
	}
	sort.Strings(extra)
	return strings.Join(append(parts, extra...), ",")
}

// Vector returns tokens in placeIDs order.
func (m Marking) Vector(placeIDs []string) []int64 {
	v := make([]int64, len(placeIDs))
	for i, pid := range placeIDs {
		v[i] = m[pid]
	}
	return v
}

// Covers reports m >= other on every place (coverability order).
func (m Marking) Covers(other Marking) bool {
	for pid, v := range other {
		if m[pid] < v {
			return false
		}
	}
	return true
}

// Equal componentwise.
func (m Marking) Equal(other Marking) bool {
	if len(m) != len(other) {
		// lengths may differ because of zero-value omission; compare contents.
	}
	for pid, v := range other {
		if m[pid] != v {
			return false
		}
	}
	for pid, v := range m {
		if other[pid] != v {
			return false
		}
	}
	return true
}

// BlockReason explains why a transition is not enabled. Multiple reasons can
// hold at once (e.g. insufficient input and a saturated output place).
type BlockReason struct {
	Code   string `json:"code"`
	Place  string `json:"place,omitempty"`
	Have   int64  `json:"have,omitempty"`
	Need   int64  `json:"need,omitempty"`
	Detail string `json:"detail,omitempty"`
}

const (
	ReasonInputShort    = "input_short"   // not enough tokens on an input place
	ReasonInhibited     = "inhibited"     // inhibitor place holds >= inhibitor weight
	ReasonCapacityFull  = "capacity_full" // postset place would exceed finite capacity
	ReasonUnconvertible = "unconvertible" // a needed weight/capacity could not be normalized
)

// Enabled reports whether the transition can fire under the joint
// enablement rule: input arcs satisfied AND every inhibitor is below its
// threshold AND firing would not exceed any finite place capacity.
//
// When an arc weight or capacity participating in the test is unconvertible,
// the transition is treated as not enabled and the reason set records it; the
// raw value is preserved on the net rather than silently guessed.
func (n *Net) Enabled(tid string, m Marking) (bool, []BlockReason) {
	var reasons []BlockReason

	for _, ref := range n.Preset(tid) {
		if ref.Arc.Weight.Value == nil {
			reasons = append(reasons, BlockReason{
				Code: ReasonUnconvertible, Place: ref.Arc.From,
				Detail: "input arc weight unconvertible: " + ref.Arc.Weight.Text,
			})
			continue
		}
		if have := m[ref.Arc.From]; have < ref.Weight {
			reasons = append(reasons, BlockReason{
				Code: ReasonInputShort, Place: ref.Arc.From, Have: have, Need: ref.Weight,
			})
		}
	}
	for _, ref := range n.Inhibitors(tid) {
		if ref.Arc.Weight.Value == nil {
			reasons = append(reasons, BlockReason{
				Code: ReasonUnconvertible, Place: ref.Arc.From,
				Detail: "inhibitor arc weight unconvertible: " + ref.Arc.Weight.Text,
			})
			continue
		}
		if have := m[ref.Arc.From]; have >= ref.Weight {
			reasons = append(reasons, BlockReason{
				Code: ReasonInhibited, Place: ref.Arc.From, Have: have, Need: ref.Weight,
			})
		}
	}

	// Compute tentative post-firing marking for the capacity test. Self loops
	// cancel out numerically per place but are still tested above.
	delta := map[string]int64{}
	for _, ref := range n.Postset(tid) {
		if ref.Arc.Weight.Value == nil {
			reasons = append(reasons, BlockReason{
				Code: ReasonUnconvertible, Place: ref.Arc.To,
				Detail: "output arc weight unconvertible: " + ref.Arc.Weight.Text,
			})
			continue
		}
		delta[ref.Arc.To] += ref.Weight
	}
	for _, ref := range n.Preset(tid) {
		if ref.Arc.Weight.Value != nil {
			delta[ref.Arc.From] -= ref.Weight
		}
	}
	places := n.PlaceByID()
	for pid, d := range delta {
		if d <= 0 {
			continue
		}
		p, ok := places[pid]
		if !ok {
			continue
		}
		if p.Capacity.Value == nil {
			if p.Capacity.Text != "" && len(p.Capacity.Diagnostics) > 0 {
				reasons = append(reasons, BlockReason{
					Code: ReasonUnconvertible, Place: pid,
					Detail: "capacity unconvertible: " + p.Capacity.Text,
				})
			}
			continue // unbounded
		}
		if m[pid]+d > *p.Capacity.Value {
			reasons = append(reasons, BlockReason{
				Code: ReasonCapacityFull, Place: pid,
				Have: m[pid], Need: m[pid] + d,
				Detail: fmt.Sprintf("capacity %d would be exceeded", *p.Capacity.Value),
			})
		}
	}
	return len(reasons) == 0, reasons
}

// EnabledTransitions lists all enabled transitions in the net's stable order.
func (n *Net) EnabledTransitions(m Marking) []string {
	var out []string
	for _, tid := range n.SortedTransitionIDs() {
		if ok, _ := n.Enabled(tid, m); ok {
			out = append(out, tid)
		}
	}
	return out
}

// Fire produces the successor marking. It returns an error when the transition
// is disabled; exploration code checks Enabled first but this keeps firing
// honest for direct callers.
func (n *Net) Fire(tid string, m Marking) (Marking, error) {
	if ok, reasons := n.Enabled(tid, m); !ok {
		return nil, fmt.Errorf("transition %s not enabled: %v", tid, reasons)
	}
	next := m.Clone()
	for _, ref := range n.Postset(tid) {
		next[ref.Arc.To] += ref.Weight
	}
	for _, ref := range n.Preset(tid) {
		next[ref.Arc.From] -= ref.Weight
	}
	return next, nil
}

// Deadlock reports a marking with no enabled transition, returning the
// blocking reason set per transition for the "deadlock reasons" view.
func (n *Net) DeadlockInfo(m Marking) map[string][]BlockReason {
	info := map[string][]BlockReason{}
	any := false
	for _, tid := range n.SortedTransitionIDs() {
		if ok, reasons := n.Enabled(tid, m); ok {
			any = true
		} else {
			info[tid] = reasons
		}
	}
	if any {
		return nil
	}
	if len(n.Transitions) == 0 {
		return map[string][]BlockReason{}
	}
	return info
}

var ErrUnknownMarkingFormat = errors.New("unrecognized marking text")
