package petrinet

import "testing"

func buildNet(t *testing.T) (*Net, Marking) {
	t.Helper()
	n := NewNet("net")
	n.PlaceIDs = []string{"p0", "p1", "p2"}
	n.TransitionIDs = []string{"t0", "t1"}
	for _, p := range n.PlaceIDs {
		n.PlaceOf[p] = Place{ID: p, Name: p}
		n.Capacity[p] = -1
	}
	for _, tr := range n.TransitionIDs {
		n.Transitions[tr] = Transition{ID: tr, Name: tr}
	}
	add := func(id string, k ArcKind, s, tgt string, w int64) {
		n.Arcs[id] = &Arc{ID: id, Kind: k, Source: s, Target: tgt, Weight: w}
	}
	add("a0", ArcInput, "p0", "t0", 1)
	add("a1", ArcOutput, "t0", "p1", 1)
	add("a2", ArcInput, "p1", "t1", 1)
	add("a3", ArcOutput, "t1", "p2", 1)
	return n, Marking{"p0": 1}
}

func TestEnablementInputsAndSequence(t *testing.T) {
	n, m := buildNet(t)
	if ok, _ := n.Enabled("t0", m); !ok {
		t.Fatal("t0 should be enabled")
	}
	if ok, _ := n.Enabled("t1", m); ok {
		t.Fatal("t1 should be disabled (no token in p1)")
	}
	m2, ok := n.Fire("t0", m)
	if !ok {
		t.Fatal("fire t0")
	}
	if m2["p1"] != 1 || m2["p0"] != 0 {
		t.Fatalf("unexpected marking %v", m2)
	}
	m3, ok := n.Fire("t1", m2)
	if !ok || m3["p2"] != 1 {
		t.Fatalf("fire t1 chain failed: %v %v", m3, ok)
	}
}

func TestCanonicalDoesNotMerge(t *testing.T) {
	places := []string{"p0", "p1"}
	a := Marking{"p0": 1, "p1": 0}
	b := Marking{"p0": 0, "p1": 1}
	c := Marking{"p0": 1}
	if a.Canonical(places) == b.Canonical(places) {
		t.Fatal("different token distributions must not merge")
	}
	if a.Canonical(places) != c.Canonical(places) {
		t.Fatal("absent and zero must canonicalize identically")
	}
}

func TestSelfLoopCapacity(t *testing.T) {
	n := NewNet("sl")
	n.PlaceIDs = []string{"p"}
	n.TransitionIDs = []string{"t"}
	n.PlaceOf["p"] = Place{ID: "p"}
	n.Capacity["p"] = 1
	n.Transitions["t"] = Transition{ID: "t"}
	n.Arcs["in"] = &Arc{ID: "in", Kind: ArcInput, Source: "p", Target: "t", Weight: 1}
	n.Arcs["out"] = &Arc{ID: "out", Kind: ArcOutput, Source: "t", Target: "p", Weight: 1}
	m := Marking{"p": 1}
	if ok, blocks := n.Enabled("t", m); !ok {
		t.Fatalf("self loop must free its token before refill: %v", blocks)
	}
	m2, _ := n.Fire("t", m)
	if m2["p"] != 1 {
		t.Fatal("self loop returns to one token")
	}
}

