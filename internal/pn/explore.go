package pn

import (
	"fmt"
	"sort"
)

// Strategy is the state-space exploration strategy.
type Strategy string

const (
	StrategyBFS Strategy = "bfs"
)

// Verdict of an exploration.
type Verdict string

const (
	VerdictReachable   Verdict = "reachable"   // goal marking proved reachable
	VerdictUnreachable Verdict = "unreachable" // finite space exhausted, goal absent
	VerdictUnknown     Verdict = "unknown"     // budget exhausted; frontier retained
)

// Config versions the exploration inputs: strategy + truncation budget.
type Config struct {
	Strategy Strategy `json:"strategy"`
	// Budget is the maximum number of transition firings consumed.
	Budget int64 `json:"budget"`
	// MaxCandidates bounds the preserved alternative firing sequences.
	MaxCandidates int `json:"max_candidates"`
}

// Event is a machine-readable judgment event emitted during exploration.
// Every value carries a stable identity in one of the id fields.
type Event struct {
	Seq    int               `json:"seq"`
	At     int64             `json:"at_firing"`
	Type   string            `json:"type"`
	State  string            `json:"state_id,omitempty"`
	Next   string            `json:"next_state_id,omitempty"`
	Trans  string            `json:"transition_id,omitempty"`
	Detail string            `json:"detail,omitempty"`
	Data   map[string]string `json:"data,omitempty"`
}

const (
	EvEnablementCheck = "enablement_checked" // inputs + inhibitor + capacity
	EvFiring          = "firing"
	EvSelfLoop        = "self_loop"
	EvTargetHit       = "target_hit"
	EvDeadlock        = "deadlock"
	EvBudgetExhausted = "budget_exhausted"
	EvSpaceExhausted  = "space_exhausted"
	EvTruncated       = "truncated"
)

// Edge is a firing edge in the explored reach-space.
type Edge struct {
	ID         string `json:"id"`
	From       string `json:"from_state_id"`
	To         string `json:"to_state_id"`
	Transition string `json:"transition_id"`
	Kind       string `json:"kind"` // normal | self_loop
}

// State is an explored marking with stable identity.
type State struct {
	ID      string  `json:"id"`
	Key     string  `json:"canonical_key"`
	Marking Marking `json:"marking"`
	Depth   int     `json:"depth"`
}

// Path is one legal candidate firing sequence.
type Path struct {
	ID          string   `json:"id"`
	Transitions []string `json:"transition_ids"`
	EndState    string   `json:"end_state_id"`
	ReachesGoal bool     `json:"reaches_goal"`
	// Deps are stable subjects the conclusion depends on (node/arc/
	// capacity/param ids), used for scoped invalidation.
	Deps []string `json:"depends_on"`
}

// FrontierItem is a retained unexplored (state, enabled-transition) pair.
type FrontierItem struct {
	State      string `json:"state_id"`
	Transition string `json:"transition_id"`
	Depth      int    `json:"depth"`
	Reason     string `json:"reason"` // "budget_exhausted"
}

// Deadlock is a terminal state with every transition's disable reasons.
type Deadlock struct {
	State   string          `json:"state_id"`
	Marking Marking         `json:"marking"`
	Depth   int             `json:"depth"`
	Reasons []DisableReason `json:"reasons"`
}

// Result is the full exploration report.
type Result struct {
	Strategy Strategy `json:"strategy"`
	Budget   int64    `json:"budget"`
	Firings  int64    `json:"firings_used"`
	StartKey string   `json:"start_key"`
	GoalKey  string   `json:"goal_key"`

	Verdict   Verdict         `json:"verdict"`
	States    []State         `json:"states"`
	Edges     []Edge          `json:"edges"`
	Paths     []Path          `json:"candidate_paths"`
	Deadlocks []Deadlock      `json:"deadlocks"`
	Frontier  []FrontierItem  `json:"frontier"`
	Events    []Event         `json:"judgment_events"`

	stateByKey map[string]string
}

