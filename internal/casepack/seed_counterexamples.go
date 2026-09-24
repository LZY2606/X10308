package casepack

import (
	"fmt"

	"petriws/internal/petri"
	"petriws/internal/store"
)

// cexDef defines one counterexample: a versioned baseline plus exactly
// one changed direct source.
type cexDef struct {
	ID          string
	Name        string
	Scenario    string
	Description string
	Base        *rawModel
	Variant     *rawModel // nil => same model, parameter change only
	BaseSnap    func(net *petri.Net) *Snapshot
	VariantSnap func(net *petri.Net) *Snapshot
	ChangedKind string // which direct source changed
	ChangedDesc string
	Judgment    string // accepted|rejected
	Rationale   string
}

func small(places []rawPlace, trans []rawTrans, arcs []rawArc, init []rawInit) *rawModel {
	return &rawModel{Places: places, Transitions: trans, Arcs: arcs, Initial: init}
}

func snap(budget int, strategy string) func(*petri.Net) *Snapshot {
	return func(net *petri.Net) *Snapshot {
		return &Snapshot{Net: net, Strategy: strategy, Budget: budget, RuleVersion: 2}
	}
}

func cexDefs() []cexDef {
	p := func(id string, cap string, x, y int) rawPlace {
		return rawPlace{ID: id, Name: id, Capacity: cap, X: x, Y: y}
	}
	tr := func(id string, x, y int) rawTrans { return rawTrans{ID: id, Name: id, X: x, Y: y} }
	a := func(id, from, to, kind string, w int64) rawArc {
		return rawArc{ID: id, From: from, To: to, Kind: kind, Weight: tok(w)}
	}
	i1 := func(place string, n int64) rawInit { return rawInit{Place: place, Tokens: tok(n)} }

	selfLoop := small(
		[]rawPlace{p("p1", tok(1), 80, 80)},
		[]rawTrans{tr("t1", 200, 80)},
		[]rawArc{a("a1", "p1", "t1", "normal", 1), a("a2", "t1", "p1", "normal", 1)},
		[]rawInit{i1("p1", 1)},
	)
	selfLoopV := small(
		[]rawPlace{p("p1", tok(1), 80, 80)},
		[]rawTrans{tr("t1", 200, 80)},
		[]rawArc{a("a1", "p1", "t1", "normal", 1), a("a2", "t1", "p1", "normal", 1)},
		[]rawInit{i1("p1", 0)},
	)
	concurrent := func(initTokens int64) *rawModel {
		return small(
			[]rawPlace{p("p1", "unbounded", 80, 80), p("p2", "unbounded", 320, 40), p("p3", "unbounded", 320, 130)},
			[]rawTrans{tr("t1", 200, 40), tr("t2", 200, 130)},
			[]rawArc{
				a("a1", "p1", "t1", "normal", 1), a("a2", "t1", "p2", "normal", 1),
				a("a3", "p1", "t2", "normal", 1), a("a4", "t2", "p3", "normal", 1),
			},
			[]rawInit{i1("p1", initTokens)},
		)
	}
	capBoundary := func(cap string) *rawModel {
		return small(
			[]rawPlace{p("p1", cap, 80, 80)},
			[]rawTrans{tr("t1", 200, 80)},
			[]rawArc{a("a1", "t1", "p1", "normal", 1)},
			[]rawInit{i1("p1", 0)},
		)
	}
	inhibitor := func(threshold int64) *rawModel {
		return small(
			[]rawPlace{p("p1", "unbounded", 80, 60), p("p2", tok(1), 80, 160)},
			[]rawTrans{tr("t1", 220, 60), tr("t2", 220, 160)},
			[]rawArc{
				a("a1", "p1", "t1", "normal", 1),
				a("a2", "p2", "t1", "inhibitor", threshold),
				a("a3", "t2", "p2", "normal", 1),
			},
			[]rawInit{i1("p1", 1)},
		)
	}
	unbounded := small(
		[]rawPlace{p("p1", "unbounded", 80, 80)},
		[]rawTrans{tr("t1", 200, 80)},
		[]rawArc{a("a1", "t1", "p1", "normal", 1)},
		[]rawInit{i1("p1", 0)},
	)
	mover := func(init []rawInit) *rawModel {
		return small(
			[]rawPlace{p("p1", "unbounded", 80, 80), p("p2", "unbounded", 320, 80)},
			[]rawTrans{tr("t1", 200, 80)},
			[]rawArc{a("a1", "p1", "t1", "normal", 1), a("a2", "t1", "p2", "normal", 1)},
			init,
		)
	}

	return []cexDef{
		{
			ID: "cex-selfloop", Name: "自环", Scenario: "self-loop",
			Description: "t1 消耗并归还 p1 的 token：状态不变但要求 token 在场",
			Base:        selfLoop, Variant: selfLoopV,
			BaseSnap: snap(16, "bfs"), VariantSnap: snap(16, "bfs"),
			ChangedKind: "marking", ChangedDesc: "初始 token p1: 1 → 0",
			Judgment: "accepted", Rationale: "变体中 t1 因输入不足立即死锁，影响限于输入条件",
		},
		{
			ID: "cex-concurrent", Name: "并发 transition", Scenario: "concurrent-transitions",
			Description: "t1 与 t2 竞争同一输入库所 p1",
			Base:        concurrent(2), Variant: concurrent(1),
			BaseSnap: snap(32, "bfs"), VariantSnap: snap(32, "bfs"),
			ChangedKind: "marking", ChangedDesc: "初始 token p1: 2 → 1",
			Judgment: "rejected", Rationale: "变体行为差异被误判为缺陷；实为 token 减少的正常结果，不予采纳",
		},
		{
			ID: "cex-capacity", Name: "容量边界", Scenario: "capacity-boundary",
			Description: "生产者 t1 在容量 2 处被阻塞",
			Base:        capBoundary(tok(2)), Variant: capBoundary(tok(3)),
			BaseSnap: snap(16, "bfs"), VariantSnap: snap(16, "bfs"),
			ChangedKind: "capacities", ChangedDesc: "容量 p1: 2 → 3 token",
			Judgment: "accepted", Rationale: "容量 +1 精确增加一个可达状态，边界行为正确",
		},
		{
			ID: "cex-inhibitor", Name: "抑制弧", Scenario: "inhibitor-arc",
			Description: "p2 达到阈值时 t1 被抑制",
			Base:        inhibitor(1), Variant: inhibitor(2),
			BaseSnap: snap(16, "bfs"), VariantSnap: snap(16, "bfs"),
			ChangedKind: "weights", ChangedDesc: "抑制弧阈值 1 → 2 token",
			Judgment: "accepted", Rationale: "阈值 2 在容量 1 下永不触发，抑制影响被正确定位",
		},
		{
			ID: "cex-unbounded", Name: "无界增长", Scenario: "unbounded-growth",
			Description: "无容量生产者：状态空间无限，预算截断返回 unknown 并保留 frontier",
			Base:        unbounded, Variant: nil,
			BaseSnap: snap(4, "bfs"), VariantSnap: snap(8, "bfs"),
			ChangedKind: "budget", ChangedDesc: "截断预算 4 → 8 states",
			Judgment: "accepted", Rationale: "两种预算均为 unknown 且 frontier 完整保留，覆盖关系正确标记增长",
		},
		{
			ID: "cex-marking", Name: "相同图不同 marking", Scenario: "same-graph-different-marking",
			Description: "同一拓扑、不同初始 token：canonicalization 不合并 token 不同的 marking",
			Base:        mover([]rawInit{i1("p1", 1)}), Variant: mover([]rawInit{i1("p2", 1)}),
			BaseSnap: snap(16, "bfs"), VariantSnap: snap(16, "bfs"),
			ChangedKind: "marking", ChangedDesc: "初始标识 (p1=1,p2=0) → (p1=0,p2=1)",
			Judgment: "rejected", Rationale: "曾建议合并对称状态以压缩空间；违反 canonicalization 规则，不予采纳",
		},
		{
			ID: "cex-truncation", Name: "预算截断", Scenario: "budget-truncation",
			Description: "预算 1 时 BFS 与 DFS 的 frontier 顺序不同，均返回 unknown",
			Base:        concurrent(2), Variant: nil,
			BaseSnap: snap(1, "bfs"), VariantSnap: snap(1, "dfs"),
			ChangedKind: "strategy", ChangedDesc: "探索策略 bfs → dfs",
			Judgment: "accepted", Rationale: "策略只改变 frontier 顺序，不改变 unknown 结论",
		},
	}
}

