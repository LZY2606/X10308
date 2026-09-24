package petrinet

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// TraceEvent is a machine-readable judgment record emitted during a replay.
// Each verdict cites inputs (stable state/transition/arc identities), the
// rule applied and the affected objects.
type TraceEvent struct {
	Seq          int            `json:"seq"`
	Kind         string         `json:"kind"`
	StateID      string         `json:"state_id,omitempty"`
	Marking      Marking        `json:"marking,omitempty"`
	TransitionID string         `json:"transition_id,omitempty"`
	Enabled      *bool          `json:"enabled,omitempty"`
	Reasons      []EnableReason `json:"reasons,omitempty"`
	NextID       string         `json:"next_state_id,omitempty"`
	Omega        map[string]bool `json:"omega_places,omitempty"`
	Rule         string         `json:"rule,omitempty"`
	Detail       string         `json:"detail,omitempty"`
	Budget       int64          `json:"budget_remaining,omitempty"`
}

type State struct {
	ID      string          `json:"id"`
	Marking Marking         `json:"marking"`
	Omega   map[string]bool `json:"omega,omitempty"`
	Order   int             `json:"order"`
	Root    bool            `json:"root"`
	Dead    bool            `json:"dead"`
	Depth   int             `json:"depth"`
}

type Edge struct {
	ID           string `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	TransitionID string `json:"transition_id"`
}

type Frontier struct {
	StateID string `json:"state_id"`
	Depth   int    `json:"depth"`
	Reason  string `json:"reason"`
}

type Candidate struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"` // target_path | deadlock | cover
	StateID     string   `json:"state_id,omitempty"`
	Path        []string `json:"path_state_ids,omitempty"`
	Transitions []string `json:"path_transitions,omitempty"`
	Detail      string   `json:"detail"`
	Features    []string `json:"features,omitempty"`
}

type Report struct {
	ScenarioID       string       `json:"scenario_id"`
	Status           string       `json:"status"` // complete | unknown_truncated
	Strategy         Strategy     `json:"strategy"`
	Budget           int64        `json:"budget"`
	BudgetUsed       int64        `json:"budget_used"`
	States           []State      `json:"states"`
	Edges            []Edge       `json:"edges"`
	Frontier         []Frontier   `json:"frontier"`
	Candidates       []Candidate  `json:"candidates"`
	Trace            []TraceEvent `json:"trace"`
	ReachedTarget    bool         `json:"reached_target"`
	HasDeadlock      bool         `json:"has_deadlock"`
	Coverability     bool         `json:"coverability"`
	Diagnostics      []ImportDiag `json:"diagnostics,omitempty"`
	ScopeTransitions []string     `json:"scope_transitions,omitempty"`
}

const omegaVal = -7

type exploreOpt struct {
	scenarioID string
	marking    Marking
	targets    map[string]bool
	budget     int64
	cover      bool
	scope      map[string]bool
	features   []string
}

func WithScenario(id string, mk Marking, targets []string, scope []string, budget int64, cover bool, features []string) func(*exploreOpt) {
	return func(o *exploreOpt) {
		o.scenarioID = id
		o.marking = cloneMarking(mk)
		o.targets = toSet(targets)
		o.budget = budget
		o.cover = cover
		if len(scope) > 0 {
			o.scope = toSet(scope)
		}
		o.features = features
	}
}

type xnode struct {
	key    string
	id     string
	marking Marking
	omega   map[string]bool
	depth   int
	parent  string
	via     string
	order   int
	dead    bool
}

