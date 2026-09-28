package petrinet

import (
	"sort"
	"strconv"
)

// Strategy orders successor expansion.
type Strategy string

const (
	StrategyBFS Strategy = "BFS"
	StrategyDFS Strategy = "DFS"
)

// Status is the reachability verdict for the requested target marking.
type Status string

const (
	// StatusReachable means the concrete target marking was reached.
	StatusReachable Status = "REACHABLE"
	// StatusUnreachable means the reachable space was fully explored without
	// hitting the concrete target (bounded, or omega regions with no match).
	StatusUnreachable Status = "UNREACHABLE"
	// StatusUnknown means the budget was exhausted before a conclusion. The
	// frontier is preserved so exploration can be resumed or audited.
	StatusUnknown Status = "UNKNOWN"
)

// Omega marks an unbounded place component in a coverability marking.
const Omega int64 = -1

// Config captures the versioned exploration strategy and truncation budget.
type Config struct {
	Strategy Strategy `json:"strategy"`
	// NodeBudget bounds the number of distinct markings expanded. Zero means
	// unlimited (used only when the model is known bounded).
	NodeBudget int `json:"node_budget"`
}

// State is one canonical reachable (or omega-coverability) marking.
type State struct {
	ID       string                   `json:"id"`
	Marking  map[string]int64         `json:"marking"`
	Omega    map[string]bool          `json:"omega,omitempty"`
	Path     []string                 `json:"path_transition_ids"`
	Deadlock bool                     `json:"deadlock"`
	Reasons  map[string][]BlockDetail `json:"deadlock_reasons,omitempty"`
}

// Edge is one observed firing between two canonical states.
type Edge struct {
	ID         string `json:"id"`
	FromState  string `json:"from_state"`
	Transition string `json:"transition_id"`
	ToState    string `json:"to_state"`
}

// FrontierItem is a pending (state, transition) expansion when truncated.
type FrontierItem struct {
	StateID    string `json:"state_id"`
	Transition string `json:"transition_id"`
}

// Result is a deterministic exploration result.
type Result struct {
	NetID           string           `json:"net_id"`
	Strategy        Strategy         `json:"strategy"`
	NodeBudget      int              `json:"node_budget"`
	Status          Status           `json:"status"`
	States          []*State         `json:"states"`
	Edges           []*Edge          `json:"edges"`
	Frontier        []FrontierItem   `json:"frontier"`
	Target          map[string]int64 `json:"target,omitempty"`
	TargetStateID   string           `json:"target_state_id,omitempty"`
	UnboundedPlaces []string         `json:"unbounded_places,omitempty"`
	Expanded        int              `json:"expanded"`
}

// genMark is a mutable marking whose values may be Omega.
type genMark struct {
	tokens map[string]int64
}

func (g genMark) key(places []string) string {
	var b []byte
	for i, p := range places {
		if i > 0 {
			b = append(b, '|')
		}
		b = append(b, p...)
		b = append(b, '=')
		if g.tokens[p] == Omega {
			b = append(b, 'w')
		} else {
			b = append(b, strconv.AppendInt(nil, g.tokens[p], 10)...)
		}
	}
	return string(b)
}

func (g genMark) clone() genMark {
	c := genMark{tokens: map[string]int64{}}
	for k, v := range g.tokens {
		c.tokens[k] = v
	}
	return c
}

