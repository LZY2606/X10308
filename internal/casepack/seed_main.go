package casepack

import (
	"petriws/internal/petri"
	"petriws/internal/store"
)

// seedMainBranch builds the main narrative on branch br-main:
//  1. versioned baseline import (model, identities, weights, tokens,
//     capacities, strategy, budget)
//  2. one parameter revision (budget 64 -> 5) with computed invalidation
//  3. one local source change (arc weight a_in_load 1 -> 2)
//  4. two conflicting human rulings on the same deadlock candidate
func seedMainBranch(c *seedCtx) error {
	// --- 1. baseline sources, raw representation kept with units ---
	doc := mainModel(1)
	if err := c.source("src-model-v1", "model", "令牌流网 结构 v1", rawJSON(doc), "token", "px"); err != nil {
		return err
	}
	weights := map[string]string{}
	for _, a := range doc.Arcs {
		weights[a.ID] = a.Weight
	}
	if err := c.source("src-weights-v1", "weights", "弧权重 v1", rawJSON(weights), "token", ""); err != nil {
		return err
	}
	if err := c.source("src-marking-v1", "marking", "初始标识 v1", rawJSON(doc.Initial), "token", ""); err != nil {
		return err
	}
	caps := map[string]string{}
	for _, p := range doc.Places {
		caps[p.ID] = p.Capacity
	}
	if err := c.source("src-capacities-v1", "capacities", "容量约束 v1", rawJSON(caps), "token", ""); err != nil {
		return err
	}
	if err := c.source("src-strategy-v1", "strategy", "探索策略 v1", rawJSON(map[string]string{"strategy": "bfs"}), "", ""); err != nil {
		return err
	}
	if err := c.source("src-budget-v1", "budget", "截断预算 v1", rawJSON(map[string]any{"budget": 64, "unit": "states"}), "states", ""); err != nil {
		return err
	}

	// Derived layer: normalize. p_done capacity "unknown" stays raw and
	// gets a diagnostic; it is treated as unbounded, never dropped.
	net, diags, err := petri.Normalize(toDoc(doc))
	if err != nil {
		return err
	}
	for _, d := range diags {
		if err := store.CreateDiagnostic(c.tx, &store.DiagnosticRow{
			SourceID: "src-model-v1", Message: d.Message, RawValue: d.RawValue, CreatedAt: ts(),
		}); err != nil {
			return err
		}
	}

	baseSrcs := []string{"src-model-v1", "src-weights-v1", "src-marking-v1",
		"src-capacities-v1", "src-strategy-v1", "src-budget-v1"}
	snap1 := &Snapshot{Net: net, Strategy: "bfs", Budget: 64, RuleVersion: 2}
	if err := c.modelVersion("mv-1", 1, "", "基线：全部参数版本化", snap1, baseSrcs); err != nil {
		return err
	}
	if err := store.CreateBranch(c.tx, &store.Branch{
		ID: "br-main", Name: "主分支：令牌流网复核", ModelVersionID: "mv-1",
		RuleVersion: 2, CreatedAt: ts(),
	}); err != nil {
		return err
	}
	if _, err := c.event("br-main", "import", map[string]any{
		"summary":  "初始导入：原模型、节点身份、弧权重、初始 token、容量、探索策略、截断预算全部版本化",
		"sources":  baseSrcs,
		"model":    "mv-1",
		"strategy": "bfs", "budget": 64, "rule_version": 2,
	}, "mv-1", 2); err != nil {
		return err
	}
	for _, d := range diags {
		if _, err := c.event("br-main", "diagnostic", map[string]any{
			"field": d.Field, "raw_value": d.RawValue, "message": d.Message,
			"policy": "无法转换的值留在原位置并附诊断，不做不可逆清洗",
		}, "mv-1", 2); err != nil {
			return err
		}
	}

	// Baseline run.
	res1, err := c.run("run-main-1", "br-main", "mv-1", snap1, baseSrcs)
	if err != nil {
		return err
	}
	if _, err := c.event("br-main", "run", map[string]any{
		"run_id": "run-main-1", "status": res1.Status, "states": len(res1.States),
	}, "mv-1", 2); err != nil {
		return err
	}

	// Deadlock candidates: every legal option is stored explicitly.
	var deadlocks []string
	for _, st := range res1.States {
		if st.Deadlock {
			deadlocks = append(deadlocks, st.ID)
		}
	}
	candIDs := []string{}
	for i, sid := range deadlocks {
		cid := "cand-deadlock-" + sid
		candIDs = append(candIDs, cid)
		st := res1.StateByID(sid)
		if err := store.CreateCandidate(c.tx, &store.Candidate{
			ID: cid, BranchID: "br-main", RunID: "run-main-1", Kind: "deadlock",
			PayloadJSON: string(rawJSON(map[string]any{
				"state_id": sid, "marking": st.Marking, "reason": st.Reason,
				"path": res1.Path(sid),
				"rank": i + 1, "of": len(deadlocks),
			})),
			CreatedAt: ts(),
		}); err != nil {
			return err
		}
	}
	if _, err := c.event("br-main", "candidates", map[string]any{
		"summary": "死锁候选全部保留，等待人工裁定",
		"ids":     candIDs,
	}, "mv-1", 2); err != nil {
		return err
	}

	// --- 2. parameter revision: budget 64 -> 5, invalidation computed ---
	if err := c.source("src-budget-v2", "budget", "截断预算 v2", rawJSON(map[string]any{"budget": 5, "unit": "states"}), "states", ""); err != nil {
		return err
	}
	snap2 := &Snapshot{Net: net, Strategy: "bfs", Budget: 5, RuleVersion: 2}
	srcs2 := []string{"src-model-v1", "src-weights-v1", "src-marking-v1",
		"src-capacities-v1", "src-strategy-v1", "src-budget-v2"}
	if err := c.modelVersion("mv-2", 2, "mv-1", "参数修订：截断预算 64→5", snap2, srcs2); err != nil {
		return err
	}
	if _, err := c.event("br-main", "version", map[string]any{
		"summary": "参数修订：截断预算 64 → 5（unit: states）",
		"field":   "budget", "from": 64, "to": 5, "model": "mv-2",
	}, "mv-2", 2); err != nil {
		return err
	}
	// Invalidation scope from dependencies, never a global silent replace.
	scope := []string{"run-main-1"}
	if _, err := c.event("br-main", "invalidate", map[string]any{
		"summary":  "预算源 src-budget-v1 被 v2 取代：按依赖关系计算失效范围",
		"source":   "src-budget-v1",
		"affected": scope,
	}, "mv-2", 2); err != nil {
		return err
	}
	res2, err := c.run("run-main-2", "br-main", "mv-2", snap2, srcs2)
	if err != nil {
		return err
	}
	if _, err := c.event("br-main", "run", map[string]any{
		"run_id": "run-main-2", "status": res2.Status, "states": len(res2.States),
		"frontier": res2.Frontier,
		"note":     "预算耗尽返回 unknown 并保留 frontier",
	}, "mv-2", 2); err != nil {
		return err
	}
	if err := store.CreateCandidate(c.tx, &store.Candidate{
		ID: "cand-frontier-run-main-2", BranchID: "br-main", RunID: "run-main-2",
		Kind: "frontier",
		PayloadJSON: string(rawJSON(map[string]any{
			"frontier": res2.Frontier, "budget": 5,
			"meaning": "截断时的未扩展状态集合，结论为 unknown",
		})),
		CreatedAt: ts(),
	}); err != nil {
		return err
	}

	// --- 3. local source change: arc weight a_in_load 1 -> 2 ---
	doc2 := mainModel(2)
	weights2 := map[string]string{}
	for _, a := range doc2.Arcs {
		weights2[a.ID] = a.Weight
	}
	if err := c.source("src-weights-v2", "weights", "弧权重 v2", rawJSON(weights2), "token", ""); err != nil {
		return err
	}
	net2, _, err := petri.Normalize(toDoc(doc2))
	if err != nil {
		return err
	}
	snap3 := &Snapshot{Net: net2, Strategy: "bfs", Budget: 64, RuleVersion: 2}
	srcs3 := []string{"src-model-v1", "src-weights-v2", "src-marking-v1",
		"src-capacities-v1", "src-strategy-v1", "src-budget-v1"}
	if err := c.modelVersion("mv-3", 3, "mv-2", "局部源改动：a_in_load 权重 1→2 token", snap3, srcs3); err != nil {
		return err
	}
	if _, err := c.event("br-main", "version", map[string]any{
		"summary": "局部源数据改动：弧 a_in_load 权重 1 → 2 token",
		"field":   "arc.a_in_load.weight", "from": "1 token", "to": "2 token", "model": "mv-3",
	}, "mv-3", 2); err != nil {
		return err
	}
	if _, err := c.event("br-main", "invalidate", map[string]any{
		"summary":  "权重源 src-weights-v1 被 v2 取代：失效范围仅限依赖该源的推导",
		"source":   "src-weights-v1",
		"affected": []string{"run-main-1", "run-main-2"},
	}, "mv-3", 2); err != nil {
		return err
	}
	res3, err := c.run("run-main-3", "br-main", "mv-3", snap3, srcs3)
	if err != nil {
		return err
	}
	if _, err := c.event("br-main", "run", map[string]any{
		"run_id": "run-main-3", "status": res3.Status, "states": len(res3.States),
	}, "mv-3", 2); err != nil {
		return err
	}

	// --- 4. conflicting human rulings on the same candidate ---
	target := candIDs[0]
	r1, err := c.event("br-main", "ruling", map[string]any{
		"candidate_id": target, "decision": "accepted", "reviewer": "审阅者甲",
		"rationale": "该死锁由抑制弧与容量共同造成，符合预期工艺语义",
	}, "mv-1", 2)
	if err != nil {
		return err
	}
	if err := store.SetCandidateStatus(c.tx, target, "accepted", "审阅者甲采纳"); err != nil {
		return err
	}
	r2, err := c.event("br-main", "ruling", map[string]any{
		"candidate_id": target, "decision": "rejected", "reviewer": "审阅者乙",
		"rationale":      "告警后不应永久阻塞装载，判定为建模缺陷",
		"conflicts_with": r1,
	}, "mv-1", 2)
	if err != nil {
		return err
	}
	if err := store.SetCandidateStatus(c.tx, target, "rejected", "审阅者乙否定（与审阅者甲冲突，两条均保留）"); err != nil {
		return err
	}
	// Head of br-main is the last event.
	return store.SetBranchHead(c.tx, "br-main", r2)
}
