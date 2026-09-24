package petri

import (
	"testing"
)

// helper: find state by canonical id
func findState(res *Result, id string) *State {
	for i := range res.States {
		if res.States[i].ID == id {
			return &res.States[i]
		}
	}
	return nil
}

// 1. Self-loop: t1 consumes 1 from p1 and returns 1 to p1.
func TestSelfLoop(t *testing.T) {
	net := &Net{
		ID:          "selfloop",
		Places:      []Place{{ID: "p1", Capacity: -1}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 1},
			{ID: "a2", Src: "t1", Dst: "p1", Kind: ArcOutput, Weight: 1},
		},
	}
	res := net.Explore(Marking{"p1": 1}, 100)
	if res.Status != "complete" {
		t.Fatalf("status=%s", res.Status)
	}
	if len(res.States) != 1 {
		t.Fatalf("self-loop should yield exactly 1 state, got %d", len(res.States))
	}
	if len(res.Edges) != 1 || res.Edges[0].From != res.Edges[0].To {
		t.Fatalf("expected one self edge, got %+v", res.Edges)
	}
}

// 2. Concurrent transitions: t1 and t2 both enabled from the same marking,
// and both orders must appear in the state space.
func TestConcurrentTransitions(t *testing.T) {
	net := &Net{
		ID:     "concurrent",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: -1}, {ID: "p3", Capacity: -1}, {ID: "p4", Capacity: -1}},
		Transitions: []Transition{{ID: "t1"}, {ID: "t2"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 1},
			{ID: "a2", Src: "t1", Dst: "p2", Kind: ArcOutput, Weight: 1},
			{ID: "a3", Src: "p3", Dst: "t2", Kind: ArcInput, Weight: 1},
			{ID: "a4", Src: "t2", Dst: "p4", Kind: ArcOutput, Weight: 1},
		},
	}
	res := net.Explore(Marking{"p1": 1, "p3": 1}, 100)
	if res.Status != "complete" {
		t.Fatalf("status=%s", res.Status)
	}
	// interleavings: t1 then t2 and t2 then t1 both reach p2=1;p4=1
	if findState(res, "p1=0;p2=1;p3=0;p4=1") == nil {
		t.Fatalf("missing joined state; states=%v", res.States)
	}
	if findState(res, "p1=0;p2=1;p3=1;p4=0") == nil || findState(res, "p1=1;p2=0;p3=0;p4=1") == nil {
		t.Fatalf("missing intermediate interleavings")
	}
}

// 3. Capacity boundary: capacity 1 on p2 blocks t1 when p2 already holds 1.
func TestCapacityBoundary(t *testing.T) {
	net := &Net{
		ID:     "capacity",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: 1}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 1},
			{ID: "a2", Src: "t1", Dst: "p2", Kind: ArcOutput, Weight: 1},
		},
	}
	// p2 already full: t1 disabled by capacity, with machine-readable reason.
	ok, bad := net.Enabled("t1", Marking{"p1": 1, "p2": 1})
	if ok {
		t.Fatal("t1 should be disabled by capacity")
	}
	found := false
	for _, v := range bad {
		if v.Kind == ViolationKindCapacity && v.PlaceID == "p2" && v.Need == 1 && v.Have == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing capacity violation: %+v", bad)
	}
	res := net.Explore(Marking{"p1": 2, "p2": 0}, 100)
	if len(res.Deadlocks) != 1 {
		t.Fatalf("expected 1 deadlock (capacity-blocked), got %d", len(res.Deadlocks))
	}
	dl := res.Deadlocks[0]
	if dl.StateID != "p1=1;p2=1" {
		t.Fatalf("deadlock at %s", dl.StateID)
	}
	if len(dl.Reasons["t1"]) == 0 || dl.Reasons["t1"][0].Kind != ViolationKindCapacity {
		t.Fatalf("deadlock reason should cite capacity: %+v", dl.Reasons)
	}
}

// 4. Inhibitor arc: t1 fires only while p2 < 1.
func TestInhibitorArc(t *testing.T) {
	net := &Net{
		ID:     "inhibitor",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: -1}, {ID: "p3", Capacity: -1}},
		Transitions: []Transition{{ID: "t1"}, {ID: "t2"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 1},
			{ID: "a2", Src: "t1", Dst: "p3", Kind: ArcOutput, Weight: 1},
			{ID: "a3", Src: "p2", Dst: "t1", Kind: ArcInhibitor, Weight: 1},
			{ID: "a4", Src: "p1", Dst: "t2", Kind: ArcInput, Weight: 1},
			{ID: "a5", Src: "t2", Dst: "p2", Kind: ArcOutput, Weight: 1},
		},
	}
	ok, _ := net.Enabled("t1", Marking{"p1": 1, "p2": 0})
	if !ok {
		t.Fatal("t1 should be enabled while inhibitor place empty")
	}
	ok, bad := net.Enabled("t1", Marking{"p1": 1, "p2": 1})
	if ok {
		t.Fatal("t1 should be inhibited")
	}
	if bad[0].Kind != ArcInhibitor {
		t.Fatalf("expected inhibitor violation, got %+v", bad)
	}
}

