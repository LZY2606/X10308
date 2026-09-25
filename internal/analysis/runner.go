package analysis

import (
	"fmt"
	"sort"

	"petristudio/internal/petri"
)

// CandidateKind enumerates what a candidate asserts.
type CandidateKind string

const (
	CandReachability CandidateKind = "reachability"
	CandDeadlock     CandidateKind = "deadlock"
	CandCover        CandidateKind = "cover"
)

// Candidate is one legal assertion the exploration produced. When the
// derivation cannot decide uniquely all legal candidates are retained.
type Candidate struct {
	ID            string                 `json:"id"`
	Kind          CandidateKind          `json:"kind"`
	TargetID      string                 `json:"target_id,omitempty"`
	Key           string                 `json:"key"`
	Verdict       string                 `json:"verdict"` // reachable | unreachable | unknown
	StateSeq      int                    `json:"state_seq,omitempty"`
	Path          []string               `json:"path,omitempty"`
	Deadlock      *petri.Deadlock        `json:"deadlock,omitempty"`
	CoveredByKey  string                 `json:"covered_by_key,omitempty"`
	Reason        string                 `json:"reason"`
	DependsOn     []string               `json:"depends_on"` // aspect names
}

// Run is an immutable analysis result snapshot.
type Run struct {
	ID             string                 `json:"id"`
	InterpretationID string               `json:"interpretation_id"`
	Status         string                 `json:"status"` // complete | truncated
	Rules          []string               `json:"rules"`
	Result         *petri.Result          `json:"result"`
	Cover          *petri.CoverResult     `json:"cover"`
	Candidates     []Candidate            `json:"candidates"`
	FrontierCount  int                    `json:"frontier_count"`
}

// Execute runs every derivation for an interpretation.
func Execute(it *Interpretation) (*Run, error) {
	idx, err := it.Net.BuildIndex()
	if err != nil {
		return nil, err
	}
	res, err := it.Net.Explore(idx, it.Initial, it.Strategy)
	if err != nil {
		return nil, err
	}
	cov, err := it.Net.Coverability(it.Initial, it.Strategy.StateBudget)
	if err != nil {
		return nil, err
	}
	run := &Run{
		InterpretationID: it.ID, Status: res.Status, Rules: AllRules(),
		Result: res, Cover: cov, FrontierCount: len(res.Frontier),
	}

	// Reachability candidates per declared target. Budget exhaustion =>
	// unknown (never silently "unreachable"), with frontier retained.
	for _, t := range it.Targets {
		c := Candidate{ID: "cand_reach_" + it.ID[4:] + "_" + t.ID, Kind: CandReachability, TargetID: t.ID, Key: t.Key, DependsOn: []string{AspectInitialTokens, AspectArcWeights, AspectNodeIdentity, AspectTruncationBudget, AspectExplorationStrategy, AspectCapacities}}
		if t.Key == "" {
			c.Verdict, c.Reason = "unknown", "target-not-normalizable"
			run.Candidates = append(run.Candidates, c)
			continue
		}
		found, known := res.Reachable(t.Key)
		switch {
		case found:
			c.Verdict = "reachable"
			for _, st := range res.States {
				if st.Key == t.Key {
					c.StateSeq = st.Seq
					c.Path = res.PathTo(st.Seq)
					break
				}
			}
			c.Reason = "reachable-state-discovered"
		case !known:
			c.Verdict, c.Reason = "unknown", "budget-exhausted-frontier-preserved"
		default:
			c.Verdict, c.Reason = "unreachable", "exhaustive-complete-exploration"
		}
		run.Candidates = append(run.Candidates, c)
	}

	// Deadlock candidates.
	for _, dl := range res.Deadlocks {
		dl := dl
		run.Candidates = append(run.Candidates, Candidate{
			ID:        fmt.Sprintf("cand_dead_%s_%d", it.ID[4:], dl.StateSeq),
			Kind:      CandDeadlock,
			Key:       dl.Key,
			Verdict:   "reachable",
			StateSeq:  dl.StateSeq,
			Path:      res.PathTo(dl.StateSeq),
			Deadlock:  &dl,
			Reason:    "terminal-no-enabled-transition",
			DependsOn: []string{AspectInitialTokens, AspectArcWeights, AspectNodeIdentity, AspectCapacities, AspectExplorationStrategy, AspectTruncationBudget},
		})
	}

	// Cover candidates: for each omega discovery, record cover relations.
	for _, cp := range cov.CoverPairs {
		run.Candidates = append(run.Candidates, Candidate{
			ID:            fmt.Sprintf("cand_cov_%s_%d_%d", it.ID[4:], cp.ParentSeq, cp.NodeSeq),
			Kind:          CandCover,
			Key:           cp.ChildKey,
			CoveredByKey:  cp.ParentKey,
			Verdict:       "covered",
			Reason:        cp.Reason,
			DependsOn:     []string{AspectInitialTokens, AspectArcWeights, AspectNodeIdentity, AspectCapacities},
		})
	}
	sort.SliceStable(run.Candidates, func(i, j int) bool {
		if run.Candidates[i].Kind != run.Candidates[j].Kind {
			return run.Candidates[i].Kind < run.Candidates[j].Kind
		}
		return run.Candidates[i].ID < run.Candidates[j].ID
	})
	run.ID = "run_" + hash(it.ID, res.Status, len(res.States), len(run.Candidates), cov.Status)
	return run, nil
}

// InvalidationScope returns candidate ids whose dependency set intersects
// the changed aspect set. Strategy/budget changes invalidate all
// reachability and deadlock candidates; topology changes hit everything.
func InvalidationScope(run *Run, changedAspects []string) []string {
	changed := map[string]bool{}
	for _, a := range changedAspects {
		changed[a] = true
	}
	var ids []string
	for _, c := range run.Candidates {
		for _, dep := range c.DependsOn {
			if changed[dep] {
				ids = append(ids, c.ID)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}