// Explore runs bounded BFS over the reach-space. Enablement always
// considers inputs, inhibitor arcs and capacities together. Markings
// differing in any token count are never merged. On budget exhaustion the
// verdict is "unknown" and the unexplored frontier is retained.
func Explore(net *Net, start, goal Marking, cfg Config) (*Result, error) {
	if err := net.MarkingValid(start); err != nil {
		return nil, fmt.Errorf("start marking: %w", err)
	}
	if err := net.MarkingValid(goal); err != nil {
		return nil, fmt.Errorf("goal marking: %w", err)
	}
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyBFS
	}
	if cfg.Strategy != StrategyBFS {
		return nil, fmt.Errorf("unsupported strategy %q", cfg.Strategy)
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = 4
	}

	r := &Result{
		Strategy:   cfg.Strategy,
		Budget:     cfg.Budget,
		StartKey:   net.MarkingKey(start),
		GoalKey:    net.MarkingKey(goal),
		stateByKey: map[string]string{},
	}
	addState := func(m Marking, depth int) string {
		key := net.MarkingKey(m)
		if id, ok := r.stateByKey[key]; ok {
			return id
		}
		id := fmt.Sprintf("s%05d", len(r.States)+1)
		r.States = append(r.States, State{ID: id, Key: key, Marking: CloneMarking(m), Depth: depth})
		r.stateByKey[key] = id
		return id
	}
	startID := addState(start, 0)

	emit := func(ev Event) {
		ev.Seq = len(r.Events) + 1
		ev.At = r.Firings
		r.Events = append(r.Events, ev)
	}

	type queueItem struct {
		key   string
		depth int
	}
	queue := []queueItem{{r.StartKey, 0}}
	keyMark := map[string]Marking{r.StartKey: CloneMarking(start)}

	truncated := false
	goalFound := r.StartKey == r.GoalKey
	if goalFound {
		emit(Event{Type: EvTargetHit, State: startID, Detail: "start marking equals goal marking"})
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		m := keyMark[item.key]
		stateID := r.stateByKey[item.key]

		enabled := net.EnabledTransitions(m)
		for _, t := range net.Transitions {
			rs := net.DisableReasons(t.ID, m)
			if len(rs) == 0 {
				emit(Event{Type: EvEnablementCheck, State: stateID, Trans: t.ID,
					Data:   map[string]string{"enabled": "true", "rules": "input,inhibitor,capacity"},
					Detail: "enabled: inputs satisfied, inhibitor arcs open, capacities fit"})
				continue
			}
			codes := make([]string, 0, len(rs))
			for _, x := range rs {
				codes = append(codes, string(x.Code))
			}
			sort.Strings(codes)
			emit(Event{Type: EvEnablementCheck, State: stateID, Trans: t.ID,
				Data:   map[string]string{"enabled": "false", "blocked_by": joinCodes(codes)},
				Detail: fmt.Sprintf("disabled: %v", codes)})
		}

		if len(enabled) == 0 {
			var all []DisableReason
			for _, t := range net.Transitions {
				all = append(all, net.DisableReasons(t.ID, m)...)
			}
			r.Deadlocks = append(r.Deadlocks, Deadlock{State: stateID, Marking: CloneMarking(m), Depth: item.depth, Reasons: all})
			emit(Event{Type: EvDeadlock, State: stateID, Detail: fmt.Sprintf("all %d transitions disabled", len(net.Transitions))})
			continue
		}

		for _, tID := range enabled {
			if r.Firings >= cfg.Budget {
				r.Frontier = append(r.Frontier, FrontierItem{State: stateID, Transition: tID, Depth: item.depth, Reason: "budget_exhausted"})
				truncated = true
				continue
			}
			next, _ := net.Fire(tID, m)
			r.Firings++
			nk := net.MarkingKey(next)
			nextID := addState(next, item.depth+1)
			isSelf := nk == item.key
			if _, ok := keyMark[nk]; !ok {
				keyMark[nk] = CloneMarking(next)
				queue = append(queue, queueItem{nk, item.depth + 1})
			}
			kind := "normal"
			evType := EvFiring
			if isSelf {
				kind = "self_loop"
				evType = EvSelfLoop
			}
			r.Edges = append(r.Edges, Edge{
				ID: fmt.Sprintf("e%05d", len(r.Edges)+1), From: stateID, To: nextID, Transition: tID, Kind: kind,
			})
			emit(Event{Type: evType, State: stateID, Next: nextID, Trans: tID})
			if nk == r.GoalKey {
				goalFound = true
				emit(Event{Type: EvTargetHit, State: nextID, Trans: tID})
			}
		}
	}

	// States left unprocessed when the budget ran out are also part of the
	// retained frontier.
	if truncated {
		for _, item := range queue {
			sid := r.stateByKey[item.key]
			for _, tID := range net.EnabledTransitions(keyMark[item.key]) {
				r.Frontier = append(r.Frontier, FrontierItem{State: sid, Transition: tID, Depth: item.depth, Reason: "budget_exhausted"})
			}
		}
		sort.SliceStable(r.Frontier, func(i, j int) bool {
			if r.Frontier[i].Depth != r.Frontier[j].Depth {
				return r.Frontier[i].Depth < r.Frontier[j].Depth
			}
			if r.Frontier[i].State != r.Frontier[j].State {
				return r.Frontier[i].State < r.Frontier[j].State
			}
			return r.Frontier[i].Transition < r.Frontier[j].Transition
		})
		emit(Event{Type: EvBudgetExhausted, Detail: fmt.Sprintf("budget %d exhausted; %d frontier items retained", cfg.Budget, len(r.Frontier))})
		emit(Event{Type: EvTruncated, Detail: "verdict unknown; reachability undecided, frontier preserved"})
	} else {
		emit(Event{Type: EvSpaceExhausted, Detail: fmt.Sprintf("finite reach-space of %d states fully explored", len(r.States))})
	}

	// Preserve multiple legal candidates (all legal sequences while
	// undecidable), restricted to the explored space.
	r.Paths = enumerateGoalPaths(net, r, cfg.MaxCandidates)

	switch {
	case len(r.Paths) > 0 || goalFound:
		r.Verdict = VerdictReachable
		if len(r.Paths) == 0 {
			r.Paths = enumerateGoalPaths(net, r, cfg.MaxCandidates)
		}
	case truncated:
		r.Verdict = VerdictUnknown
	default:
		r.Verdict = VerdictUnreachable
	}
	return r, nil
}

