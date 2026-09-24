package petri

import "fmt"

// Enablement records why a transition is (not) enabled in a state. The
// three rule inputs are evaluated together: input arcs, inhibitor arcs and
// post-fire capacity limits.
type Enablement struct {
	StateID      string `json:"state_id"`
	TransitionID string `json:"transition_id"`
	InputsOK     bool   `json:"inputs_ok"`
	InhibitorOK  bool   `json:"inhibitor_ok"`
	CapacityOK   bool   `json:"capacity_ok"`
	Enabled      bool   `json:"enabled"`
	Detail       string `json:"detail,omitempty"`
}

// State is one reached marking in the state space.
type State struct {
	ID      string         `json:"id"`
	Key     string         `json:"key"`
	Marking map[string]int `json:"marking"`
	Depth   int            `json:"depth"`
	Parent  string         `json:"parent,omitempty"`
	Via     string         `json:"via,omitempty"`
}

// Edge is a fired transition between two states.
type Edge struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Transition string `json:"transition"`
}

// Coverage records that a state strictly covers an ancestor on its path,
// causing ω-acceleration on the listed places.
type Coverage struct {
	StateID     string   `json:"state_id"`
	AncestorID  string   `json:"ancestor_id"`
	OmegaPlaces []string `json:"omega_places"`
}

// DeadlockInfo explains a deadlock: every transition and why it is disabled.
type DeadlockInfo struct {
	StateID string       `json:"state_id"`
	Reasons []Enablement `json:"reasons"`
}

// CanonicalEntry is the machine-readable canonicalization log: one entry per
// computed marking key. Token-distinct markings are never merged.
type CanonicalEntry struct {
	Key     string `json:"key"`
	StateID string `json:"state_id"`
	Action  string `json:"action"` // "created" or "deduped" (identical marking only)
}

// TargetResult reports reachability of a target marking. All witness states
// are kept when the answer is not uniquely decidable.
type TargetResult struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"` // "reachable" | "unreachable" | "unknown" | "invalid"
	Witnesses []string `json:"witnesses,omitempty"`
}

// Event is a machine-readable judgment record produced while exploring.
type Event struct {
	Kind     string   `json:"kind"`
	Detail   string   `json:"detail,omitempty"`
	State    string   `json:"state,omitempty"`
	Count    int      `json:"count,omitempty"`
	Frontier []string `json:"frontier,omitempty"`
}

// Result is the outcome of one exploration run.
type Result struct {
	Status        string           `json:"status"` // "complete" | "unknown"
	Budget        int              `json:"budget"`
	Strategy      Strategy         `json:"strategy"`
	States        []State          `json:"states"`
	Edges         []Edge           `json:"edges"`
	Coverages     []Coverage       `json:"coverages,omitempty"`
	Deadlocks     []DeadlockInfo   `json:"deadlocks,omitempty"`
	Frontier      []string         `json:"frontier,omitempty"` // canonical keys, truncated
	EnablementLog []Enablement     `json:"enablement_log"`
	CanonicalLog  []CanonicalEntry `json:"canonical_log"`
	Targets       []TargetResult   `json:"targets,omitempty"`
	Events        []Event          `json:"events"`
}

func geq(a, b int) bool {
	if a == Omega {
		return true
	}
	if b == Omega {
		return a == Omega
	}
	return a >= b
}

func copyMarking(mk map[string]int) map[string]int {
	out := make(map[string]int, len(mk))
	for k, v := range mk {
		out[k] = v
	}
	return out
}

func equalMarking(a, b map[string]int, places []Place) bool {
	for _, p := range places {
		if a[p.ID] != b[p.ID] {
			return false
		}
	}
	return true
}

// covers reports whether a <= b componentwise and a != b (strict cover).
func strictlyCovers(ancestor, next map[string]int, places []Place) bool {
	strict := false
	for _, p := range places {
		av, bv := ancestor[p.ID], next[p.ID]
		if !geq(bv, av) {
			return false
		}
		if av != Omega && bv != av {
			strict = true
		}
	}
	return strict
}

