package petrinet

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Enablement considers all three constraints at once:
//  1. input arcs: every normal p->t has at least its weight of tokens,
//  2. inhibitor arcs: t is blocked while p holds >= inhibitor weight,
//  3. capacity: after firing, no bounded place exceeds its capacity.
// A self loop (t both consumes and produces p) consumes first, so its
// intermediate dip is what matters for capacity; input still requires tokens.

type EnableReason struct {
	ArcID   string `json:"arc_id"`
	PlaceID string `json:"place_id"`
	Kind    string `json:"input" | "inhibitor" | "capacity"`
	Detail  string `json:"detail"`
	Have    int64  `json:"have,omitempty"`
	Need    int64  `json:"need,omitempty"`
}

type EnableResult struct {
	TransitionID string         `json:"transition_id"`
	Enabled      bool           `json:"enabled"`
	Reasons      []EnableReason `json:"reasons"`
}

type ArcUse struct {
	PlaceID  string
	Consumes int64
	Produces int64
	Inhibit  int64 // 0 when the arc is not an inhibitor
	ArcIDs   []string
}

func (n Net) placeIndex() map[string]*Place {
	m := map[string]*Place{}
	for i := range n.Places {
		m[n.Places[i].ID] = &n.Places[i]
	}
	return m
}

// Uses returns the per-place consumption/production/inhibition effects.
func (n Net) Uses(tid string) map[string]*ArcUse {
	u := map[string]*ArcUse{}
	get := func(pid string) *ArcUse {
		x, ok := u[pid]
		if !ok {
			x = &ArcUse{PlaceID: pid}
			u[pid] = x
		}
		return x
	}
	for _, a := range n.Arcs {
		if a.Target != tid {
			continue
		}
		w := a.Weight.Value
		if w < 0 {
			w = 0
		}
		x := get(a.Source)
		x.ArcIDs = append(x.ArcIDs, a.ID)
		if a.Kind == "inhibitor" {
			x.Inhibit = w
		} else {
			x.Consumes += w
		}
	}
	// Producing arcs are transition -> place.
	for _, a := range n.Arcs {
		if a.Source == tid && a.Kind == "normal" {
			x := get(a.Target)
			x.ArcIDs = append(x.ArcIDs, a.ID)
			x.Produces += a.Weight.Value
		}
	}
	return u
}

func (n Net) inScope(tid string, scope map[string]bool) bool {
	return scope == nil || scope[tid]
}

// CheckEnable evaluates one transition under a marking.
func (m Model) CheckEnable(tid string, at Marking) EnableResult {
	n := m.Net
	res := EnableResult{TransitionID: tid, Enabled: true}
	uses := n.Uses(tid)
	pidx := n.placeIndex()
	order := make([]string, 0, len(uses))
	for pid := range uses {
		order = append(order, pid)
	}
	sort.Strings(order)
	for _, pid := range order {
		u := uses[pid]
		have := at[pid]
		if u.Consumes > 0 && have < u.Consumes {
			res.Enabled = false
			res.Reasons = append(res.Reasons, EnableReason{
				PlaceID: pid, Kind: "input", ArcID: strings.Join(u.ArcIDs, "+"),
				Have: have, Need: u.Consumes,
				Detail: "input arc requires more tokens than held",
			})
		}
		if u.Inhibit > 0 && have >= u.Inhibit {
			res.Enabled = false
			res.Reasons = append(res.Reasons, EnableReason{
				PlaceID: pid, Kind: "inhibitor", ArcID: strings.Join(u.ArcIDs, "+"),
				Have: have, Need: u.Inhibit,
				Detail: "inhibitor arc blocks transition while tokens >= weight",
			})
		}
		after := have - u.Consumes + u.Produces
		if pl, ok := pidx[pid]; ok && pl.Capacity.ConvertOK && pl.Capacity.Value >= 0 && after > pl.Capacity.Value {
			res.Enabled = false
			res.Reasons = append(res.Reasons, EnableReason{
				PlaceID: pid, Kind: "capacity", ArcID: strings.Join(u.ArcIDs, "+"),
				Have: after, Need: pl.Capacity.Value,
				Detail: "firing would exceed place capacity",
			})
		}
	}
	return res
}

func (m Model) Fire(tid string, at Marking) Marking {
	out := Marking{}
	for k, v := range at {
		out[k] = v
	}
	for pid, u := range m.Net.Uses(tid) {
		out[pid] = at[pid] - u.Consumes + u.Produces
	}
	return out
}

// CanonicalKey serializes a token vector over the full, sorted place set.
// States with different token counts produce different keys; equal vectors
// always share a key. This is the canonicalization boundary: it merges only
// identical markings, never different ones.
func (n Net) CanonicalKey(m Marking) string {
	ids := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte('=')
		b.WriteString(intToStr(m[id]))
		b.WriteByte(';')
	}
	return b.String()
}

// StateID is a stable content identity derived from the canonical vector.
func StateID(n Net, m Marking) string {
	sum := sha256.Sum256([]byte(n.CanonicalKey(m)))
	return "st_" + hex.EncodeToString(sum[:10])
}

func intToStr(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
