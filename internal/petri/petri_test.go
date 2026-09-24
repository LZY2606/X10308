package petri

import "testing"

func mkModel(places []Place, transitions []Transition, arcs []Arc) Model {
	return Model{ID: "m", Places: places, Transitions: transitions, Arcs: arcs}
}

func TestSelfLoop(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1", X: 0, Y: 0}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p1", Weight: 1, Kind: ArcOut},
		},
	)
	if !m.Enabled(Marking{"p1": 1}, "t1") {
		t.Fatal("self-loop transition must be enabled with one token")
	}
	next, ok := m.Fire(Marking{"p1": 1}, "t1")
	if !ok || next["p1"] != 1 {
		t.Fatalf("self-loop firing must return to same marking, got %v", next)
	}
	res := m.Explore(Marking{"p1": 1}, ExploreOptions{Strategy: BFS, Budget: 16})
	if res.Status != "complete" || len(res.States) != 1 {
		t.Fatalf("self-loop net must have exactly one reachable state, got %+v", res)
	}
}

func TestConcurrentTransitions(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}, {ID: "p4"}},
		[]Transition{{ID: "t1"}, {ID: "t2"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p2", Weight: 1, Kind: ArcOut},
			{ID: "a3", From: "p3", To: "t2", Weight: 1, Kind: ArcIn},
			{ID: "a4", From: "t2", To: "p4", Weight: 1, Kind: ArcOut},
		},
	)
	enabled := m.EnabledTransitions(Marking{"p1": 1, "p3": 1})
	if len(enabled) != 2 {
		t.Fatalf("both independent transitions must be enabled, got %v", enabled)
	}
	res := m.Explore(Marking{"p1": 1, "p3": 1}, ExploreOptions{Strategy: BFS, Budget: 16})
	if len(res.States) != 4 {
		t.Fatalf("concurrent net must reach 4 states, got %d", len(res.States))
	}
}

func TestCapacityBoundary(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}, {ID: "p2", HasCapacity: true, Capacity: 2}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p2", Weight: 2, Kind: ArcOut},
		},
	)
	if !m.Enabled(Marking{"p1": 1, "p2": 0}, "t1") {
		t.Fatal("firing into capacity 2 with weight 2 must be allowed at boundary")
	}
	if m.Enabled(Marking{"p1": 1, "p2": 1}, "t1") {
		t.Fatal("capacity overflow must disable the transition")
	}
}

func TestInhibitorArc(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}, {ID: "guard"}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "guard", To: "t1", Weight: 1, Kind: ArcInhibitor},
		},
	)
	if !m.Enabled(Marking{"p1": 1, "guard": 0}, "t1") {
		t.Fatal("inhibitor with empty guard must not block")
	}
	if m.Enabled(Marking{"p1": 1, "guard": 1}, "t1") {
		t.Fatal("inhibitor must block when guard holds tokens")
	}
}

func TestUnboundedGrowth(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p1", Weight: 2, Kind: ArcOut},
		},
	)
	res := m.Explore(Marking{"p1": 1}, ExploreOptions{Strategy: BFS, Budget: 8})
	if !res.Unbounded {
		t.Fatal("net producing more than it consumes must be flagged unbounded")
	}
	if len(res.Witness) == 0 {
		t.Fatal("unbounded net must retain a witness path")
	}
}

func TestSameGraphDifferentMarking(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}, {ID: "p2"}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p2", Weight: 1, Kind: ArcOut},
		},
	)
	a := m.Explore(Marking{"p1": 1, "p2": 0}, ExploreOptions{Strategy: BFS, Budget: 16})
	b := m.Explore(Marking{"p1": 2, "p2": 0}, ExploreOptions{Strategy: BFS, Budget: 16})
	if len(a.States) == len(b.States) {
		t.Fatalf("token-different initial markings must not merge: %d vs %d", len(a.States), len(b.States))
	}
	if CanonicalKey(Marking{"p1": 1}) == CanonicalKey(Marking{"p1": 2}) {
		t.Fatal("canonicalization merged token-different markings")
	}
}

func TestBudgetTruncation(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p1", Weight: 2, Kind: ArcOut},
		},
	)
	res := m.Explore(Marking{"p1": 1}, ExploreOptions{Strategy: BFS, Budget: 3})
	if res.Status != "unknown" {
		t.Fatalf("exhausted budget must return unknown, got %s", res.Status)
	}
	if !res.Truncated || len(res.Frontier) == 0 {
		t.Fatal("truncated exploration must retain its frontier")
	}
	if len(res.States) > 3 {
		t.Fatalf("budget must cap stored states, got %d", len(res.States))
	}
}

func TestDeadlockReason(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1", HasCapacity: true, Capacity: 1}, {ID: "p2"}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p2", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p1", Weight: 1, Kind: ArcOut},
		},
	)
	res := m.Explore(Marking{"p1": 1, "p2": 0}, ExploreOptions{Strategy: BFS, Budget: 16})
	if len(res.Deadlocks) != 1 {
		t.Fatalf("expected one deadlock, got %v", res.Deadlocks)
	}
	if len(res.DeadlockInfo) != 1 || len(res.DeadlockInfo[0].Blocked) != 1 {
		t.Fatalf("deadlock must carry blocking reasons, got %+v", res.DeadlockInfo)
	}
}

func TestTargetMarking(t *testing.T) {
	m := mkModel(
		[]Place{{ID: "p1"}, {ID: "p2"}},
		[]Transition{{ID: "t1"}},
		[]Arc{
			{ID: "a1", From: "p1", To: "t1", Weight: 1, Kind: ArcIn},
			{ID: "a2", From: "t1", To: "p2", Weight: 1, Kind: ArcOut},
		},
	)
	res := m.Explore(Marking{"p1": 1}, ExploreOptions{Strategy: BFS, Budget: 16, Target: Marking{"p2": 1}})
	if len(res.ReachTarget) != 1 {
		t.Fatalf("target marking must be located, got %v", res.ReachTarget)
	}
}
