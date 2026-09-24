package petri

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// ExploreRequest is the versioned strategy + budget for one derived run.
type ExploreRequest struct {
	Strategy string         // "bfs" | "dfs"
	Budget   int            // max NEW states
	MaxDepth int
	Target   Marking        // requested target marking
	TargetRaw string        // original representation of the goal
}

func sha8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// StateID is the stable identity of a marking: hashing its canonical text.
func StateID(canonical string) string { return "s-" + sha8(canonical) }

// Valid reports whether every imported quantity needed by the engine could be
// normalized. Raw representations are always retained; only derivation stops.
func (c *Compiled) Valid() (bool, []Diagnostic) {
	if c.Blocked {
		return false, c.Diagnostics
	}
	var bad []Diagnostic
	for _, p := range c.PlaceOrder {
		pi := c.Places[p]
		if pi.InitRaw.Diagnostic != "" {
			bad = append(bad, Diagnostic{Code: "unconvertible-tokens", ObjectID: p, Path: "tokens", Message: pi.InitRaw.Diagnostic})
		}
		if pi.CapRaw.Diagnostic != "" {
			bad = append(bad, Diagnostic{Code: "unconvertible-capacity", ObjectID: p, Path: "capacities", Message: pi.CapRaw.Diagnostic})
		}
	}
	if len(bad) > 0 {
		return false, append(append([]Diagnostic{}, c.Diagnostics...), bad...)
	}
	return true, c.Diagnostics
}

const maxCandidates = 8

