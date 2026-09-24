package petri

import (
	"fmt"
	"sort"
)

// EvaluateOmega is Evaluate for coverability labels (omega components).
// Input fails only when the held finite count is below weight; an omega
// source always supplies enough. Inhibitor fails on a finite count at/over
// the threshold (omega never blocks because it represents unbounded growth
// rather than an actual finite token count). Capacity applies to finite
// results only.
func (n *Net) EvaluateOmega(tid string, m OmegaMarking) EnableReason {
	a := n.adj(tid)
	r := EnableReason{TransitionID: tid}

	r.InputOK = true
	for pid, w := range a.preset {
		have := m[pid]
		if have != Omega && have < w {
			r.InputOK = false
			r.Blocking = append(r.Blocking,
				fmt.Sprintf("input:%s:need=%dtoken,have=%dtoken", pid, w, have))
		}
	}
	r.InhibitorOK = true
	for pid, threshold := range a.inhibit {
		have := m[pid]
		if have != Omega && have >= threshold {
			r.InhibitorOK = false
			r.Blocking = append(r.Blocking,
				fmt.Sprintf("inhibitor:%s:threshold<%dtoken,have=%dtoken", pid, threshold, have))
		}
	}
	r.CapacityOK = true
	if r.InputOK && r.InhibitorOK {
		for pid, produced := range a.postset {
			consumed := a.preset[pid]
			have := m[pid]
			if have == Omega {
				continue
			}
			need := have + produced - consumed
			if c := n.Places[pid].Capacity; c != nil && need > *c {
				r.CapacityOK = false
				r.Blocking = append(r.Blocking,
					fmt.Sprintf("capacity:%s:cap=%dtoken,would=%dtoken", pid, *c, need))
			}
		}
	}
	r.Enabled = r.InputOK && r.InhibitorOK && r.CapacityOK
	sort.Strings(r.Blocking)
	return r
}

func fireOmega(n *Net, tid string, m OmegaMarking) OmegaMarking {
	a := n.adj(tid)
	next := m.Clone()
	for pid, w := range a.preset {
		if next[pid] != Omega {
			next[pid] -= w
		}
	}
	for pid, w := range a.postset {
		if next[pid] == Omega {
			continue
		}
		need := next[pid] + w
		if c := n.Places[pid].Capacity; c != nil && need > *c {
			// Should never happen: capacity blocks enablement.
			need = *c
		}
		next[pid] = need
	}
	return next
}

// EnabledOf lists enabled transition ids at a stored state.
func (r *Report) EnabledOf(n *Net, sid string) []string {
	st := r.StateByID(sid)
	if st == nil {
		return nil
	}
	var out []string
	for _, tid := range n.TransitionOrder {
		if n.EvaluateOmega(tid, st.Label).Enabled {
			out = append(out, tid)
		}
	}
	return out
}

func labelMatches(label OmegaMarking, target Marking) bool {
	if !label.Finite() {
		return false
	}
	for p, v := range target {
		if label[p] != v {
			return false
		}
	}
	for p, v := range label {
		if tv, ok := target[p]; !ok && v != 0 {
			return false
		} else if ok && tv != v {
			return false
		}
	}
	return true
}

func strictAncestor(r *Report, st *State, label OmegaMarking) *State {
	cur := st.ParentID
	for cur != "" {
		anc := r.StateByID(cur)
		if anc == nil {
			break
		}
		if label.StrictlyDominates(anc.Label) {
			return anc
		}
		cur = anc.ParentID
	}
	return nil
}

func appendCand(paths []Path, p Path, cap int) []Path {
	if len(paths) >= cap {
		return paths
	}
	for _, ex := range paths {
		if pathSig(ex) == pathSig(p) {
			return paths
		}
	}
	return append(paths, p)
}

func pathSig(p Path) string {
	s := ""
	for _, st := range p.Steps {
		s += st.TransitionID + ">"
	}
	return s
}

// mergeState drops the accelerated duplicate child from the state list and
// points its parent at the canonical existing state.
func mergeState(r *Report, dup, keep *State) {
	filtered := r.States[:0]
	for _, s := range r.States {
		if s.ID != dup.ID {
			filtered = append(filtered, s)
		}
	}
	r.States = filtered
	if p := r.parent[dup.ID]; p != "" {
		r.parent[keep.ID] = p
	}
	if t := r.firedBy[dup.ID]; t != "" {
		r.firedBy[keep.ID] = t
	}
	for i := range r.Edges {
		if r.Edges[i].ToStateID == dup.ID {
			r.Edges[i].ToStateID = keep.ID
		}
	}
}

func finalizeFindings(
	r *Report, n *Net, targets []Marking,
	hits map[string]map[string]bool, deadlocks map[string][]Path,
) {
	// Target findings: reachable; unreachable when complete, unknown on budget.
	for ti, target := range targets {
		tkey := fmt.Sprintf("target-%d", ti+1)
		f := Finding{ID: "F-" + tkey, Kind: "target", Subject: n.Canonical(target)}
		hit := hits[tkey]
		switch {
		case len(hit) > 0:
			f.Status = "reachable"
			ids := sortedKeys(hit)
			for _, sid := range ids {
				p := r.PathTo(sid)
				if len(f.Candidates) < r.Config.MaxCandidatePaths {
					f.Candidates = append(f.Candidates, Candidate{
						ID: "Cand-" + tkey + "-" + sid, Kind: "reachable-path",
						PathID: p.ID, Detail: pathSig(p),
					})
				}
				f.StateID = sid
			}
		case r.Status == "unknown":
			f.Status = "unknown"
			for i, fr := range r.Frontier {
				if i >= r.Config.MaxCandidatePaths {
					break
				}
				f.Candidates = append(f.Candidates, Candidate{
					ID: "Cand-" + tkey + "-frontier-" + fr.StateID,
					Kind: "truncated-frontier", PathID: "P-" + fr.StateID,
					Detail: fr.Canonical,
				})
			}
		default:
			f.Status = "unreachable"
		}
		r.Findings = append(r.Findings, f)
	}

	// Deadlock findings: one per deadlock state, multiple legal paths kept.
	dlIDs := make([]string, 0, len(deadlocks))
	for sid := range deadlocks {
		dlIDs = append(dlIDs, sid)
	}
	sort.Strings(dlIDs)
	for _, sid := range dlIDs {
		st := r.StateByID(sid)
		f := Finding{ID: "F-deadlock-" + sid, Kind: "deadlock", Subject: st.Canonical,
			Status: "deadlock", StateID: sid}
		for i, p := range deadlocks[sid] {
			f.Candidates = append(f.Candidates, Candidate{
				ID: fmt.Sprintf("Cand-deadlock-%s-%d", sid, i+1),
				Kind: "deadlock-path", PathID: p.ID, Detail: pathSig(p),
			})
		}
		r.Findings = append(r.Findings, f)
	}

	// Findings are emitted in stable order: targets, deadlocks, unbounded.
	ordered := make([]Finding, 0, len(r.Findings))
	order := map[string]int{"target": 0, "deadlock": 1, "unbounded": 2}
	ordered = append(ordered, r.Findings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if order[ordered[i].Kind] != order[ordered[j].Kind] {
			return order[ordered[i].Kind] < order[ordered[j].Kind]
		}
		return ordered[i].ID < ordered[j].ID
	})
	r.Findings = ordered
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
