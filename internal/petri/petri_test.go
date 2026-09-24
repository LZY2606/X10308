package petri

import (
	"testing"
)

func i64(v int64) *int64 { return &v }

// self-loop: t1 consumes 1 from p1 and returns 1 to p1; enabled only while
// p1 has a token; firing does not change the marking.
func TestSelfLoop(t *testing.T) {
	net := &Net{
		Places:      []Place{{ID: "p1", Capacity: i64(1)}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs: []Arc{
			{ID: "a1", From: "p1", To: "t1", Kind: ArcNormal, Weight: 1},
			{ID: "a2", From: "t1", To: "p1", Kind: ArcNormal, Weight: 1},
		},
		Initial: Marking{"p1": 1},
	}
	res := Explore(net, StrategyBFS, 16, RulesFor(2))
	if res.Status != "complete" {
		t.Fatalf("status = %s", res.Status)
	}
	if len(res.States) != 1 {
		t.Fatalf("self-loop must not create new states, got %d", len(res.States))
	}
	if len(res.Edges) != 1 || res.Edges[0].To != "s0" {
		t.Fatalf("expected one self edge, got %+v", res.Edges)
	}
	// Without the initial token the transition is dead.
	net.Initial = Marking{"p1": 0}
	res = Explore(net, StrategyBFS, 16, RulesFor(2))
	if !res.States[0].Deadlock {
		t.Fatalf("expected deadlock, reason %q", res.States[0].Reason)
	}
}

// concurrent transitions: two transitions compete for the same input place.
func TestConcurrentTransitions(t *testing.T) {
	net := &Net{
		Places: []Place{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}},
		Transitions: []Transition{
			{ID: "t1"}, {ID: "t2"},
		},
		Arcs: []Arc{
			{ID: "a1", From: "p1", To: "t1", Kind: ArcNormal, Weight: 1},
			{ID: "a2", From: "t1", To: "p2", Kind: ArcNormal, Weight: 1},
			{ID: "a3", From: "p1", To: "t2", Kind: ArcNormal, Weight: 1},
			{ID: "a4", From: "t2", To: "p3", Kind: ArcNormal, Weight: 1},
		},
		Initial: Marking{"p1": 2},
	}
	res := Explore(net, StrategyBFS, 32, RulesFor(2))
	if res.Status != "complete" {
		t.Fatalf("status = %s", res.Status)
	}
	// Both interleavings reachable: markings (p2=1,p1=1), (p3=1,p1=1),
	// (p2=1,p3=1), (p2=2), (p3=2) plus initial.
	if len(res.States) != 6 {
		keys := []string{}
		for _, s := range res.States {
			keys = append(keys, s.Key)
		}
		t.Fatalf("expected 6 states, got %d: %v", len(res.States), keys)
	}
}

