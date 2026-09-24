package engine

import (
	"sort"

	"tflocal/internal/domain"
)

// Outcome is the verdict of an exact exploration.
type Outcome string

const (
	OutcomeReached    Outcome = "reached"
	OutcomeUnreached  Outcome = "unreached"
	OutcomeDeadlocked Outcome = "deadlocked"
	OutcomeUnknown    Outcome = "unknown" // budget exhausted: frontier retained
)

// State is one visited concrete state. Seq is the stable discovery order.
type State struct {
	ID         string                 `json:"id"`
	Seq        int                    `json:"seq"`
	Marking    domain.Marking         `json:"marking"`
	Canon      string                 `json:"canon"`
	ParentID   string                 `json:"parent_id,omitempty"`
	ParentFire string                 `json:"parent_fire,omitempty"`
	Depth      int                    `json:"depth"`
	IsGoal     bool                   `json:"is_goal"`
	Deadlock   bool                   `json:"deadlock"`
	Frontier   bool                   `json:"frontier"`
	Enablement map[string]*Enablement `json:"enablement,omitempty"`
}

// Edge is one firing between two concrete states.
type Edge struct {
	FromID     string `json:"from_id"`
	ToID       string `json:"to_id"`
	Transition string `json:"transition"`
}

// Result is the complete machine-readable exploration record.
type Result struct {
	Strategy    string            `json:"strategy"`
	Budget      int               `json:"budget"`
	Fired       int               `json:"steps_used"`
	Truncated   bool              `json:"truncated"`
	Outcome     Outcome           `json:"outcome"`
	States      []*State          `json:"states"`
	Edges       []*Edge           `json:"edges"`
	Frontier    []string          `json:"frontier_ids"`
	GoalPath    []string          `json:"goal_path_transitions,omitempty"`
	GoalStateID string            `json:"goal_state_id,omitempty"`
	DeadlockIDs []string          `json:"deadlock_ids,omitempty"`
	Events      []map[string]any  `json:"events,omitempty"`
}

// ExploreExact performs bounded BFS. The budget counts transition firings.
// When the budget is exhausted the verdict is unknown and every state that
// still has an unexplored enabled successor is kept in the retained frontier.
func ExploreExact(n *domain.Net, params domain.Params, budgetOverride int) *Result {
	budget := params.Budget
	if budgetOverride > 0 {
		budget = budgetOverride
	}
	r := &Result{Strategy: params.Strategy, Budget: budget}
	emit := func(kind string, kv map[string]any) {
		ev := map[string]any{"event": kind}
		for k, v := range kv {
			ev[k] = v
		}
		r.Events = append(r.Events, ev)
	}
	emit("exploration_started", map[string]any{"strategy": params.Strategy, "budget": budget})

	states := map[string]*State{}
	start := &State{Marking: n.Init.Clone(), Depth: 0}
	start.Canon = start.Marking.Canonical()
	start.ID = "S" + start.Canon
	if n.Goal != nil && start.Marking.Equal(*n.Goal) {
		start.IsGoal = true
	}
	states[start.Canon] = start
	order := []*State{start}
	queue := []*State{start}
	goal := n.Goal
	tids := sortedTransitions(n)
	frontierSet := map[string]bool{}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cur.Enablement = map[string]*Enablement{}
		var enabled []string
		for _, t := range tids {
			en := Enable(n, t, cur.Marking, params.InhibitorsEnabled)
			en2 := en
			cur.Enablement[t] = &en2
			if en.Enabled {
				enabled = append(enabled, t)
			}
		}
		emit("enablement_evaluated", map[string]any{
			"state_id": cur.ID,
			"rule":    "input_and_inhibitor_and_capacity",
			"checks":  enablementSummary(cur.Enablement),
		})
		if len(enabled) == 0 {
			cur.Deadlock = true
			r.DeadlockIDs = append(r.DeadlockIDs, cur.ID)
			emit("deadlock_candidate", map[string]any{"state_id": cur.ID, "reasons": deadlockReasons(cur.Enablement)})
			continue
		}
		for _, t := range enabled {
			if r.Fired >= budget {
				if !frontierSet[cur.ID] {
					frontierSet[cur.ID] = true
					cur.Frontier = true
					r.Frontier = append(r.Frontier, cur.ID)
				}
				emit("frontier_retained", map[string]any{"state_id": cur.ID, "blocked_fire": t, "reason": "budget_exhausted"})
				continue
			}
			r.Fired++
			nm := Fire(n, t, cur.Marking)
			can := nm.Canonical()
			ns, seen := states[can]
			if !seen {
				ns = &State{Marking: nm, Canon: can, ID: "S" + can, ParentID: cur.ID,
					ParentFire: t, Depth: cur.Depth + 1}
				if goal != nil && nm.Equal(*goal) {
					ns.IsGoal = true
				}
				states[can] = ns
				order = append(order, ns)
				queue = append(queue, ns)
				emit("state_discovered", map[string]any{"state_id": ns.ID, "parent_id": cur.ID, "fire": t, "depth": ns.Depth})
			}
			r.Edges = append(r.Edges, &Edge{FromID: cur.ID, ToID: ns.ID, Transition: t})
			if ns.IsGoal && r.GoalStateID == "" {
				r.GoalStateID = ns.ID
				r.GoalPath = pathTo(states, ns)
				emit("goal_reached", map[string]any{"state_id": ns.ID, "path": r.GoalPath})
			}
		}
	}
	sort.Strings(r.Frontier)
	sort.Strings(r.DeadlockIDs)
	// seq follows discovery (insertion) order, which is deterministic
	for i, s := range order {
		s.Seq = i
	}
	r.States = order
	r.Truncated = len(r.Frontier) > 0
	switch {
	case r.GoalStateID != "":
		r.Outcome = OutcomeReached
	case r.Truncated:
		r.Outcome = OutcomeUnknown
		emit("budget_exhausted", map[string]any{"steps_used": r.Fired, "retained_frontier": r.Frontier, "verdict": "unknown"})
	case len(r.DeadlockIDs) > 0:
		r.Outcome = OutcomeDeadlocked
	default:
		r.Outcome = OutcomeUnreached
	}
	emit("exploration_finished", map[string]any{"outcome": string(r.Outcome), "states": len(order), "edges": len(r.Edges), "truncated": r.Truncated})
	return r
}

func pathTo(states map[string]*State, target *State) []string {
	byID := make(map[string]*State, len(states))
	for _, s := range states {
		byID[s.ID] = s
	}
	var path []string
	cur := target
	for cur.ParentID != "" {
		path = append([]string{cur.ParentFire}, path...)
		cur = byID[cur.ParentID]
	}
	return path
}

func enablementSummary(es map[string]*Enablement) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	ids := make([]string, 0, len(es))
	for id := range es {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := es[id]
		out[id] = map[string]bool{"input": e.InputOK, "inhibitor": e.InhibitorOK, "capacity": e.CapacityOK, "enabled": e.Enabled}
	}
	return out
}

func deadlockReasons(es map[string]*Enablement) map[string][]string {
	out := map[string][]string{}
	ids := make([]string, 0, len(es))
	for id := range es {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out[id] = es[id].Details
	}
	return out
}