// 5. Unbounded growth: producer loop must ω-accelerate, not run forever.
func TestUnboundedGrowth(t *testing.T) {
	net := &Net{
		ID:     "unbounded",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: -1}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 1},
			{ID: "a2", Src: "t1", Dst: "p1", Kind: ArcOutput, Weight: 1},
			{ID: "a3", Src: "t1", Dst: "p2", Kind: ArcOutput, Weight: 1},
		},
	}
	res := net.Explore(Marking{"p1": 1}, 1000)
	if res.Status != "complete" {
		t.Fatalf("ω acceleration should terminate, status=%s states=%d", res.Status, len(res.States))
	}
	if len(res.Covers) == 0 {
		t.Fatal("expected at least one cover pair")
	}
	omega := findState(res, "p1=1;p2=ω")
	if omega == nil {
		t.Fatalf("expected ω state, states=%v", res.States)
	}
	if res.Covers[0].OmegaPlace[0] != "p2" {
		t.Fatalf("omega place = %v", res.Covers[0].OmegaPlace)
	}
}

// 6. Same graph, different markings: canonicalization must not merge
// markings whose token counts differ.
func TestCanonicalizationKeepsTokenDistinct(t *testing.T) {
	net := &Net{
		ID:     "canon",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: -1}},
	}
	a := net.Canonical(Marking{"p1": 1, "p2": 2})
	b := net.Canonical(Marking{"p1": 2, "p2": 1})
	c := net.Canonical(Marking{"p2": 2, "p1": 1}) // same tokens, other order
	if a == b {
		t.Fatal("different token counts merged")
	}
	if a != c {
		t.Fatal("same marking canonicalized differently")
	}
	if net.Canonical(Marking{"p1": Omega}) != "p1=ω;p2=0" {
		t.Fatalf("omega rendering: %s", net.Canonical(Marking{"p1": Omega}))
	}
}

// 7. Budget truncation: returns unknown and preserves the frontier.
func TestBudgetTruncation(t *testing.T) {
	net := &Net{
		ID:     "budget",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: -1}, {ID: "p3", Capacity: -1}},
		Transitions: []Transition{{ID: "t1"}, {ID: "t2"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 1},
			{ID: "a2", Src: "t1", Dst: "p2", Kind: ArcOutput, Weight: 1},
			{ID: "a3", Src: "p2", Dst: "t2", Kind: ArcInput, Weight: 1},
			{ID: "a4", Src: "t2", Dst: "p3", Kind: ArcOutput, Weight: 1},
		},
	}
	res := net.Explore(Marking{"p1": 1}, 1)
	if res.Status != "unknown" {
		t.Fatalf("status=%s, want unknown", res.Status)
	}
	if len(res.Frontier) == 0 {
		t.Fatal("frontier must be preserved on truncation")
	}
	if res.Frontier[0].ID != "p1=0;p2=1;p3=0" {
		t.Fatalf("frontier=%+v", res.Frontier)
	}
	// Full budget reaches the terminal state.
	full := net.Explore(Marking{"p1": 1}, 100)
	if full.Status != "complete" || findState(full, "p1=0;p2=0;p3=1") == nil {
		t.Fatalf("full exploration failed: %s", full.Status)
	}
}

// Enablement considers input + inhibitor + capacity simultaneously.
func TestEnablementCombined(t *testing.T) {
	net := &Net{
		ID:     "combined",
		Places: []Place{{ID: "p1", Capacity: -1}, {ID: "p2", Capacity: -1}, {ID: "p3", Capacity: 1}},
		Transitions: []Transition{{ID: "t1"}},
		Arcs: []Arc{
			{ID: "a1", Src: "p1", Dst: "t1", Kind: ArcInput, Weight: 2},
			{ID: "a2", Src: "p2", Dst: "t1", Kind: ArcInhibitor, Weight: 1},
			{ID: "a3", Src: "t1", Dst: "p3", Kind: ArcOutput, Weight: 1},
		},
	}
	// input insufficient AND inhibited AND capacity would overflow
	ok, bad := net.Enabled("t1", Marking{"p1": 1, "p2": 5, "p3": 1})
	if ok {
		t.Fatal("should be disabled")
	}
	kinds := map[ArcKind]bool{}
	for _, v := range bad {
		kinds[v.Kind] = true
	}
	if !kinds[ArcInput] || !kinds[ArcInhibitor] {
		t.Fatalf("expected input+inhibitor violations, got %+v", bad)
	}
	// capacity only
	ok, bad = net.Enabled("t1", Marking{"p1": 2, "p2": 0, "p3": 1})
	if ok || len(bad) != 1 || bad[0].Kind != ViolationKindCapacity {
		t.Fatalf("expected single capacity violation, got ok=%v %+v", ok, bad)
	}
	// all satisfied
	ok, _ = net.Enabled("t1", Marking{"p1": 2, "p2": 0, "p3": 0})
	if !ok {
		t.Fatal("should be enabled")
	}
}
