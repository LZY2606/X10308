package cases

import (
	"encoding/json"
	"fmt"
	"testing"

	"pnwork/internal/explore"
	"pnwork/internal/ingest"
	"pnwork/internal/pnnet"
	"pnwork/internal/service"
)

type fileMap = map[string][]byte

func buildCheck(t *testing.T, files fileMap) (*pnnet.Net, pnnet.Marking, explore.Config) {
	out, err := ingest.Parse(files["model"])
	if err != nil {
		t.Fatal(err)
	}
	mo := out.Net
	var wdoc struct {
		Arcs map[string]struct {
			Raw string `json:"raw"`
		} `json:"arcs"`
	}
	if err := json.Unmarshal(files["weights"], &wdoc); err != nil {
		t.Fatal(err)
	}
	arcByID := map[string]*pnnet.Arc{}
	for i := range mo.Arcs {
		arcByID[mo.Arcs[i].ID] = &mo.Arcs[i]
	}
	for id, e := range wdoc.Arcs {
		r := pnnet.Raw{Text: e.Raw, Unit: "token", Basis: "token"}
		var n int64
		fmt.Sscanf(e.Raw, "%d", &n)
		r.Value = &n
		arcByID[id].Weight = r
	}
	var cdoc struct {
		Places map[string]struct {
			Raw string `json:"raw"`
		} `json:"places"`
	}
	if err := json.Unmarshal(files["capacity"], &cdoc); err != nil {
		t.Fatal(err)
	}
	pb := mo.PlaceByID()
	for id, e := range cdoc.Places {
		r := pnnet.Raw{Text: e.Raw, Unit: "token", Basis: "token"}
		var n int64
		if e.Raw != "∞" {
			fmt.Sscanf(e.Raw, "%d", &n)
			r.Value = &n
		}
		pb[id].Capacity = r
	}
	pm, err := ingest.ParseMarking(files["initial_tokens"])
	if err != nil {
		t.Fatal(err)
	}
	st, err := ingest.ParseStrategy(files["strategy"])
	if err != nil {
		t.Fatal(err)
	}
	bg, err := ingest.ParseBudget(files["budget"])
	if err != nil {
		t.Fatal(err)
	}
	return mo, pm.Marking, explore.Config{Strategy: st.Strategy, Budget: bg.Budget}
}

func branchFiles(bun *service.Bundle, id string) fileMap {
	br := bun.Baseline
	if id != BranchBaseline {
		for _, x := range bun.Branches {
			if x.ID == id {
				br = x
			}
		}
	}
	m := fileMap{}
	for _, a := range br.Aspects {
		m[a.Aspect] = []byte(a.Content)
	}
	return m
}

func TestTmpScenarioProbe(t *testing.T) {
	bun := Bundle()
	probe := func(branch string, goal pnnet.Marking) {
		net, init0, cfg := buildCheck(t, branchFiles(bun, branch))
		eng := explore.NewEngine(net, cfg)
		r := eng.CoverReach(init0, goal)
		t.Logf("branch=%s goal=%s verdict=%s exact=%v covered=%v nodes=%d frontier=%d paths=%d omega=%v",
			branch, goal.Canonical(net.SortedPlaceIDs()), r.Verdict, r.ExactReach, r.Covered, r.NodeCount, len(r.Frontier), len(r.Paths), r.OmegaPlaces)
		for i, p := range r.Paths {
			str := ""
			for _, st := range p {
				str += " " + st.Transition
			}
			t.Logf("  path%d:%s", i+1, str)
		}
	}
	probe(BranchBaseline, pnnet.Marking{"p_ready": 1, "p_pool": 3, "p_buffer": 1, "p_loopctl": 1})
	probe(BranchBaseline, pnnet.Marking{"p_pool": 8, "p_loopctl": 1})
	probe(BranchBaseline, pnnet.Marking{"p_buffer": 3, "p_loopctl": 1})
	probe(BranchBaseline, pnnet.Marking{"p_guard": 1, "p_pool": 9, "p_loopctl": 1})
	probe(BranchDecisionA, pnnet.Marking{"p_ready": 0, "p_pool": 0, "p_buffer": 0, "p_done": 2, "p_guard": 1, "p_loopctl": 0})
	{
		net, init0, _ := buildCheck(t, branchFiles(bun, BranchDecisionA))
		if info := net.DeadlockInfo(init0); info == nil {
			t.Errorf("initial m1 should be a deadlock")
		} else {
			t.Logf("m1 deadlock reasons: %v", info)
		}
	}
	probe(BranchParamRev, pnnet.Marking{"p_pool": 8, "p_loopctl": 1})

	net, init0, cfg := buildCheck(t, branchFiles(bun, BranchBaseline))
	eng := explore.NewEngine(net, cfg)
	sp, cover := eng.Enumerate(init0, []pnnet.Marking{{"p_pool": 5, "p_loopctl": 1}, {"p_buffer": 3, "p_loopctl": 1}})
	t.Logf("enumerate expanded=%d visited=%d budgetHit=%v deadlocks=%d frontier=%d coverKeys=%d",
		sp.ExpandedCount, sp.VisitedCount, sp.BudgetHit, len(sp.Deadlocks), len(sp.Frontier), len(cover))
	for k, v := range cover {
		t.Logf(" cover %s -> %d", k, len(v))
	}
	mo, err := ingest.Parse(branchFiles(bun, BranchBaseline)["model"])
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range mo.Diagnostics {
		t.Logf("diag %s/%s: %s", d.Object, d.Field, d.Reason)
	}
}

func TestTmpConcurrencyExact(t *testing.T) {
	bun := Bundle()
	net, init0, cfg := buildCheck(t, branchFiles(bun, BranchBaseline))
	eng := explore.NewEngine(net, cfg)
	goal := pnnet.Marking{"p_ready": 0, "p_pool": 3, "p_buffer": 2, "p_loopctl": 1}
	r := eng.Reach(init0, goal)
	t.Logf("exact concurrency verdict=%s visited=%d paths=%d", r.Verdict, r.VisitedCount, len(r.Paths))
	for i, p := range r.Paths {
		str := ""
		for _, st := range p {
			str += " " + st.Transition
		}
		t.Logf(" path%d:%s", i+1, str)
	}
}