func pad(seq int) string {
	s := strconv.Itoa(seq)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// enabledOn applies enablement to a marking that may contain omega:
// an omega input place always satisfies consumption, an omega inhibitor place
// always blocks, and output into an omega place stays unbounded.
func (n *Net) enabledOn(t string, g genMark, places []string) (bool, []BlockDetail) {
	var blocks []BlockDetail
	for _, in := range n.Inputs(t) {
		v := g.tokens[in.Place]
		if v != Omega && v < in.Weight {
			blocks = append(blocks, BlockDetail{ReasonInputShort, in.ArcID, in.Place, in.Weight, v})
		}
	}
	for _, inh := range n.Inhibitors(t) {
		v := g.tokens[inh.Place]
		if v == Omega || v >= inh.Weight {
			actual := v
			if v == Omega {
				actual = -1
			}
			blocks = append(blocks, BlockDetail{ReasonInhibited, inh.ArcID, inh.Place, inh.Weight, actual})
		}
	}
	// Capacity after consume+produce.
	next := g.clone()
	for _, in := range n.Inputs(t) {
		if next.tokens[in.Place] != Omega {
			next.tokens[in.Place] -= in.Weight
		}
	}
	for _, out := range n.Outputs(t) {
		if next.tokens[out.Place] != Omega {
			next.tokens[out.Place] += out.Weight
		}
	}
	for _, out := range n.Outputs(t) {
		if g.tokens[out.Place] == Omega || next.tokens[out.Place] == Omega {
			continue
		}
		cap := n.Capacity[out.Place]
		if cap >= 0 && next.tokens[out.Place] > cap {
			blocks = append(blocks, BlockDetail{ReasonCapacityFull, out.ArcID, out.Place, cap, next.tokens[out.Place]})
		}
	}
	return len(blocks) == 0, blocks
}

func hasOmega(g genMark, places []string) bool {
	for _, p := range places {
		if g.tokens[p] == Omega {
			return true
		}
	}
	return false
}

func concreteEqual(g genMark, target Marking, places []string) bool {
	for _, p := range places {
		if g.tokens[p] != target[p] {
			return false
		}
	}
	return true
}

func copyPath(p []string, t string) []string {
	out := make([]string, 0, len(p)+1)
	out = append(out, p...)
	out = append(out, t)
	return out
}

// Explore runs a budgeted Karp-Miller-style coverability search.
//
// Successors use exact enablement (input + inhibitor + capacity). A successor
// that strictly dominates a strict ancestor marking promotes the dominated
// places to omega, which records unbounded growth. Omega markings never equal
// a concrete target, so coverability cannot fabricate reachability.
func (n *Net) Explore(initial Marking, target Marking, cfg Config) *Result {
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyBFS
	}
	places := append([]string(nil), n.PlaceIDs...)
	trans := append([]string(nil), n.TransitionIDs...)

	res := &Result{NetID: n.ID, Strategy: cfg.Strategy, NodeBudget: cfg.NodeBudget}
	if target != nil {
		res.Target = map[string]int64(target)
	}

	start := genMark{tokens: map[string]int64{}}
	for _, p := range places {
		start.tokens[p] = initial[p]
	}

	states := []*State{}
	stateByKey := map[string]*State{}
	markByKey := map[string]genMark{}
	parentKey := map[string]string{} // first-discovery parent
	edgeSeen := map[string]bool{}
	var edges []*Edge

	ensureState := func(g genMark, path []string) *State {
		k := g.key(places)
		if st, ok := stateByKey[k]; ok {
			return st
		}
		st := &State{ID: "S" + pad(len(states)+1), Marking: map[string]int64{},
			Path: append([]string(nil), path...)}
		for _, p := range places {
			if g.tokens[p] == Omega {
				st.Marking[p] = 0
				if st.Omega == nil {
					st.Omega = map[string]bool{}
				}
				st.Omega[p] = true
			} else {
				st.Marking[p] = g.tokens[p]
			}
		}
		stateByKey[k] = st
		markByKey[k] = g
		states = append(states, st)
		return st
	}

	type job struct {
		g    genMark
		path []string
	}
	queue := []job{{g: start}}
	queued := map[string]bool{start.key(places): true}
	visited := map[string]bool{}

	pop := func() job {
		if cfg.Strategy == StrategyDFS {
			j := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			return j
		}
		j := queue[0]
		queue = queue[1:]
		return j
	}

	expanded := 0
	truncated := false

	for len(queue) > 0 {
		if cfg.NodeBudget > 0 && expanded >= cfg.NodeBudget {
			truncated = true
			break
		}
		j := pop()
		k := j.g.key(places)
		queued[k] = false
		if visited[k] {
			continue
		}
		visited[k] = true
		expanded++

		cur := ensureState(j.g, j.path)

		// Deadlock classification via exact enablement.
		anyEnabled := false
		for _, t := range trans {
			if ok, _ := n.enabledOn(t, j.g, places); ok {
				anyEnabled = true
				break
			}
		}
		if !anyEnabled {
			cur.Deadlock = true
			cur.Reasons = map[string][]BlockDetail{}
			for _, t := range trans {
				if ok, blocks := n.enabledOn(t, j.g, places); !ok {
					cur.Reasons[t] = blocks
				}
			}
		}

		if target != nil && !hasOmega(j.g, places) && concreteEqual(j.g, target, places) {
			res.TargetStateID = cur.ID
		}

		for _, t := range trans {
			if ok, _ := n.enabledOn(t, j.g, places); !ok {
				continue
			}
			ng := j.g.clone()
			for _, in := range n.Inputs(t) {
				if ng.tokens[in.Place] != Omega {
					ng.tokens[in.Place] -= in.Weight
				}
			}
			for _, out := range n.Outputs(t) {
				if ng.tokens[out.Place] != Omega {
					ng.tokens[out.Place] += out.Weight
				}
			}

			// Omega promotion against strict ancestors on the discovery path.
			for anc := k; anc != ""; anc = parentKey[anc] {
				am := markByKey[anc]
				strictlyLess := false
				compatible := true
				for _, p := range places {
					a := am.tokens[p]
					x := ng.tokens[p]
					if a == Omega {
						if x != Omega {
							compatible = false
							break
						}
						continue
					}
					if x == Omega {
						continue
					}
					if x < a {
						compatible = false
						break
					}
					if x > a {
						strictlyLess = true
					}
				}
				if compatible && strictlyLess {
					for _, p := range places {
						if am.tokens[p] != Omega && ng.tokens[p] > am.tokens[p] {
							ng.tokens[p] = Omega
						}
					}
				}
			}

			nk := ng.key(places)
			nst := ensureState(ng, copyPath(j.path, t))
			ek := cur.ID + ">" + t + ">" + nst.ID
			if !edgeSeen[ek] {
				edgeSeen[ek] = true
				edges = append(edges, &Edge{ID: "E" + pad(len(edges)+1),
					FromState: cur.ID, Transition: t, ToState: nst.ID})
			}
			if !visited[nk] && !queued[nk] {
				if _, ok := parentKey[nk]; !ok {
					parentKey[nk] = k
				}
				queued[nk] = true
				queue = append(queue, job{g: ng, path: copyPath(j.path, t)})
			}
		}
	}

	res.States = states
	res.Edges = edges
	res.Expanded = expanded

	if truncated {
		frontSeen := map[string]bool{}
		for _, st := range states {
			k := stateKeyOf(st, places)
			g := markByKey[k]
			for _, t := range trans {
				if ok, _ := n.enabledOn(t, g, places); !ok {
					continue
				}
				sg := successorKey(g, n, t, places)
				if visited[sg] {
					continue
				}
				fk := st.ID + ">" + t
				if frontSeen[fk] {
					continue
				}
				frontSeen[fk] = true
				res.Frontier = append(res.Frontier, FrontierItem{StateID: st.ID, Transition: t})
			}
		}
		sort.Slice(res.Frontier, func(i, j int) bool {
			if res.Frontier[i].StateID != res.Frontier[j].StateID {
				return res.Frontier[i].StateID < res.Frontier[j].StateID
			}
			return res.Frontier[i].Transition < res.Frontier[j].Transition
		})
		res.Status = StatusUnknown
	} else if res.TargetStateID != "" {
		res.Status = StatusReachable
	} else {
		res.Status = StatusUnreachable
	}

	ub := map[string]bool{}
	for _, st := range states {
		for p := range st.Omega {
			ub[p] = true
		}
	}
	for p := range ub {
		res.UnboundedPlaces = append(res.UnboundedPlaces, p)
	}
	sort.Strings(res.UnboundedPlaces)

	return res
}

func stateKeyOf(st *State, places []string) string {
	g := genMark{tokens: map[string]int64{}}
	for _, p := range places {
		if st.Omega != nil && st.Omega[p] {
			g.tokens[p] = Omega
		} else {
			g.tokens[p] = st.Marking[p]
		}
	}
	return g.key(places)
}

func successorKey(g genMark, n *Net, t string, places []string) string {
	ng := g.clone()
	for _, in := range n.Inputs(t) {
		if ng.tokens[in.Place] != Omega {
			ng.tokens[in.Place] -= in.Weight
		}
	}
	for _, out := range n.Outputs(t) {
		if ng.tokens[out.Place] != Omega {
			ng.tokens[out.Place] += out.Weight
		}
	}
	return ng.key(places)
}
