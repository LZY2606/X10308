package casepack

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"petriws/internal/petri"
	"petriws/internal/store"
)

func seeded(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "case.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := Seed(s); err != nil {
		t.Fatal(err)
	}
	// Idempotent: second seed is a no-op.
	if err := Seed(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSeedBaselineVersioned(t *testing.T) {
	s := seeded(t)
	srcs, err := s.ListSources()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, src := range srcs {
		kinds[src.Kind]++
	}
	for _, k := range []string{"model", "weights", "marking", "capacities", "strategy", "budget"} {
		if kinds[k] == 0 {
			t.Fatalf("missing versioned source kind %s", k)
		}
	}
	mv, err := s.GetModelVersion("mv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(mv.SourceIDs) != 6 {
		t.Fatalf("baseline must reference all six sources: %v", mv.SourceIDs)
	}
	// Raw representation preserved: capacity keeps its unit string.
	raw, _ := s.GetSource("src-capacities-v1")
	var caps map[string]string
	json.Unmarshal(raw.Raw, &caps)
	if caps["p_stage"] != "2 token" {
		t.Fatalf("raw capacity must keep unit: %v", caps["p_stage"])
	}
	// Unconvertible value kept with diagnostic, not cleaned.
	diags, _ := s.ListDiagnostics("src-model-v1")
	if len(diags) != 1 || diags[0].RawValue != "unknown" {
		t.Fatalf("expected diagnostic for unconvertible capacity: %+v", diags)
	}
}

func TestSeedNarrative(t *testing.T) {
	s := seeded(t)
	events, err := s.ListEvents("br-main", 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	for _, want := range []string{"import", "diagnostic", "run", "version", "invalidate", "ruling"} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("main branch missing %s event; got %v", want, kinds)
		}
	}
	// Parameter revision produced a truncated run with frontier kept.
	r2, err := s.GetRun("run-main-2")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != "unknown" {
		t.Fatalf("budget revision must yield unknown, got %s", r2.Status)
	}
	var res petri.Result
	json.Unmarshal([]byte(r2.ResultJSON), &res)
	if len(res.Frontier) == 0 {
		t.Fatalf("frontier must be preserved")
	}
	// Conflicting rulings both preserved.
	rulings, _ := s.ListEvents("br-main", 0)
	var rulingPayloads []string
	for _, e := range rulings {
		if e.Kind == "ruling" {
			rulingPayloads = append(rulingPayloads, e.PayloadJSON)
		}
	}
	if len(rulingPayloads) < 2 {
		t.Fatalf("conflicting rulings must both be preserved: %v", rulingPayloads)
	}
	// Local source change: mv-3 depends on src-weights-v2, not v1.
	mv3, _ := s.GetModelVersion("mv-3")
	found := false
	for _, id := range mv3.SourceIDs {
		if id == "src-weights-v2" {
			found = true
		}
		if id == "src-weights-v1" {
			t.Fatalf("mv-3 must not reference the old weights source")
		}
	}
	if !found {
		t.Fatalf("mv-3 must reference src-weights-v2")
	}
}

func TestCounterexampleArchive(t *testing.T) {
	s := seeded(t)
	cex, err := s.ListCounterexamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(cex) != 7 {
		t.Fatalf("expected 7 counterexamples, got %d", len(cex))
	}
	scenarios := map[string]string{}
	for _, c := range cex {
		scenarios[c.Scenario] = c.Judgment
	}
	for _, want := range []string{"self-loop", "concurrent-transitions", "capacity-boundary",
		"inhibitor-arc", "unbounded-growth", "same-graph-different-marking", "budget-truncation"} {
		if _, ok := scenarios[want]; !ok {
			t.Fatalf("missing scenario %s", want)
		}
	}
	// Both accepted and rejected results are kept.
	var acc, rej int
	for _, c := range cex {
		if c.Judgment == "accepted" {
			acc++
		}
		if c.Judgment == "rejected" {
			rej++
		}
		// Provenance chain: run -> model version -> sources.
		deps, err := s.DependenciesOf("run", c.BaselineRunID)
		if err != nil || len(deps) == 0 {
			t.Fatalf("run %s missing provenance: %v", c.BaselineRunID, err)
		}
	}
	if acc == 0 || rej == 0 {
		t.Fatalf("archive must keep accepted and rejected: %d/%d", acc, rej)
	}
	// Unbounded growth: unknown with frontier and cover relations.
	for _, c := range cex {
		if c.Scenario != "unbounded-growth" {
			continue
		}
		run, _ := s.GetRun(c.BaselineRunID)
		var res petri.Result
		json.Unmarshal([]byte(run.ResultJSON), &res)
		if res.Status != "unknown" || len(res.Frontier) == 0 || len(res.Covers) == 0 {
			t.Fatalf("unbounded growth must be unknown with frontier and covers")
		}
	}
	// Budget truncation: strategy change alters frontier order only.
	for _, c := range cex {
		if c.Scenario != "budget-truncation" {
			continue
		}
		b, _ := s.GetRun(c.BaselineRunID)
		v, _ := s.GetRun(c.VariantRunID)
		var rb, rv petri.Result
		json.Unmarshal([]byte(b.ResultJSON), &rb)
		json.Unmarshal([]byte(v.ResultJSON), &rv)
		if rb.Status != "unknown" || rv.Status != "unknown" {
			t.Fatalf("both truncated runs must be unknown")
		}
		if len(rb.Frontier) == 0 || len(rv.Frontier) == 0 {
			t.Fatalf("frontier must be kept on both")
		}
		if rb.Frontier[0] == rv.Frontier[0] && len(rb.Frontier) == len(rv.Frontier) && rb.Frontier[0] != "" {
			if rb.Frontier[0] == rv.Frontier[0] && (len(rb.Frontier) < 2 || rb.Frontier[1] == rv.Frontier[1]) {
				t.Fatalf("dfs frontier order should differ from bfs: %v vs %v", rb.Frontier, rv.Frontier)
			}
		}
	}
}

func TestEnablementBranch(t *testing.T) {
	s := seeded(t)
	br, err := s.GetBranch("br-enablement")
	if err != nil {
		t.Fatal(err)
	}
	if br.ParentID != "br-main" {
		t.Fatalf("re-interpretation must fork a branch, got parent %q", br.ParentID)
	}
	// Multi-candidates preserved on the second branch.
	cands, _ := s.ListCandidates("br-enablement")
	if len(cands) < 2 {
		t.Fatalf("all legal candidates must be preserved, got %d", len(cands))
	}
	// Legacy v1 run explores more states than v2 (inhibitor ignored).
	r1, _ := s.GetRun("run-enab-legacy")
	r2, _ := s.GetRun("run-enab-v2")
	var res1, res2 petri.Result
	json.Unmarshal([]byte(r1.ResultJSON), &res1)
	json.Unmarshal([]byte(r2.ResultJSON), &res2)
	if len(res1.States) <= len(res2.States) {
		t.Fatalf("v1 (no inhibitor/capacity) must reach more states: %d vs %d",
			len(res1.States), len(res2.States))
	}
}