// Explore runs reachability and records the full machine-readable judgement
// trail. Budget exhaustion yields status "unknown" with the frontier kept.
func (c *Compiled) Explore(req ExploreRequest) *Result {
	runSeed := fmt.Sprintf("%s|%s|%d|%d|%s", c.BundleID, c.ModelVersion, req.Budget, req.MaxDepth, req.TargetRaw)
	res := &Result{
		RunID:    "r-" + sha8(runSeed),
		Strategy: req.Strategy,
		Budget:   req.Budget,
		Target:   req.TargetRaw,
	}
	if ok, diags := c.Valid(); !ok {
		res.Status = "invalid-input"
		res.Diagnostics = diags
		return res
	}
	budget := req.Budget
	if budget <= 0 {
		budget = 256
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 64
	}

	startCanon := c.Canonical(c.Initial)
	startID := StateID(startCanon)
	depth := map[string]int{startID: 0}
	canonByID := map[string]string{startID: startCanon}
	parents := map[string][]predEdge{} // shortest-distance predecessors
	queue := []string{startID}
	expanded := map[string]bool{}
	used := 1

	emitEnablement := func(sid, canon string, m Marking) {
		for _, t := range c.TransOrder {
			ok, reasons := c.Enabled(t, m)
			verdict := "enabled"
			basis := map[string]any{
				"inputs":     inputBasis(c, t),
				"inhibitors": inhibitorBasis(c, t),
				"capacities": capacityBasis(c, t, m),
				"marking":    canon,
			}
			if !ok {
				verdict = "disabled"
				basis["violations"] = reasons
			}
			res.RuleEvents = append(res.RuleEvents, RuleEvent{
				ID: fmt.Sprintf("re-%s-%s", sid, sha8(t)), Kind: "enablement",
				Transition: t, StateID: sid, Marking: canon,
				Verdict: verdict, Basis: basis,
			})
		}
	}
	emitEnablement(startID, startCanon, c.Initial)

	targetCanon := ""
	if req.Target != nil {
		targetCanon = c.Canonical(req.Target)
	}
	found := targetCanon == startCanon

	for len(queue) > 0 && !found {
		var cur string
		if req.Strategy == "dfs" {
			cur = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			cur = queue[0]
			queue = queue[1:]
		}
		if expanded[cur] || depth[cur] >= maxDepth {
			if depth[cur] >= maxDepth {
				m := c.parseCanonical(canonByID[cur])
				for _, sc := range c.Successors(m) {
					nc := c.Canonical(sc.Marking)
					if _, seen := depth[StateID(nc)]; !seen {
						res.Frontier = append(res.Frontier, FrontierItem{
							StateID: StateID(nc), Marking: nc, Depth: depth[cur] + 1,
							FromState: cur, Transition: sc.Transition, Reason: "depth-limit",
						})
					}
				}
			}
			continue
		}
		expanded[cur] = true
		m := c.parseCanonical(canonByID[cur])
		for _, sc := range c.Successors(m) {
			nc := c.Canonical(sc.Marking)
			nid := StateID(nc)
			if _, seen := depth[nid]; seen {
				if depth[cur]+1 == depth[nid] {
					parents[nid] = append(parents[nid], predEdge{From: cur, Transition: sc.Transition})
				}
				res.RuleEvents = append(res.RuleEvents, RuleEvent{
					ID: fmt.Sprintf("rc-%s-%s", nid, sha8(cur)), Kind: "canonicalization",
					StateID: nid, Marking: nc, Verdict: "merged-identical-tokens",
					Basis: map[string]any{"canonical": nc, "existing_state": nid},
				})
				continue
			}
			if used >= budget {
				res.Frontier = append(res.Frontier, FrontierItem{
					StateID: nid, Marking: nc, Depth: depth[cur] + 1,
					FromState: cur, Transition: sc.Transition, Reason: "budget-exhausted",
				})
				continue
			}
			used++
			depth[nid] = depth[cur] + 1
			canonByID[nid] = nc
			parents[nid] = []predEdge{{From: cur, Transition: sc.Transition}}
			queue = append(queue, nid)
			res.RuleEvents = append(res.RuleEvents, RuleEvent{
				ID: fmt.Sprintf("rd-%s-%s", nid, sha8(cur)), Kind: "canonicalization",
				StateID: nid, Marking: nc, Verdict: "distinct-tokens-new-state",
				Basis: map[string]any{"canonical": nc},
			})
			emitEnablement(nid, nc, sc.Marking)
			if nc == targetCanon {
				found = true
			}
		}
	}

	ids := make([]string, 0, len(depth))
	for id := range depth {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if depth[ids[i]] != depth[ids[j]] {
			return depth[ids[i]] < depth[ids[j]]
		}
		return canonByID[ids[i]] < canonByID[ids[j]]
	})
	edgeSeen := map[string]bool{}
	for _, id := range ids {
		m := c.parseCanonical(canonByID[id])
		sv := StateView{ID: id, Marking: canonByID[id], Depth: depth[id]}
		sv.Deadlock = len(c.Successors(m)) == 0
		if sv.Deadlock {
			res.Deadlocks = append(res.Deadlocks, sv)
		}
		res.States = append(res.States, sv)
		for _, ps := range parents[id] {
			key := ps.From + ">" + id + ">" + ps.Transition
			if edgeSeen[key] {
				continue
			}
			edgeSeen[key] = true
			res.Edges = append(res.Edges, EdgeView{From: ps.From, To: id, Transition: ps.Transition})
		}
	}

	sort.Slice(res.Frontier, func(i, j int) bool {
		if res.Frontier[i].Depth != res.Frontier[j].Depth {
			return res.Frontier[i].Depth < res.Frontier[j].Depth
		}
		return res.Frontier[i].StateID < res.Frontier[j].StateID
	})

	// Cover relation over the reached finite set, component-wise.
	for _, a := range ids {
		for _, b := range ids {
			if a == b {
				continue
			}
			ma := c.parseCanonical(canonByID[a])
			mb := c.parseCanonical(canonByID[b])
			greater := false
			covers := true
			for _, p := range c.PlaceOrder {
				if ma[p] < mb[p] {
					covers = false
					break
				}
				if ma[p] > mb[p] {
					greater = true
				}
			}
			if covers && greater {
				res.Covers = append(res.Covers, CoverPair{Covering: a, Covered: b})
			}
		}
	}

	if req.Target != nil {
		tid := StateID(targetCanon)
		if found {
			b := req.TargetRaw
			res.Reachable = ptrBool(true)
			res.Status = "reachable"
			paths := enumeratePaths(parents, tid, startID, maxCandidates)
			for i, p := range paths {
				res.Candidates = append(res.Candidates, Candidate{
					ID: fmt.Sprintf("c-%s-%d", tid, i+1), Kind: "reachability-path",
					StateID: tid, Marking: b, Path: p, Depth: depth[tid],
					Note: plural(len(paths), "shortest firing sequence"),
				})
			}
		} else if len(res.Frontier) > 0 {
			res.Status = "unknown"
			res.BudgetExhausted = true
			res.RuleEvents = append(res.RuleEvents, RuleEvent{
				ID: "r-budget-" + res.RunID, Kind: "budget",
				Verdict: "unknown", Marking: targetCanon,
				Basis: map[string]any{
					"budget": budget, "states_reached": len(depth),
					"frontier_size": len(res.Frontier), "frontier_retained": true,
				},
			})
		} else {
			res.Reachable = ptrBool(false)
			res.Status = "not-reachable"
		}
	} else {
		res.Status = "explored"
	}
	for i, dl := range res.Deadlocks {
		res.Candidates = append(res.Candidates, Candidate{
			ID: fmt.Sprintf("cd-%s-%d", dl.ID, i+1), Kind: "deadlock",
			StateID: dl.ID, Marking: dl.Marking, Depth: dl.Depth,
			Note: "no enabled transition (inputs, inhibitors, capacity)",
		})
	}

	res.Promotions, res.Unbounded = c.karpMiller(budget)
	return res
}