func joinCodes(codes []string) string {
	out := ""
	for i, c := range codes {
		if i > 0 {
			out += ","
		}
		out += c
	}
	return out
}

// enumerateGoalPaths enumerates simple (no repeated state) legal firing
// sequences from start to goal inside the explored states.
func enumerateGoalPaths(net *Net, r *Result, max int) []Path {
	adj := map[string][]Edge{}
	for _, e := range r.Edges {
		adj[e.From] = append(adj[e.From], e)
	}
	var paths []Path
	var dfs func(cur string, usedState map[string]bool, seq []string)
	dfs = func(cur string, usedState map[string]bool, seq []string) {
		if len(paths) >= max {
			return
		}
		state := stateByID(r, cur)
		if state.Key == r.GoalKey {
			depSet := map[string]bool{"param:budget": true, "param:strategy": true}
			for _, tID := range seq {
				depSet[tID] = true
				for _, a := range net.incoming[tID] {
					depSet[a.ID] = true
				}
				for _, a := range net.outgoing[tID] {
					depSet[a.ID] = true
					depSet["cap:"+a.To] = true
				}
				for _, a := range net.inhibitors[tID] {
					depSet[a.ID] = true
				}
			}
			deps := make([]string, 0, len(depSet))
			for k := range depSet {
				deps = append(deps, k)
			}
			sort.Strings(deps)
			paths = append(paths, Path{
				ID:          fmt.Sprintf("path:%s:%d", cur, len(paths)),
				Transitions: append([]string(nil), seq...),
				EndState:    cur,
				ReachesGoal: true,
				Deps:        deps,
			})
			return
		}
		edges := append([]Edge(nil), adj[cur]...)
		sort.SliceStable(edges, func(i, j int) bool {
			if edges[i].To != edges[j].To {
				return edges[i].To < edges[j].To
			}
			return edges[i].Transition < edges[j].Transition
		})
		for _, e := range edges {
			if usedState[e.To] {
				continue
			}
			usedState[e.To] = true
			dfs(e.To, usedState, append(seq, e.Transition))
			delete(usedState, e.To)
		}
	}
	start := r.stateByKey[r.StartKey]
	dfs(start, map[string]bool{start: true}, nil)
	return paths
}

func stateByID(r *Result, id string) State {
	for _, s := range r.States {
		if s.ID == id {
			return s
		}
	}
	return State{}
}