// enablement evaluates one transition in one marking under the combined
// input + inhibitor + capacity rule.
func (m *Model) enablement(stateID string, mk map[string]int, tID string) Enablement {
	en := Enablement{StateID: stateID, TransitionID: tID, InputsOK: true, InhibitorOK: true, CapacityOK: true}
	post := copyMarking(mk)
	for _, a := range m.Arcs {
		if !a.WeightOK {
			continue // unconvertible weight: kept in place, excluded from semantics
		}
		if a.To == tID && isPlace(m, a.From) {
			if a.Inhibitor {
				if geq(mk[a.From], a.Weight) {
					en.InhibitorOK = false
					en.Detail = fmt.Sprintf("inhibitor arc %s active: %s has %s tokens (>= %d)", a.ID, a.From, tok(mk[a.From]), a.Weight)
				}
			} else {
				if !geq(mk[a.From], a.Weight) {
					en.InputsOK = false
					en.Detail = fmt.Sprintf("input arc %s: %s has %s tokens (< %d)", a.ID, a.From, tok(mk[a.From]), a.Weight)
				}
			}
		}
	}
	// Fire hypothetically to check capacities.
	if en.InputsOK {
		for _, a := range m.Arcs {
			if !a.WeightOK {
				continue
			}
			if a.To == tID && isPlace(m, a.From) && !a.Inhibitor {
				if post[a.From] != Omega {
					post[a.From] -= a.Weight
				}
			}
			if a.From == tID && isPlace(m, a.To) {
				if post[a.To] != Omega {
					post[a.To] += a.Weight
				}
			}
		}
		for _, p := range m.Places {
			if p.Capacity == Unbounded {
				continue
			}
			if post[p.ID] == Omega || post[p.ID] > p.Capacity {
				en.CapacityOK = false
				en.Detail = fmt.Sprintf("capacity of %s (%d) would be exceeded", p.ID, p.Capacity)
			}
		}
	}
	en.Enabled = en.InputsOK && en.InhibitorOK && en.CapacityOK
	return en
}

func tok(v int) string {
	if v == Omega {
		return "ω"
	}
	return fmt.Sprintf("%d", v)
}

func isPlace(m *Model, id string) bool {
	return m.PlaceByID(id) != nil
}

// fire applies a transition to a marking (assumes inputs are satisfied).
func (m *Model) fire(mk map[string]int, tID string) map[string]int {
	post := copyMarking(mk)
	for _, a := range m.Arcs {
		if !a.WeightOK {
			continue
		}
		if a.To == tID && isPlace(m, a.From) && !a.Inhibitor {
			if post[a.From] != Omega {
				post[a.From] -= a.Weight
			}
		}
		if a.From == tID && isPlace(m, a.To) {
			if post[a.To] != Omega {
				post[a.To] += a.Weight
			}
		}
	}
	return post
}