// Explore runs deterministic BFS. Exact reachability merges only identical
// token vectors. The coverability variant applies Karp-Miller acceleration.
// On budget exhaustion the verdict is unknown_truncated and the frontier is
// retained for later resumption.
func (m Model) Explore(opts ...func(*exploreOpt)) *Report {
	opt := exploreOpt{budget: m.Budget, marking: cloneMarking(m.InitialMarking), cover: m.Strategy.Coverability}
	if len(m.ScenarioScope) > 0 {
		opt.scope = toSet(m.ScenarioScope)
	}
	for _, o := range opts {
		o(&opt)
	}
	rep := &Report{ScenarioID: opt.scenarioID, Strategy: m.Strategy, Budget: opt.budget, Coverability: opt.cover}
	rep.ScopeTransitions = setList(opt.scope)

	tids := make([]string, 0, len(m.Net.Transitions))
	for _, t := range m.Net.Transitions {
		if m.Net.inScope(t.ID, opt.scope) {
			tids = append(tids, t.ID)
		}
	}

	nodes := map[string]*xnode{}
	orderKeys := []string{}
	queue := []string{}

	root := &xnode{marking: cloneMarking(opt.marking), depth: 0, order: 0}
	root.key = fullKey(m.Net, root.marking, nil)
	root.id = StateID(m.Net, root.marking)
	nodes[root.key] = root
	orderKeys = append(orderKeys, root.key)
	queue = append(queue, root.key)
	rep.Trace = append(rep.Trace, TraceEvent{
		Seq: len(rep.Trace), Kind: "canonicalize", StateID: root.id, Marking: cloneMarking(opt.marking),
		Rule:   "canonical key over full sorted place vector",
		Detail: "different token vectors are never merged; equal vectors share identity",
		Budget: opt.budget,
	})

	var budget int64
	unknown := false

	for len(queue) > 0 {
		curKey := queue[0]
		queue = queue[1:]
		cur := nodes[curKey]

		results := make([]EnableResult, 0, len(tids))
		enabledT := []string{}
		for _, tid := range tids {
			er := m.CheckEnable(tid, cur.marking)
			results = append(results, er)
			rep.Trace = append(rep.Trace, TraceEvent{
				Seq: len(rep.Trace), Kind: "enablement", StateID: cur.id, TransitionID: tid,
				Enabled: &er.Enabled, Reasons: er.Reasons,
				Rule:   "enabled iff inputs held, inhibitor tokens below weight, and capacity satisfied after firing",
				Budget: opt.budget - budget,
			})
			if er.Enabled {
				enabledT = append(enabledT, tid)
			}
		}
		cur.dead = len(enabledT) == 0
		if cur.dead {
			rep.HasDeadlock = true
			rep.Candidates = append(rep.Candidates, Candidate{
				ID: candID("deadlock", cur.id), Kind: "deadlock", StateID: cur.id,
				Path: buildPath(nodes, cur.key), Transitions: buildTransitions(nodes, cur.key),
				Detail: "no in-scope transition satisfies input+inhibitor+capacity; " + deadlockSummary(results),
				Features: append(append([]string{}, opt.features...), "deadlock"),
			})
			rep.Trace = append(rep.Trace, TraceEvent{
				Seq: len(rep.Trace), Kind: "deadlock", StateID: cur.id,
				Rule: "deadlock iff every scoped transition fails at least one of input/inhibitor/capacity",
			})
		}
		if opt.targets[cur.id] {
			rep.ReachedTarget = true
			rep.Candidates = append(rep.Candidates, Candidate{
				ID: candID("target", cur.id), Kind: "target_path", StateID: cur.id,
				Path: buildPath(nodes, cur.key), Transitions: buildTransitions(nodes, cur.key),
				Detail: "target marking reached by exact token-vector equality",
				Features: append(append([]string{}, opt.features...), "target"),
			})
		}

		if budget >= opt.budget {
			unknown = true
			rep.Frontier = append(rep.Frontier, Frontier{StateID: cur.id, Depth: cur.depth, Reason: "unexpanded: expansion budget exhausted"})
			rep.Trace = append(rep.Trace, TraceEvent{
				Seq: len(rep.Trace), Kind: "budget_exhausted", StateID: cur.id,
				Rule: "budget exhausted => unknown; frontier retained", Budget: 0,
			})
			continue
		}

		for i, tid := range tids {
			if !results[i].Enabled {
				continue
			}
			budget++
			next := m.Fire(tid, cur.marking)
			nextOmega := cloneOmega(cur.omega)
			rule := "fire transition (consume then produce)"
			if opt.cover {
				if anc := growingAncestor(nodes, cur, next); anc != nil {
					for pid := range next {
						if next[pid] > anc.marking[pid] && !isOmega(anc.marking[pid]) {
							next[pid] = omegaVal
							nextOmega[pid] = true
						}
					}
					rule = "Karp-Miller acceleration: strictly larger along path -> omega"
				}
			}
			nk := fullKey(m.Net, next, nextOmega)
			nn, known := nodes[nk]
			if !known {
				nn = &xnode{
					key: nk, id: omegaStateID(m.Net, next, nextOmega), marking: next, omega: nextOmega,
					depth: cur.depth + 1, parent: cur.key, via: tid, order: len(orderKeys),
				}
				nodes[nk] = nn
				orderKeys = append(orderKeys, nk)
				queue = append(queue, nk)
			}
			rep.Edges = append(rep.Edges, Edge{ID: edgeID(cur.id, nn.id, tid), From: cur.id, To: nn.id, TransitionID: tid})
			rep.Trace = append(rep.Trace, TraceEvent{
				Seq: len(rep.Trace), Kind: "fire", StateID: cur.id, TransitionID: tid, NextID: nn.id,
				Marking: next, Omega: nextOmega, Rule: rule, Budget: opt.budget - budget,
			})
			if budget >= opt.budget {
				break
			}
		}
	}

	sort.Slice(orderKeys, func(a, b int) bool { return nodes[orderKeys[a]].order < nodes[orderKeys[b]].order })
	for _, k := range orderKeys {
		n := nodes[k]
		rep.States = append(rep.States, State{
			ID: n.id, Marking: n.marking, Omega: n.omega, Order: n.order,
			Root: n.parent == "", Dead: n.dead, Depth: n.depth,
		})
	}
	if opt.cover {
		rep.Candidates = append(rep.Candidates, coverCandidates(nodes, orderKeys, opt.features)...)
	}
	rep.BudgetUsed = budget
	if unknown {
		rep.Status = "unknown_truncated"
		rep.Trace = append(rep.Trace, TraceEvent{
			Seq: len(rep.Trace), Kind: "verdict_unknown",
			Rule: "budget exhaustion returns unknown and preserves the truncated frontier",
			Detail: "frontier states stay available for a later replay with a larger budget",
		})
	} else {
		rep.Status = "complete"
	}
	return rep
}

