package petri

import (
	"fmt"
	"sort"
	"strings"
)

type queueItem struct {
	canon  string
	parent string
	fired  string
	depth  int
}

// Explore builds the Karp-Miller coverability tree of n against targets,
// honoring state/frontier budgets. Outputs are deterministic for identical
// (net, config, targets): state ids follow discovery order and transitions
// fire in declaration order.
func Explore(n *Net, cfg Config, targets []Marking) *Report {
	if cfg.MaxCandidatePaths <= 0 {
		cfg.MaxCandidatePaths = 3
	}
	r := &Report{
		NetID:       n.ID,
		Config:      cfg,
		Status:      "complete",
		byCanon:     map[string]*State{},
		parent:      map[string]string{},
		firedBy:     map[string]string{},
	}

	rootLabel := FromMarking(n.InitialMarking())
	rootCanon := n.CanonicalOmega(rootLabel)
	root := &State{ID: "S0", Order: 0, Depth: 0, Label: rootLabel, Canonical: rootCanon, Expanded: false}
	r.States = append(r.States, root)
	r.byCanon[rootCanon] = root
	r.RootStateID = root.ID
	r.Judgments = append(r.Judgments, Judgment{
		ID: "J-S0-canon", Rule: RuleCanonicalization, StateID: root.ID,
		Outcome: "canonical", Detail: rootCanon,
	})

	queue := []queueItem{{canon: rootCanon, depth: 0}}
	expanded := 0
	frontierOrder := 0
	edgeSeen := map[string]bool{}
	coverSeen := map[string]bool{}
	targetHits := map[string]map[string]bool{} // targetKey -> state ids hit
	unboundedSeen := map[string]bool{}
	deadlockCands := map[string][]Path{}

	for len(queue) > 0 {
		var item queueItem
		if cfg.Strategy == StrategyDFS {
			item = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			item = queue[0]
			queue = queue[1:]
		}
		st := r.byCanon[item.canon]
		if st == nil || st.Expanded {
			continue
		}

		if expanded >= cfg.StateBudget {
			r.Status = "unknown"
			r.Frontier = append(r.Frontier, FrontierEntry{
				StateID: st.ID, ParentID: item.parent, TransitionID: item.fired,
				Canonical: st.Canonical, QueueOrder: frontierOrder,
			})
			frontierOrder++
			r.Judgments = append(r.Judgments, Judgment{
				ID: fmt.Sprintf("J-%s-budget", st.ID), Rule: RuleBudgetUnknown, StateID: st.ID,
				Outcome: "unknown",
				Detail:  fmt.Sprintf("stateBudget=%d exhausted; frontier retained", cfg.StateBudget),
			})
			continue
		}
		expanded++
		st.Expanded = true
		st.ParentID = item.parent
		st.FiredBy = item.fired

		// Enablement is evaluated against input, inhibitor, and capacity.
		var enabled []string
		for _, tid := range n.TransitionOrder {
			reason := n.Evaluate(tid, st.Label)
			outcome := "disabled"
			if reason.Enabled {
				outcome = "enabled"
				enabled = append(enabled, tid)
			}
			r.Judgments = append(r.Judgments, Judgment{
				ID: fmt.Sprintf("J-%s-%s-en", st.ID, tid), Rule: RuleEnablement,
				StateID: st.ID, TransitionID: tid, Outcome: outcome, Reason: reason,
			})
		}

		// Deadlock: no enabled transition. All blocking reasons are evidence.
		if len(enabled) == 0 {
			var blocks []string
			for _, tid := range n.TransitionOrder {
				rs := n.Evaluate(tid, st.Label)
				blocks = append(blocks, rs.Blocking...)
			}
			sort.Strings(blocks)
			r.Judgments = append(r.Judgments, Judgment{
				ID: fmt.Sprintf("J-%s-deadlock", st.ID), Rule: RuleDeadlock, StateID: st.ID,
				Outcome: "deadlock", Detail: strings.Join(blocks, ";"),
			})
			p := r.PathTo(st.ID)
			deadlockCands[st.ID] = appendCand(deadlockCands[st.ID], p, cfg.MaxCandidatePaths)
		}

		// Targets.
		for ti, target := range targets {
			tkey := fmt.Sprintf("target-%d", ti+1)
			if labelMatches(st.Label, target) {
				if targetHits[tkey] == nil {
					targetHits[tkey] = map[string]bool{}
				}
				targetHits[tkey][st.ID] = true
				r.Judgments = append(r.Judgments, Judgment{
					ID: fmt.Sprintf("J-%s-%s", st.ID, tkey), Rule: RuleTargetReach,
					StateID: st.ID, Outcome: "matched", Detail: n.Canonical(target),
				})
			}
		}

		// Karp-Miller acceleration using strict ancestors.
		label := st.Label
		if ancestor := strictAncestor(r, st, label); ancestor != nil {
			acc := label.Clone()
			var unbPlaces []string
			for _, pid := range n.PlaceOrder {
				if label[pid] != Omega && acc[pid] != ancestor.Label[pid] {
					acc[pid] = Omega
					unbPlaces = append(unbPlaces, pid)
				}
			}
			if len(unbPlaces) > 0 {
				sort.Strings(unbPlaces)
				key := ancestor.ID + ">" + st.ID
				if !coverSeen[key] {
					coverSeen[key] = true
					r.Covers = append(r.Covers, CoverRelation{
						ID: "C-" + ancestor.ID + "-" + st.ID, CoveringID: st.ID,
						CoveredID: ancestor.ID, Accelerated: true,
					})
					r.Judgments = append(r.Judgments, Judgment{
						ID: fmt.Sprintf("J-%s-accel", st.ID), Rule: RuleCoverAcceleration, StateID: st.ID,
						Outcome: "omega", Detail: "unbounded:" + strings.Join(unbPlaces, ","),
					})
				}
				for _, pid := range unbPlaces {
					if !unboundedSeen[pid] {
						unboundedSeen[pid] = true
						r.Findings = append(r.Findings, Finding{
							ID: "F-unbounded-" + pid, Kind: "unbounded", Subject: pid,
							Status: "unbounded", StateID: st.ID,
						})
					}
				}
				label = acc
				st.Label = acc
				st.Canonical = n.CanonicalOmega(acc)
				if existing := r.byCanon[st.Canonical]; existing != nil && existing.ID != st.ID {
					mergeState(r, st, existing)
					st = existing
					continue
				}
				r.byCanon[st.Canonical] = st
			}
		}

		// Fire successors in transition declaration order.
		for _, tid := range enabled {
			next := fireOmega(n, tid, label)
			ncanon := n.CanonicalOmega(next)
			r.Judgments = append(r.Judgments, Judgment{
				ID: fmt.Sprintf("J-%s-%s-canon", st.ID, tid), Rule: RuleCanonicalization,
				StateID: st.ID, TransitionID: tid, Outcome: "successor", Detail: ncanon,
			})
			ekey := st.ID + "|" + tid + "|" + ncanon
			if existing := r.byCanon[ncanon]; existing != nil {
				if !edgeSeen[ekey] {
					edgeSeen[ekey] = true
					r.Edges = append(r.Edges, Edge{
						ID: "E-" + st.ID + "-" + tid + "-" + existing.ID,
						FromStateID: st.ID, ToStateID: existing.ID, TransitionID: tid,
					})
				}
				// Extra legal path to an existing deadlock state.
				if len(r.EnabledOf(existing.ID)) == 0 {
					p := r.PathTo(st.ID)
					p.ID = "P-" + st.ID + "-" + tid + "-" + existing.ID
					p.Steps = append(p.Steps, PathStep{TransitionID: tid, FromStateID: st.ID, ToStateID: existing.ID})
					deadlockCands[existing.ID] = appendCand(deadlockCands[existing.ID], p, cfg.MaxCandidatePaths)
				}
				continue
			}

			child := &State{
				ID: fmt.Sprintf("S%d", len(r.States)), Order: len(r.States),
				Depth: item.depth + 1, Label: next, Canonical: ncanon,
			}
			r.States = append(r.States, child)
			r.byCanon[ncanon] = child
			r.parent[child.ID] = st.ID
			r.firedBy[child.ID] = tid
			r.Edges = append(r.Edges, Edge{
				ID: "E-" + st.ID + "-" + tid + "-" + child.ID,
				FromStateID: st.ID, ToStateID: child.ID, TransitionID: tid,
			})
			queue = append(queue, queueItem{canon: ncanon, parent: st.ID, fired: tid, depth: item.depth + 1})
		}
	}

	finalizeFindings(r, n, targets, targetHits, deadlockCands)
	return r
}
