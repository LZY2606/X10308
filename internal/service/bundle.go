package service

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strconv"

	"tflocal/internal/domain"
)

//go:embed bundle_files/*
var bundleFS embed.FS

// LoadBaseline reads the shipped offline case pack raw bytes.
func LoadBaseline() ([]byte, string, error) {
	b, err := bundleFS.ReadFile("bundle_files/case-v1-baseline.json")
	if err != nil {
		return nil, "", err
	}
	return b, "case-v1-baseline.json", nil
}

func LoadRevisionFile(name string) ([]byte, string, error) {
	b, err := bundleFS.ReadFile("bundle_files/" + name)
	if err != nil {
		return nil, "", err
	}
	return b, name, nil
}

// EnsureBaseline imports the shipped pack exactly once per database
// (idempotent thanks to immutable source ids).
func (s *Service) EnsureBaseline(ctx context.Context) (*ImportSummary, error) {
	raw, fn, err := LoadBaseline()
	if err != nil {
		return nil, err
	}
	return s.ImportDocument(ctx, raw, fn, "shipped_case_pack")
}

// Mutation describes one counterexample: exactly one direct source input is
// changed relative to the baseline. Raw bytes of the mutated document are
// imported as a new immutable source and explored on a dedicated branch.
type Mutation struct {
	Key          string `json:"key"`
	ScenarioID   string `json:"scenario_id"`
	Name         string `json:"name"`
	DirectSource string `json:"direct_source"`
	Description  string `json:"description"`
	Mutate       func(doc *domain.Document)
	Expectation  string `json:"expectation"`
}

func cloneDoc(b []byte) (*domain.Document, []byte, error) {
	var d domain.Document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, nil, err
	}
	out, err := json.MarshalIndent(&d, "", "  ")
	return &d, out, err
}

// Mutations returns the seven required counterexample families. Each mutates
// exactly one direct source component and is replayable from raw bytes.
func Mutations(baseline []byte) []Mutation {
	return []Mutation{
		{
			Key: "selfloop_noop", ScenarioID: "self_loop",
			Name: "自环：仅改一条输入 arc 权重 1->2", DirectSource: "arc_weight",
			Description: "只改动一条直接来源 arc 的权重：t_finish 额外需要 2 个 ready token；自环 ping 仍在原 marking 上原地触发，差异只体现在 finish 的 input enablement。",
			Expectation: "ping self-loop marking unchanged; finish input condition changes",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "self_loop")
				// arc-only change: require 2 tokens for finish; the single
				// initial token can never satisfy it, so the goal stays out of
				// reach while the ping self-loop remains unchanged.
				sc.Arcs = append(sc.Arcs,
					&domain.Arc{ID: "mut_self_w2_in", From: "p_ready", To: "t_finish", Kind: domain.ArcNormal, RawWeight: "2"})
			},
		},
		{
			Key: "concurrent_add_choice", ScenarioID: "concurrency",
			Name: "并发：增加一个并发 transition", DirectSource: "node_identity+arc",
			Description: "新增 t_c 第三条共享资源分支，产生新的交错序列；影响限制在 concurrency 情形。",
			Expectation: "new interleavings and deadlock candidate inside concurrency only",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "concurrency")
				sc.Places = append(sc.Places, &domain.Place{ID: "c_busy_c", Name: "busy-C",
					Coord: domain.Coord{X: 620, Y: 420, Unit: domain.UnitCoordinate, Basis: d.CoordBasis}})
				sc.Transitions = append(sc.Transitions, &domain.Transition{ID: "t_c", Name: "acquire-C",
					Coord: domain.Coord{X: 480, Y: 420, Unit: domain.UnitCoordinate, Basis: d.CoordBasis}})
				sc.Arcs = append(sc.Arcs,
					&domain.Arc{ID: "mut_ca1", From: "c_res", To: "t_c", Kind: domain.ArcNormal, RawWeight: "1"},
					&domain.Arc{ID: "mut_ca2", From: "c_idle", To: "t_c", Kind: domain.ArcNormal, RawWeight: "1"},
					&domain.Arc{ID: "mut_ca3", From: "t_c", To: "c_busy_c", Kind: domain.ArcNormal, RawWeight: "1"})
			},
		},
		{
			Key: "capacity_shrink", ScenarioID: "capacity_boundary",
			Name: "容量边界：cap 2 -> 1", DirectSource: "capacity",
			Description: "只把 k_buf 容量从 2 改为 1；put 的容量 enablement 更早失败，影响限于本情形。",
			Expectation: "capacity condition changes reachable markings",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "capacity_boundary")
				for _, p := range sc.Places {
					if p.ID == "k_buf" && p.Capacity != nil {
						p.Capacity.Raw = "1"
					}
				}
			},
		},
		{
			Key: "inhibitor_remove", ScenarioID: "inhibitor_arc",
			Name: "抑制 arc：删除抑制弧", DirectSource: "arc",
			Description: "删除 i_lock -> t_use 的抑制弧后，被锁住的 pending 也能被 use 消费，死锁候选消失。",
			Expectation: "deadlock removed when inhibitor condition disabled",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "inhibitor_arc")
				out := sc.Arcs[:0]
				for _, a := range sc.Arcs {
					if a.ID != "ih1" {
						out = append(out, a)
					}
				}
				sc.Arcs = out
			},
		},
		{
			Key: "unbounded_cap", ScenarioID: "unbounded_growth",
			Name: "无界增长：给 u_net 加容量", DirectSource: "capacity",
			Description: "给 u_net 设置容量 3，Karp-Miller 不再出现 ω，覆盖关系消失，影响仅在该情形。",
			Expectation: "omega disappears; bounded reachability replaces coverability",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "unbounded_growth")
				for _, p := range sc.Places {
					if p.ID == "u_net" {
						p.Capacity = &domain.RawCapacity{PlaceID: "u_net", Raw: "3", Unit: domain.UnitCapacity}
					}
				}
			},
		},
		{
			Key: "samegraph_remark", ScenarioID: "samegraph_a",
			Name: "相同图不同 marking：只改初始 token", DirectSource: "initial_token",
			Description: "节点与 arc 完全不动，只把 samegraph_a 的 sg_in 初始 token 从 0 改为 2，结论从不可达变为可达。",
			Expectation: "identical graph, changed marking => distinct canonical states",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "samegraph_a")
				for _, rt := range sc.Tokens {
					if rt.PlaceID == "sg_in" {
						rt.Raw = "2"
					}
				}
			},
		},
		{
			Key: "budget_override_zero", ScenarioID: "budget_cut",
			Name: "预算截断：override 12 -> 4", DirectSource: "truncation_budget",
			Description: "只降低场景级截断预算；结论更早变为 unknown，被保留的 frontier 更小。",
			Expectation: "unknown with retained frontier, no false conclusion",
			Mutate: func(d *domain.Document) {
				sc := findScenario(d, "budget_cut")
				sc.BudgetOverride = 4
			},
		},
	}
}