// seedCounterexamples builds the archive: each entry stores the versioned
// baseline, changes exactly one direct source, and keeps both accepted
// and rejected judgments as machine-readable events.
func seedCounterexamples(c *seedCtx) error {
	if err := store.CreateBranch(c.tx, &store.Branch{
		ID: "br-archive", Name: "反例档案", ModelVersionID: "mv-1",
		RuleVersion: 2, CreatedAt: ts(),
	}); err != nil {
		return err
	}
	var last int64
	for _, d := range cexDefs() {
		if err := seedOneCounterexample(c, d, &last); err != nil {
			return err
		}
	}
	return store.SetBranchHead(c.tx, "br-archive", last)
}

func seedOneCounterexample(c *seedCtx, d cexDef, last *int64) error {
	build := func(m *rawModel, suffix, label string, seq int, parent string, sn *Snapshot) (string, *petri.Net, []string, error) {
		srcModel := fmt.Sprintf("src-%s-model-%s", d.ID, suffix)
		if err := c.source(srcModel, "model", d.Name+" 模型 "+label, rawJSON(m), "token", "px"); err != nil {
			return "", nil, nil, err
		}
		srcChanged := fmt.Sprintf("src-%s-%s", d.ID, suffix)
		if err := c.source(srcChanged, d.ChangedKind, d.Name+" "+label+"："+d.ChangedDesc,
			rawJSON(map[string]string{"change": d.ChangedDesc}), "token", ""); err != nil {
			return "", nil, nil, err
		}
		net, diags, err := petri.Normalize(toDoc(m))
		if err != nil {
			return "", nil, nil, err
		}
		for _, dg := range diags {
			if err := store.CreateDiagnostic(c.tx, &store.DiagnosticRow{
				SourceID: srcModel, Message: dg.Message, RawValue: dg.RawValue, CreatedAt: ts(),
			}); err != nil {
				return "", nil, nil, err
			}
		}
		sn.Net = net
		mvID := fmt.Sprintf("mv-%s-%s", d.ID, suffix)
		srcs := []string{srcModel, srcChanged}
		if err := c.modelVersion(mvID, seq, parent, d.Name+" "+label, sn, srcs); err != nil {
			return "", nil, nil, err
		}
		return mvID, net, srcs, nil
	}

	variantModel := d.Variant
	if variantModel == nil {
		variantModel = d.Base
	}
	baseSnap := d.BaseSnap(nil)
	varSnap := d.VariantSnap(nil)

	mvB, _, srcsB, err := build(d.Base, "base", "基线", 100, "", baseSnap)
	if err != nil {
		return err
	}
	mvV, _, srcsV, err := build(variantModel, "variant", "变体", 101, mvB, varSnap)
	if err != nil {
		return err
	}

	runB := fmt.Sprintf("run-%s-base", d.ID)
	resB, err := c.run(runB, "br-archive", mvB, baseSnap, srcsB)
	if err != nil {
		return err
	}
	runV := fmt.Sprintf("run-%s-variant", d.ID)
	resV, err := c.run(runV, "br-archive", mvV, varSnap, srcsV)
	if err != nil {
		return err
	}

	if _, err := c.event("br-archive", "run", map[string]any{
		"run_id": runB, "scenario": d.Scenario, "role": "baseline",
		"status": resB.Status, "states": len(resB.States), "frontier": resB.Frontier,
	}, mvB, 2); err != nil {
		return err
	}
	if _, err := c.event("br-archive", "run", map[string]any{
		"run_id": runV, "scenario": d.Scenario, "role": "variant",
		"status": resV.Status, "states": len(resV.States), "frontier": resV.Frontier,
		"changed_source": d.ChangedKind, "change": d.ChangedDesc,
	}, mvV, 2); err != nil {
		return err
	}

	if err := store.CreateCounterexample(c.tx, &store.Counterexample{
		ID: d.ID, Name: d.Name, Scenario: d.Scenario, Description: d.Description,
		BranchID: "br-archive", BaselineRunID: runB, VariantRunID: runV,
		ChangedSourceID: fmt.Sprintf("src-%s-variant", d.ID),
		Judgment:        d.Judgment, CreatedAt: ts(),
	}); err != nil {
		return err
	}
	seq, err := c.event("br-archive", "ruling", map[string]any{
		"counterexample_id": d.ID, "decision": d.Judgment,
		"rationale": d.Rationale,
		"basis":     "transition enablement 同时考虑输入、抑制和容量；canonicalization 不合并 token 不同的 marking；预算耗尽返回 unknown 并保留 frontier",
	}, mvV, 2)
	if err != nil {
		return err
	}
	*last = seq
	return nil
}