func growingAncestor(nodes map[string]*xnode, cur *xnode, next Marking) *xnode {
	k := cur.key
	for k != "" {
		a := nodes[k]
		greater := false
		ok := true
		for pid, v := range next {
			av := a.marking[pid]
			if isOmega(av) {
				continue
			}
			if v < av {
				ok = false
				break
			}
			if v > av {
				greater = true
			}
		}
		if ok && greater {
			return a
		}
		k = a.parent
	}
	return nil
}

func isOmega(v int64) bool { return v == omegaVal }

func coverCandidates(nodes map[string]*xnode, keys []string, features []string) []Candidate {
	var out []Candidate
	for _, k := range keys {
		n := nodes[k]
		if len(n.omega) == 0 {
			continue
		}
		places := make([]string, 0, len(n.omega))
		for p := range n.omega {
			places = append(places, p)
		}
		sort.Strings(places)
		out = append(out, Candidate{
			ID: candID("cover", n.id), Kind: "cover", StateID: n.id,
			Path: buildPath(nodes, n.key), Transitions: buildTransitions(nodes, n.key),
			Detail: "unbounded growth: omega places " + strings.Join(places, ","),
			Features: append(append([]string{}, features...), "unbounded", "cover"),
		})
	}
	return out
}

func buildPath(nodes map[string]*xnode, key string) []string {
	var rev []string
	for key != "" {
		n := nodes[key]
		rev = append(rev, n.id)
		key = n.parent
	}
	out := make([]string, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

func buildTransitions(nodes map[string]*xnode, key string) []string {
	var rev []string
	for key != "" {
		n := nodes[key]
		if n.via == "" {
			break
		}
		rev = append(rev, n.via)
		key = n.parent
	}
	out := make([]string, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

func deadlockSummary(rs []EnableResult) string {
	kinds := map[string]bool{}
	for _, r := range rs {
		for _, why := range r.Reasons {
			kinds[why.Kind] = true
		}
	}
	all := []string{}
	for _, k := range []string{"input", "inhibitor", "capacity"} {
		if kinds[k] {
			all = append(all, k)
		}
	}
	if len(all) == 0 {
		return "empty transition scope"
	}
	return "blocking reasons present: " + strings.Join(all, "+")
}

func fullKey(n Net, mk Marking, om map[string]bool) string {
	key := n.CanonicalKey(mk)
	if len(om) == 0 {
		return key
	}
	ps := make([]string, 0, len(om))
	for p := range om {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return key + "|omega=" + strings.Join(ps, ",")
}

func omegaStateID(n Net, mk Marking, om map[string]bool) string {
	if len(om) == 0 {
		return StateID(n, mk)
	}
	sum := sha256.Sum256([]byte(fullKey(n, mk, om)))
	return "st_" + hex.EncodeToString(sum[:10])
}

func omegaSuffix(om map[string]bool) string {
	if len(om) == 0 {
		return ""
	}
	ps := make([]string, 0, len(om))
	for p := range om {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return "|omega=" + strings.Join(ps, ",")
}

func edgeID(from, to, tid string) string {
	sum := sha256.Sum256([]byte(from + ">" + tid + ">" + to))
	return "ed_" + hex.EncodeToString(sum[:8])
}

func candID(kind, stateID string) string {
	sum := sha256.Sum256([]byte(kind + ":" + stateID))
	return "cd_" + hex.EncodeToString(sum[:8])
}

func cloneMarking(m Marking) Marking {
	out := Marking{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneOmega(m map[string]bool) map[string]bool {
	if len(m) == 0 {
		return nil
	}
	out := map[string]bool{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func toSet(xs []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range xs {
		out[x] = true
	}
	return out
}

func setList(set map[string]bool) []string {
	if set == nil {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