func findScenario(d *domain.Document, id string) *domain.Scenario {
	for _, sc := range d.Scenarios {
		if sc.ID == id {
			return sc
		}
	}
	return nil
}

// CounterexampleRecord is a persisted replay unit: baseline and mutated raw
// documents, branches, outcome comparison and the final adoption decision.
type CounterexampleRecord struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	DirectSource  string `json:"direct_source"`
	Description   string `json:"description"`
	MutBranch     string `json:"mut_branch"`
	BaselineOutcome string `json:"baseline_outcome"`
	MutOutcome    string `json:"mut_outcome"`
	Adopted       bool   `json:"adopted"`
	Decision      string `json:"decision"`
	Expectation   string `json:"expectation"`
	ScenarioID    string `json:"scenario_id"`
}

// BuildCounterexamples creates one branch per mutation family. Both accepted
// and rejected outcomes are kept on file as candidates/decisions.
func (s *Service) BuildCounterexamples(ctx context.Context, baselineBranch string, adoptFirst bool) ([]CounterexampleRecord, error) {
	raw, _, err := LoadBaseline()
	if err != nil {
		return nil, err
	}
	muts := Mutations(raw)
	var recs []CounterexampleRecord
	for i, m := range muts {
		var d domain.Document
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		m.Mutate(&d)
		d.Revision = "cex-" + m.Key
		d.Title = "反例：" + m.Name
		mb, err := json.MarshalIndent(&d, "", "  ")
		if err != nil {
			return nil, err
		}
		br, err := s.ReviseSource(ctx, baselineBranch, mb, "counterexample:"+m.Key+" ("+m.DirectSource+")")
		if err != nil {
			return nil, err
		}
		rec := CounterexampleRecord{Key: m.Key, Name: m.Name, DirectSource: m.DirectSource,
			Description: m.Description, MutBranch: br, ScenarioID: m.ScenarioID, Expectation: m.Expectation}
		baseR, _, err := s.RunResult(ctx, baselineBranch, m.ScenarioID, "exact")
		if err == nil {
			rec.BaselineOutcome = string(baseR.Outcome)
		}
		mr, _, err := s.RunResult(ctx, br, m.ScenarioID, "exact")
		if err == nil {
			rec.MutOutcome = string(mr.Outcome)
		}
		// mark a reachability candidate accepted/rejected to show both verdicts
		cands, err := s.ListCandidates(ctx, br)
		if err != nil {
			return nil, err
		}
		var reachID string
		for _, c := range cands {
			if c.Scenario == m.ScenarioID && c.Kind == "reachability" {
				reachID = c.CandidateID
				break
			}
		}
		if reachID != "" {
			accept := adoptFirst && i == 0
			decision := "rejected"
			if accept {
				decision = "accepted"
			}
			if _, err := s.Decide(ctx, br, reachID, decision, "counterexample-archive", 0); err != nil {
				return nil, err
			}
			rec.Decision, rec.Adopted = decision, accept
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

var _ = strconv.Itoa
var _ = fmt.Sprintf