// capacity boundary: producer blocked exactly at the capacity.
func TestCapacityBoundary(t *testing.T) {
	net := &Net{
		Places:      []Place{{ID: "p1", Capacity: i64(2)}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs:        []Arc{{ID: "a1", From: "t1", To: "p1", Kind: ArcNormal, Weight: 1}},
		Initial:     Marking{"p1": 0},
	}
	res := Explore(net, StrategyBFS, 16, RulesFor(2))
	if res.Status != "complete" || len(res.States) != 3 {
		t.Fatalf("capacity 2 must yield exactly 3 states, got %d (%s)", len(res.States), res.Status)
	}
	last := res.States[len(res.States)-1]
	if !last.Deadlock {
		t.Fatalf("state at capacity must be a deadlock")
	}
	if want := "capacity:p1"; !contains(last.Reason, want) {
		t.Fatalf("deadlock reason %q must mention %q", last.Reason, want)
	}
	// Legacy rule v1 ignores capacity: unbounded growth, budget truncates.
	res = Explore(net, StrategyBFS, 5, RulesFor(1))
	if res.Status != "unknown" || len(res.Frontier) == 0 {
		t.Fatalf("v1 without capacity must be unknown with frontier, got %s", res.Status)
	}
}

// inhibitor arc: t1 fires only while p2 holds fewer tokens than threshold.
func TestInhibitorArc(t *testing.T) {
	net := &Net{
		Places: []Place{{ID: "p1"}, {ID: "p2", Capacity: i64(1)}},
		Transitions: []Transition{
			{ID: "t1"}, {ID: "t2"},
		},
		Arcs: []Arc{
			{ID: "a1", From: "p1", To: "t1", Kind: ArcNormal, Weight: 1},
			{ID: "a2", From: "p2", To: "t1", Kind: ArcInhibitor, Weight: 1},
			{ID: "a3", From: "t2", To: "p2", Kind: ArcNormal, Weight: 1},
		},
		Initial: Marking{"p1": 1, "p2": 0},
	}
	res := Explore(net, StrategyBFS, 16, RulesFor(2))
	if res.Status != "complete" {
		t.Fatalf("status = %s", res.Status)
	}
	// From (p1=1,p2=0): t1 fires -> (0,0); t2 fires -> (1,1) where t1 is
	// inhibited. (1,1) must be a deadlock caused by the inhibitor.
	var inhibited *State
	for i := range res.States {
		if res.States[i].Marking["p1"] == 1 && res.States[i].Marking["p2"] == 1 {
			inhibited = &res.States[i]
		}
	}
	if inhibited == nil || !inhibited.Deadlock {
		t.Fatalf("expected inhibited deadlock state, got %+v", res.States)
	}
	if !contains(inhibited.Reason, "inhibitor:p2") {
		t.Fatalf("reason %q must cite the inhibitor arc", inhibited.Reason)
	}
	// Under legacy rules v1 the inhibitor is ignored: no deadlock there.
	res = Explore(net, StrategyBFS, 16, RulesFor(1))
	for _, s := range res.States {
		if s.Marking["p1"] == 1 && s.Marking["p2"] == 1 && s.Deadlock {
			t.Fatalf("v1 must ignore inhibitor arcs")
		}
	}
}

// unbounded growth: no capacity, producer loops forever; budget truncates
// and the frontier is preserved.
func TestUnboundedGrowth(t *testing.T) {
	net := &Net{
		Places:      []Place{{ID: "p1"}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs:        []Arc{{ID: "a1", From: "t1", To: "p1", Kind: ArcNormal, Weight: 1}},
		Initial:     Marking{"p1": 0},
	}
	res := Explore(net, StrategyBFS, 4, RulesFor(2))
	if res.Status != "unknown" {
		t.Fatalf("unbounded net must be unknown, got %s", res.Status)
	}
	if len(res.Frontier) == 0 {
		t.Fatalf("frontier must be preserved on budget exhaustion")
	}
	// Cover relation: each ancestor is strictly covered by descendants.
	if len(res.Covers) == 0 {
		t.Fatalf("expected cover relations for growing markings")
	}
	for _, c := range res.Covers {
		a, d := res.StateByID(c.Ancestor), res.StateByID(c.Descendant)
		if a.Marking["p1"] >= d.Marking["p1"] {
			t.Fatalf("cover %s -> %s is not strict growth", c.Ancestor, c.Descendant)
		}
	}
}

// same graph, different marking: canonicalization never merges states that
// differ in tokens.
func TestSameGraphDifferentMarking(t *testing.T) {
	mk := func(init Marking) *Net {
		return &Net{
			Places:      []Place{{ID: "p1"}, {ID: "p2"}},
			Transitions: []Transition{{ID: "t1"}},
			Arcs: []Arc{
				{ID: "a1", From: "p1", To: "t1", Kind: ArcNormal, Weight: 1},
				{ID: "a2", From: "t1", To: "p2", Kind: ArcNormal, Weight: 1},
			},
			Initial: init,
		}
	}
	a := Explore(mk(Marking{"p1": 1, "p2": 0}), StrategyBFS, 16, RulesFor(2))
	b := Explore(mk(Marking{"p1": 0, "p2": 1}), StrategyBFS, 16, RulesFor(2))
	if a.States[0].Key == b.States[0].Key {
		t.Fatalf("token-distinct markings merged: %q", a.States[0].Key)
	}
	if len(a.States) != 2 || len(b.States) != 1 {
		t.Fatalf("same graph different marking must explore differently: %d vs %d",
			len(a.States), len(b.States))
	}
	if b.States[0].Deadlock != true {
		t.Fatalf("marking (0,1) must deadlock immediately")
	}
}

// budget truncation: unknown status, frontier kept in exploration order.
func TestBudgetTruncation(t *testing.T) {
	net := &Net{
		Places: []Place{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}},
		Transitions: []Transition{
			{ID: "t1"}, {ID: "t2"},
		},
		Arcs: []Arc{
			{ID: "a1", From: "p1", To: "t1", Kind: ArcNormal, Weight: 1},
			{ID: "a2", From: "t1", To: "p2", Kind: ArcNormal, Weight: 1},
			{ID: "a3", From: "p1", To: "t2", Kind: ArcNormal, Weight: 1},
			{ID: "a4", From: "t2", To: "p3", Kind: ArcNormal, Weight: 1},
		},
		Initial: Marking{"p1": 2},
	}
	full := Explore(net, StrategyBFS, 100, RulesFor(2))
	trunc := Explore(net, StrategyBFS, 1, RulesFor(2))
	if trunc.Status != "unknown" {
		t.Fatalf("expected unknown, got %s", trunc.Status)
	}
	if len(trunc.Frontier) != 2 || trunc.Frontier[0] != "s1" || trunc.Frontier[1] != "s2" {
		t.Fatalf("frontier must hold unexpanded states in order, got %v", trunc.Frontier)
	}
	if len(trunc.States) >= len(full.States) {
		t.Fatalf("truncated run must be a strict prefix")
	}
	// DFS and BFS explore different prefixes under the same budget.
	dfs := Explore(net, StrategyDFS, 1, RulesFor(2))
	if dfs.Frontier[0] != "s2" || dfs.Frontier[1] != "s1" {
		t.Fatalf("dfs frontier order should differ, got %v", dfs.Frontier)
	}
}

// normalize: units preserved in raw, diagnostics for unconvertible values.
func TestNormalizeDiagnostics(t *testing.T) {
	doc := &RawDoc{}
	doc.Places = append(doc.Places,
		struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Capacity string `json:"capacity"`
			X        int    `json:"x"`
			Y        int    `json:"y"`
		}{ID: "p1", Capacity: "unknown", X: 1, Y: 2},
	)
	doc.Initial = append(doc.Initial, struct {
		Place  string `json:"place"`
		Tokens string `json:"tokens"`
	}{Place: "p1", Tokens: "3 token"})
	net, diags, err := Normalize(doc)
	if err != nil {
		t.Fatal(err)
	}
	if net.Places[0].Capacity != nil {
		t.Fatalf("unconvertible capacity must fall back to unbounded")
	}
	if len(diags) != 1 || diags[0].RawValue != "unknown" {
		t.Fatalf("expected one diagnostic keeping the raw value, got %+v", diags)
	}
	if net.Initial["p1"] != 3 {
		t.Fatalf("unit-suffixed tokens must parse: %v", net.Initial)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