type predEdge struct {
	From       string
	Transition string
}

func enumeratePaths(parents map[string][]predEdge, target, start string, limit int) [][]string {
	var out [][]string
	var walk func(string, []string)
	walk = func(id string, acc []string) {
		if len(out) >= limit {
			return
		}
		if id == start {
			p := append([]string{}, acc...)
			for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
				p[i], p[j] = p[j], p[i]
			}
			out = append(out, p)
			return
		}
		preds := append([]predEdge{}, parents[id]...)
		sort.Slice(preds, func(i, j int) bool {
			if preds[i].From != preds[j].From {
				return preds[i].From < preds[j].From
			}
			return preds[i].Transition < preds[j].Transition
		})
		for _, pe := range preds {
			walk(pe.From, append(acc, pe.Transition))
		}
	}
	walk(target, nil)
	return out
}

func ptrBool(b bool) *bool { return &b }

func plural(n int, s string) string {
	if n <= 1 {
		return "one of 1 " + s
	}
	return fmt.Sprintf("one of %d distinct %ss", n, s)
}

func inputBasis(c *Compiled, t string) []map[string]any {
	var out []map[string]any
	for _, a := range c.InputArc[t] {
		if a.Inhibitor {
			continue
		}
		out = append(out, map[string]any{"arc": a.ID, "place": a.From, "required": a.Weight})
	}
	return out
}

func inhibitorBasis(c *Compiled, t string) []map[string]any {
	var out []map[string]any
	for _, a := range c.InputArc[t] {
		if a.Inhibitor {
			out = append(out, map[string]any{"arc": a.ID, "place": a.From, "threshold": a.Weight})
		}
	}
	return out
}

func capacityBasis(c *Compiled, t string, m Marking) []map[string]any {
	seen := map[string]bool{}
	var out []map[string]any
	for _, a := range append(append([]Arc{}, c.OutputArc[t]...), c.InputArc[t]...) {
		p := a.To
		if !a.Inhibitor && a.From != "" {
			if _, isP := c.Places[a.From]; isP {
				p = a.From
			}
		}
		if a.Inhibitor || seen[p] {
			continue
		}
		seen[p] = true
		pi := c.Places[p]
		entry := map[string]any{"place": p, "tokens": m[p]}
		if pi.Capacity != nil {
			entry["capacity"] = *pi.Capacity
		} else {
			entry["capacity"] = "∞"
		}
		out = append(out, entry)
	}
	return out
}