func TestCapacityBoundaryBlocks(t *testing.T) {
	n := NewNet("cap")
	n.PlaceIDs = []string{"p0", "p1"}
	n.TransitionIDs = []string{"t"}
	for _, p := range n.PlaceIDs {
		n.PlaceOf[p] = Place{ID: p}
	}
	n.Capacity["p0"] = -1
	n.Capacity["p1"] = 1
	n.Transitions["t"] = Transition{ID: "t"}
	n.Arcs["in"] = &Arc{ID: "in", Kind: ArcInput, Source: "p0", Target: "t", Weight: 1}
	n.Arcs["out"] = &Arc{ID: "out", Kind: ArcOutput, Source: "t", Target: "p1", Weight: 1}
	m := Marking{"p0": 1, "p1": 1}
	ok, blocks := n.Enabled("t", m)
	if ok {
		t.Fatal("capacity must block producing a second token")
	}
	found := false
	for _, b := range blocks {
		if b.Reason == ReasonCapacityFull {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected capacity reason, got %v", blocks)
	}
}

func TestInhibitor(t *testing.T) {
	n := NewNet("inh")
	n.PlaceIDs = []string{"p0", "p1"}
	n.TransitionIDs = []string{"t"}
	for _, p := range n.PlaceIDs {
		n.PlaceOf[p] = Place{ID: p}
		n.Capacity[p] = -1
	}
	n.Transitions["t"] = Transition{ID: "t"}
	n.Arcs["i"] = &Arc{ID: "i", Kind: ArcInhibitor, Source: "p0", Target: "t", Weight: 1}
	n.Arcs["o"] = &Arc{ID: "o", Kind: ArcOutput, Source: "t", Target: "p1", Weight: 1}

	if ok, _ := n.Enabled("t", Marking{"p0": 0}); !ok {
		t.Fatal("inhibitor weight 1 permits zero tokens")
	}
	if ok, blocks := n.Enabled("t", Marking{"p0": 1}); ok {
		t.Fatal("inhibitor must block at weight tokens")
	} else {
		has := false
		for _, b := range blocks {
			has = has || b.Reason == ReasonInhibited
		}
		if !has {
			t.Fatal("expected inhibitor reason")
		}
	}
}

func TestConcurrentTransitions(t *testing.T) {
	// One token enables two competing transitions from p0 to p1/p2.
	n := NewNet("conc")
	n.PlaceIDs = []string{"p0", "p1", "p2"}
	n.TransitionIDs = []string{"ta", "tb"}
	for _, p := range n.PlaceIDs {
		n.PlaceOf[p] = Place{ID: p}
		n.Capacity[p] = -1
	}
	for _, tr := range n.TransitionIDs {
		n.Transitions[tr] = Transition{ID: tr}
	}
	n.Arcs["ia"] = &Arc{ID: "ia", Kind: ArcInput, Source: "p0", Target: "ta", Weight: 1}
	n.Arcs["oa"] = &Arc{ID: "oa", Kind: ArcOutput, Source: "ta", Target: "p1", Weight: 1}
	n.Arcs["ib"] = &Arc{ID: "ib", Kind: ArcInput, Source: "p0", Target: "tb", Weight: 1}
	n.Arcs["ob"] = &Arc{ID: "ob", Kind: ArcOutput, Source: "tb", Target: "p2", Weight: 1}
	m := Marking{"p0": 1}
	got := n.EnabledTransitions(m)
	if len(got) != 2 {
		t.Fatalf("expected two concurrent enabled transitions, got %v", got)
	}
}

func TestUnboundedOmega(t *testing.T) {
	// t produces one token in p and loops: p grows without bound.
	n := NewNet("unb")
	n.PlaceIDs = []string{"p"}
	n.TransitionIDs = []string{"t"}
	n.PlaceOf["p"] = Place{ID: "p"}
	n.Capacity["p"] = -1
	n.Transitions["t"] = Transition{ID: "t"}
	n.Arcs["o"] = &Arc{ID: "o", Kind: ArcOutput, Source: "t", Target: "p", Weight: 1}
	res := n.Explore(Marking{"p": 0}, Marking{"p": 3}, Config{Strategy: StrategyBFS})
	found := false
	for _, p := range res.UnboundedPlaces {
		if p == "p" {
			found = true
		}
	}
	if !found {
		t.Fatalf("p should be detected unbounded, states=%d", len(res.States))
	}
	omegaState := 0
	for _, s := range res.States {
		if s.Omega != nil && s.Omega["p"] {
			omegaState++
		}
	}
	if omegaState == 0 {
		t.Fatal("expected at least one omega state")
	}
}

func TestBudgetUnknownKeepsFrontier(t *testing.T) {
	n := NewNet("chain")
	places := []string{"p0", "p1", "p2", "p3", "p4"}
	n.PlaceIDs = places
	for _, p := range places {
		n.PlaceOf[p] = Place{ID: p}
		n.Capacity[p] = -1
	}
	for i := 0; i < 4; i++ {
		tr := "t" + string(rune('0'+i))
		n.TransitionIDs = append(n.TransitionIDs, tr)
		n.Transitions[tr] = Transition{ID: tr}
		n.Arcs[tr+"i"] = &Arc{ID: tr + "i", Kind: ArcInput, Source: places[i], Target: tr, Weight: 1}
		n.Arcs[tr+"o"] = &Arc{ID: tr + "o", Kind: ArcOutput, Source: tr, Target: places[i+1], Weight: 1}
	}
	res := n.Explore(Marking{"p0": 1}, Marking{"p4": 1}, Config{Strategy: StrategyBFS, NodeBudget: 2})
	if res.Status != StatusUnknown {
		t.Fatalf("expected UNKNOWN, got %s", res.Status)
	}
	if len(res.Frontier) == 0 {
		t.Fatal("truncated exploration must retain a frontier")
	}
}

func TestReachableAndDeadlock(t *testing.T) {
	n, m := buildNet(t)
	res := n.Explore(m, Marking{"p2": 1}, Config{Strategy: StrategyBFS})
	if res.Status != StatusReachable {
		t.Fatalf("expected reachable, got %s", res.Status)
	}
	if res.TargetStateID == "" {
		t.Fatal("target state id missing")
	}
	dead := 0
	for _, s := range res.States {
		if s.Deadlock {
			dead++
		}
	}
	if dead != 1 {
		t.Fatalf("expected exactly one terminal deadlock state, got %d", dead)
	}
}

func TestSameGraphDifferentMarking(t *testing.T) {
	n, _ := buildNet(t)
	r1 := n.Explore(Marking{"p0": 1}, Marking{"p2": 1}, Config{Strategy: StrategyBFS})
	r2 := n.Explore(Marking{"p0": 2}, Marking{"p0": 0, "p1": 1, "p2": 1}, Config{Strategy: StrategyBFS})
	if r1.Status != StatusReachable || r2.Status != StatusReachable {
		t.Fatal("both should reach p2=1")
	}
	// With two starting tokens the reachable space is larger.
	if len(r2.States) <= len(r1.States) {
		t.Fatalf("different initial marking must produce different space: %d vs %d",
			len(r1.States), len(r2.States))
	}
}

func TestCoverRelation(t *testing.T) {
	places := []string{"p0", "p1"}
	big := Marking{"p0": 2, "p1": 1}
	small := Marking{"p0": 1, "p1": 1}
	if !big.Covers(small, places) {
		t.Fatal("2,1 covers 1,1")
	}
	if small.Covers(big, places) {
		t.Fatal("1,1 does not cover 2,1")
	}
}
