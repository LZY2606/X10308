package petrinet

import (
	"sort"
	"strconv"
	"strings"
)

// Marking is a concrete token assignment. Canonicalization must never merge
// two markings that differ in tokens, so identity is the exact token vector
// rendered in authored place order.
type Marking struct {
	Tokens map[string]int64
}

// Clone returns an independent copy.
func (m Marking) Clone() Marking {
	c := Marking{Tokens: make(map[string]int64, len(m.Tokens))}
	for k, v := range m.Tokens {
		c.Tokens[k] = v
	}
	return c
}

// Equal reports exact token equality.
func (m Marking) Equal(o Marking) bool {
	if len(m.Tokens) != len(o.Tokens) {
		return false
	}
	for k, v := range m.Tokens {
		if ov, ok := o.Tokens[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

// Key is the canonical string identity of a marking over a fixed place order.
// Different token vectors always produce different keys.
func (m Marking) Key(order []string) string {
	var b strings.Builder
	for i, p := range order {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(p)
		b.WriteByte('=')
		b.WriteString(strconv.FormatInt(m.Tokens[p], 10))
	}
	return b.String()
}

// Covers reports componentwise m >= o (the cover relation on token vectors).
func (m Marking) Covers(o Marking) bool {
	for k, v := range o.Tokens {
		if m.Tokens[k] < v {
			return false
		}
	}
	return true
}

// SortedPlaces yields place ids in deterministic order (fallback when no
// external order is supplied).
func (m Marking) SortedPlaces() []string {
	ids := make([]string, 0, len(m.Tokens))
	for k := range m.Tokens {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// DisableReason explains why a transition is not enabled, if any. Empty string
// means enabled. Enablement requires ALL of:
//   - every input arc place holds at least its weight;
//   - every inhibitor arc place holds strictly fewer than its weight;
//   - after firing, every output/input-affected place stays within capacity.
type DisableReason struct {
	Transition string
	Code       string // missing_input | inhibited | capacity
	Detail     string
	Place      string
	Short      int64  // for missing_input: tokens still needed
	Have       int64  // current tokens at the offending place
	Limit      int64  // relevant bound
}

// Enabled reports whether t can fire from m.
func (n *NormalizedNet) Enabled(m Marking, t string) bool {
	return n.DisableReason(m, t) == nil
}

// DisableReason computes the first blocking reason (deterministic order), or
// nil when the transition is enabled.
func (n *NormalizedNet) DisableReason(m Marking, t string) *DisableReason {
	consume := map[string]int64{}
	produce := map[string]int64{}
	inhibited := []Arc{}
	for _, a := range n.Arcs {
		if a.To == t && a.Kind == ArcInput {
			consume[a.From] += a.Weight
		}
		if a.From == t && a.Kind == ArcOutput {
			produce[a.To] += a.Weight
		}
		if a.To == t && a.Kind == ArcInhibitor {
			inhibited = append(inhibited, a)
		}
	}
	// 1) inputs
	for _, pid := range sortedKeys(consume) {
		need := consume[pid]
		have := m.Tokens[pid]
		if have < need {
			return &DisableReason{
				Transition: t, Code: "missing_input", Place: pid,
				Short: need - have, Have: have, Limit: need,
				Detail: "place " + pid + " has " + strconv.FormatInt(have, 10) +
					" tokens but arc requires " + strconv.FormatInt(need, 10),
			}
		}
	}
	// 2) inhibitors
	for _, a := range inhibited {
		have := m.Tokens[a.From]
		if have >= a.Weight {
			return &DisableReason{
				Transition: t, Code: "inhibited", Place: a.From,
				Have: have, Limit: a.Weight,
				Detail: "inhibitor from " + a.From + " blocks while tokens (" +
					strconv.FormatInt(have, 10) + ") >= " + strconv.FormatInt(a.Weight, 10),
			}
		}
	}
	// 3) capacity after firing
	delta := map[string]int64{}
	for p, v := range consume {
		delta[p] -= v
	}
	for p, v := range produce {
		delta[p] += v
	}
	for _, pid := range sortedKeys(delta) {
		pl, ok := n.Place(pid)
		if !ok {
			continue
		}
		if pl.Capacity < 0 {
			continue
		}
		after := m.Tokens[pid] + delta[pid]
		if after > pl.Capacity {
			return &DisableReason{
				Transition: t, Code: "capacity", Place: pid,
				Have: after, Limit: pl.Capacity,
				Detail: "filling would put " + strconv.FormatInt(after, 10) +
					" tokens in " + pid + " exceeding capacity " + strconv.FormatInt(pl.Capacity, 10),
			}
		}
	}
	return nil
}

// Fire computes the successor marking. Self loops (a place appearing as both
// input and output of t) naturally return the same token count. The caller is
// expected to check Enabled first.
func (n *NormalizedNet) Fire(m Marking, t string) Marking {
	next := m.Clone()
	for _, a := range n.Arcs {
		if a.To == t && (a.Kind == ArcInput) {
			next.Tokens[a.From] -= a.Weight
		}
		if a.From == t && a.Kind == ArcOutput {
			next.Tokens[a.To] += a.Weight
		}
	}
	return next
}

// EnabledTransitions returns all enabled transitions in authored order. When
// more than one is enabled the net has concurrency; exploration keeps every
// legal candidate rather than forcing a single choice.
func (n *NormalizedNet) EnabledTransitions(m Marking) []string {
	out := []string{}
	for _, t := range n.TransitionOrder {
		if n.DisableReason(m, t) == nil {
			out = append(out, t)
		}
	}
	return out
}

// DeadlockReasons returns a deterministic explanation per transition when none
// is enabled.
func (n *NormalizedNet) DeadlockReasons(m Marking) []DisableReason {
	out := []DisableReason{}
	for _, t := range n.TransitionOrder {
		if r := n.DisableReason(m, t); r != nil {
			out = append(out, *r)
		}
	}
	return out
}

func sortedKeys(m map[string]int64) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