// Explore runs a deterministic BFS exploration with coverability
// acceleration and a hard state budget. Exhausting the budget yields status
// "unknown" and preserves the frontier.
func Explore(m *Model, spec ExplorationSpec) *Result {
	if spec.Strategy.Order == "" {
		spec.Strategy.Order = "bfs"
	}
	if spec.Strategy.TieBreak == "" {
		spec.Strategy.TieBreak = "transition-id"
	}
	res := &Result{Status: "complete", Budget: spec.Budget, Strategy: spec.Strategy}
	res.Events = append(res.Events, Event{Kind: "exploration_started", Detail: fmt.Sprintf("strategy=%s/%s budget=%d", spec.Strategy.Order, spec.Strategy.TieBreak, spec.Budget)})

	states := map[string]*State{}
	byID := map[string]*State{}
	var queue []*State
	stateN, edgeN := 0, 0

	addState := func(mk map[string]int, parent *State, via string) *State {
		stateN++
		st := &State{ID: fmt.Sprintf("st:%d", stateN), Marking: mk, Via: via}
		if parent != nil {
			st.Parent = parent.ID
			st.Depth = parent.Depth + 1
		}
		st.Key = m.CanonicalKey(mk)
		states[st.Key] = st
		byID[st.ID] = st
		res.States = append(res.States, *st)
		res.CanonicalLog = append(res.CanonicalLog, CanonicalEntry{Key: st.Key, StateID: st.ID, Action: "created"})
		queue = append(queue, st)
		return st
	}

	root := addState(copyMarking(m.Initial), nil, "")
	truncated := false

loop:
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		enabledAny := false
		var reasons []Enablement
		for _, tr := range m.Transitions {
			en := m.enablement(cur.ID, cur.Marking, tr.ID)
			res.EnablementLog = append(res.EnablementLog, en)
			if !en.Enabled {
				reasons = append(reasons, en)
				continue
			}
			enabledAny = true
			next := m.fire(cur.Marking, tr.ID)
			// Coverability: accelerate against strictly covered ancestors.
			for anc := cur; anc != nil; {
				if strictlyCovers(anc.Marking, next, m.Places) {
					var omegaPlaces []string
					for _, p := range m.Places {
						if anc.Marking[p.ID] != Omega && next[p.ID] != anc.Marking[p.ID] {
							next[p.ID] = Omega
							omegaPlaces = append(omegaPlaces, p.ID)
						}
					}
					res.Coverages = append(res.Coverages, Coverage{StateID: "pending", AncestorID: anc.ID, OmegaPlaces: omegaPlaces})
					res.Events = append(res.Events, Event{Kind: "omega_acceleration", State: anc.ID, Detail: fmt.Sprintf("places %v accelerated to ω", omegaPlaces)})
					break
				}
				if anc.Parent == "" {
					break
				}
				anc = byID[anc.Parent]
			}
			key := m.CanonicalKey(next)
			edgeN++
			edge := Edge{ID: fmt.Sprintf("e:%d", edgeN), From: cur.ID, Transition: tr.ID}
			if existing, ok := states[key]; ok {
				edge.To = existing.ID
				res.Edges = append(res.Edges, edge)
				res.CanonicalLog = append(res.CanonicalLog, CanonicalEntry{Key: key, StateID: existing.ID, Action: "deduped"})
				continue
			}
			if spec.Budget > 0 && len(res.States) >= spec.Budget {
				// Budget exhausted: return unknown, keep the frontier.
				truncated = true
				res.Frontier = append(res.Frontier, key)
				for _, q := range queue {
					res.Frontier = append(res.Frontier, q.Key)
				}
				res.Events = append(res.Events, Event{Kind: "budget_exhausted", Count: spec.Budget, Frontier: res.Frontier})
				break loop
			}
			st := addState(next, cur, tr.ID)
			edge.To = st.ID
			res.Edges = append(res.Edges, edge)
			if len(res.Coverages) > 0 && res.Coverages[len(res.Coverages)-1].StateID == "pending" {
				res.Coverages[len(res.Coverages)-1].StateID = st.ID
			}
		}
		if !enabledAny {
			res.Deadlocks = append(res.Deadlocks, DeadlockInfo{StateID: cur.ID, Reasons: reasons})
			res.Events = append(res.Events, Event{Kind: "deadlock", State: cur.ID})
		}
	}

	if truncated {
		res.Status = "unknown"
	}

	// Targets: keep every witness when reachable.
	for _, t := range m.Targets {
		tr := TargetResult{Name: t.Name}
		if !t.Valid {
			tr.Status = "invalid"
			res.Targets = append(res.Targets, tr)
			continue
		}
		for i := range res.States {
			st := &res.States[i]
			if equalMarking(st.Marking, t.Marking, m.Places) {
				tr.Witnesses = append(tr.Witnesses, st.ID)
			}
		}
		switch {
		case len(tr.Witnesses) > 0:
			tr.Status = "reachable"
		case res.Status == "complete":
			tr.Status = "unreachable"
		default:
			tr.Status = "unknown"
		}
		res.Targets = append(res.Targets, tr)
	}

	// Machine-readable judgment summary.
	disInput, disInhib, disCap := 0, 0, 0
	for _, en := range res.EnablementLog {
		switch {
		case !en.InputsOK:
			disInput++
		case !en.InhibitorOK:
			disInhib++
		case !en.CapacityOK:
			disCap++
		}
	}
	res.Events = append(res.Events,
		Event{Kind: "enablement_checked", Count: len(res.EnablementLog), Detail: fmt.Sprintf("disabled_by_input=%d disabled_by_inhibitor=%d disabled_by_capacity=%d", disInput, disInhib, disCap)},
		Event{Kind: "canonicalization", Count: len(res.States), Detail: "token-distinct markings never merged; dedup only on identical keys"},
		Event{Kind: "exploration_finished", Detail: "status=" + res.Status},
	)
	return res
}

// Path replays the firing sequence from the initial state to stateID.
func (r *Result) Path(stateID string) []Edge {
	byID := map[string]State{}
	for _, s := range r.States {
		byID[s.ID] = s
	}
	edgeTo := map[string]Edge{}
	for _, e := range r.Edges {
		if _, ok := edgeTo[e.To]; !ok {
			edgeTo[e.To] = e
		}
	}
	var rev []Edge
	cur, ok := byID[stateID]
	if !ok {
		return nil
	}
	for cur.Parent != "" {
		e, ok := edgeTo[cur.ID]
		if !ok {
			break
		}
		rev = append(rev, e)
		cur = byID[cur.Parent]
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
