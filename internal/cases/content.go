package cases

import (
	"encoding/json"

	"pnwork/internal/service"
)

func q(id, branch, kind string, goal map[string]int64, label string) service.Question {
	return service.Question{ID: id, BranchID: branch, Kind: kind, Goal: goal, Label: label}
}

func questions() []service.Question {
	return []service.Question{
		q("q-concurrency", BranchBaseline, "reachability",
			map[string]int64{"p_ready": 0, "p_pool": 3, "p_buffer": 2, "p_loopctl": 1},
			"并发 transition：两次 t_acquire 到达满缓冲区"),
		q("q-unbounded", BranchBaseline, "coverability",
			map[string]int64{"p_pool": 8, "p_loopctl": 1},
			"无界增长：t_inject 把 p_pool 推到 8，基线预算内可达"),
		q("q-cap-boundary", BranchBaseline, "coverability",
			map[string]int64{"p_buffer": 3, "p_loopctl": 1},
			"容量边界：p_buffer 容量 2，token=3 不可达（覆盖也失败）"),
		q("q-inhibitor", BranchBaseline, "coverability",
			map[string]int64{"p_guard": 1, "p_pool": 9, "p_loopctl": 1},
			"抑制 arc：设置哨兵后 t_inject 必须停止"),
		q("q-deadlock", BranchDecisionA, "enumerate",
			nil,
			"同图不同 marking：枚举 m1 直接得到 deadlock 候选与每 transition 阻塞原因"),
		q("q-enumerate", BranchBaseline, "enumerate",
			map[string]int64{"p_pool": 5, "p_loopctl": 1},
			"状态空间枚举：覆盖关系、死锁原因与有界截断 frontier"),
		q("q-truncated", BranchParamRev, "coverability",
			map[string]int64{"p_pool": 8, "p_loopctl": 1},
			"预算截断：DFS 预算 6 下无界目标返回 unknown 并保留 frontier"),
	}
}

func scenarios() []service.Scenario {
	return []service.Scenario{
		{ID: "sc-self-loop", Name: "自环", BranchID: BranchBaseline,
			Expectation: "t_selfloop 读/写 p_loopctl，是始终使能的自环；状态不被错误合并"},
		{ID: "sc-concurrent", Name: "并发 transition", BranchID: BranchBaseline,
			Expectation: "初始 p_ready=2,p_pool=3 时两次 t_acquire 产生多个独立后继与多条候选路径"},
		{ID: "sc-capacity", Name: "容量边界", BranchID: BranchBaseline,
			Expectation: "p_buffer 容量 2：到达 2 后 t_acquire 以 capacity_full 阻塞"},
		{ID: "sc-inhibitor", Name: "抑制 arc", BranchID: BranchBaseline,
			Expectation: "p_guard>=1 时 t_inject 以 inhibited 阻塞，且 enablement 同时检查输入/容量"},
		{ID: "sc-unbounded", Name: "无界增长", BranchID: BranchBaseline,
			Expectation: "p_pool 无界，t_inject 可不断注入；覆盖 p_pool=8 在预算内成立"},
		{ID: "sc-same-graph-diff-marking", Name: "相同图不同 marking", BranchID: BranchDecisionA,
			Expectation: "图结构完全相同，仅初始 marking 改变即产生死锁候选，canonical key 不同"},
		{ID: "sc-budget-truncation", Name: "预算截断", BranchID: BranchParamRev,
			Expectation: "DFS 预算 6 耗尽时 verdict=unknown，frontier 非空并被保留"},
	}
}

func narrative() service.Narrative {
	return service.Narrative{
		Title: "流程建模小组的可达性与死锁复核",
		Steps: []service.NarrativeStep{
			{Order: 1, Title: "导入并核对七要素", BranchID: BranchBaseline,
				Detail:      "导入 place、transition、加权 arc、初始 marking、容量；核对原模型/身份/权重/初始 token/容量/策略/预算均版本化，原始表示与单位保留。",
				ScenarioIDs: []string{"sc-self-loop"}},
			{Order: 2, Title: "触发边界组合", BranchID: BranchBaseline,
				Detail:      "依次重放并发、容量边界、抑制 arc、无界增长；页面记录输入片段、判定状态与受影响对象。",
				ScenarioIDs: []string{"sc-concurrent", "sc-capacity", "sc-inhibitor", "sc-unbounded"}},
			{Order: 3, Title: "参数修订与截断", BranchID: BranchParamRev,
				Detail:      "改为 DFS 并把预算降到 6，深层无界目标变成 unknown，frontier 保留；失效范围仅限引用 strategy/budget 的运行。",
				ScenarioIDs: []string{"sc-budget-truncation"}},
			{Order: 4, Title: "局部依赖改动", BranchID: BranchLocalDep,
				Detail:      "只把 p_buffer 容量从 2 改成 1，验证失效范围只命中 capacity 依赖，不做全局替换。",
				ScenarioIDs: []string{"sc-capacity"}},
			{Order: 5, Title: "相同图不同 marking 与冲突裁定", BranchID: BranchDecisionA,
				Detail:      "切到同一图的死锁 marking，保留全部合法候选；A 窗口接受、B 窗口否定同一候选，后提交者看到版本冲突与双方差异。",
				ScenarioIDs: []string{"sc-same-graph-diff-marking"}},
		},
	}
}

func ev(seq int64, id, branch, typ string, payload any, deps []string, rev int64, undone bool) service.BundleEvent {
	b, _ := json.Marshal(payload)
	return service.BundleEvent{Seq: seq, EventID: id, BranchID: branch, Type: typ,
		Payload: b, DependsOn: deps, Rev: rev, Undone: undone}
}

func seedEvents() []service.BundleEvent {
	const rules = "rule@rules.tokenflow.v1"
	deps := func(extra ...string) []string {
		out := append([]string{rules}, extra...)
		return out
	}
	var out []service.BundleEvent
	seq := int64(1)

	// Baseline branch creation.
	basePins := []map[string]any{
		{"aspect": "model", "version": 1}, {"aspect": "identity", "version": 1},
		{"aspect": "weights", "version": 1}, {"aspect": "initial_tokens", "version": 1},
		{"aspect": "capacity", "version": 1}, {"aspect": "strategy", "version": 1},
		{"aspect": "budget", "version": 1},
	}
	out = append(out, ev(seq, "ev_"+BranchBaseline, BranchBaseline, service.EvBranchCreated,
		map[string]any{"branch_id": BranchBaseline, "kind": "baseline", "name": "基线", "pins": basePins},
		deps(), 1, false))
	seq++
	for _, a := range []string{"model", "identity", "weights", "initial_tokens", "capacity", "strategy", "budget"} {
		out = append(out, ev(seq, "ev_aspect_"+a+"_baseline", BranchBaseline, service.EvAspectImported,
			map[string]any{"aspect": a, "version": 1, "source_id": "src-" + sourceSlug(a),
				"note": "baseline versioned import"},
			deps(a+"@v1"), 1, false))
		seq++
	}
	exEv := func(branch, exid, question string, goal map[string]int64, verdict string, candIDs []string, depVers map[string]int, rev int64) {
		d := deps()
		for a, v := range depVers {
			d = append(d, a+"@v"+json.Number(jsonNum(v)).String())
		}
		out = append(out, ev(seq, "ev_"+exid, branch, service.EvExploration,
			map[string]any{
				"exploration_id": exid, "question": question, "goal": goal,
				"verdict": verdict, "candidate_ids": candIDs,
			}, d, rev, false))
		seq++
	}
	baseV := map[string]int{"model": 1, "identity": 1, "weights": 1, "initial_tokens": 1,
		"capacity": 1, "strategy": 1, "budget": 1}

	exEv(BranchBaseline, ExConcurrency, "reachability",
		map[string]int64{"p_ready": 1, "p_pool": 3, "p_buffer": 1, "p_loopctl": 1},
		"reachable", []string{"cand_" + ExConcurrency + "_1", "cand_" + ExConcurrency + "_2"},
		baseV, 1)
	exEv(BranchBaseline, ExUnbounded, "coverability",
		map[string]int64{"p_pool": 8, "p_loopctl": 1}, "reachable",
		[]string{"cand_" + ExUnbounded + "_1"}, baseV, 1)
	exEv(BranchBaseline, ExCapBoundary, "coverability",
		map[string]int64{"p_buffer": 3, "p_loopctl": 1}, "unreachable", nil, baseV, 1)
	exEv(BranchBaseline, ExInhibitor, "coverability",
		map[string]int64{"p_guard": 1, "p_pool": 9, "p_loopctl": 1}, "reachable",
		[]string{"cand_" + ExInhibitor + "_1"}, baseV, 1)
	exEv(BranchBaseline, ExEnumerate, "enumerate",
		map[string]int64{"p_pool": 5, "p_loopctl": 1}, "complete", nil, baseV, 1)

	// Parameter revision branch (strategy v2, budget v2).
	pinRev := append([]map[string]any{}, basePins...)
	pinRev[5] = map[string]any{"aspect": "strategy", "version": 2}
	pinRev[6] = map[string]any{"aspect": "budget", "version": 2}
	out = append(out, ev(seq, "ev_"+BranchParamRev, BranchParamRev, service.EvBranchCreated,
		map[string]any{"branch_id": BranchParamRev, "parent_branch_id": BranchBaseline,
			"kind": "revision", "pins": pinRev}, deps(), 2, false))
	seq++
	out = append(out, ev(seq, "ev_aspect_strategy_v2", BranchParamRev, service.EvAspectImported,
		map[string]any{"aspect": "strategy", "version": 2, "scope": map[string]any{
			"changed_keys": []string{"strategy@v2"}, "explorations": []string{ExUnbounded, ExTruncated}}},
		deps("strategy@v2"), 2, false))
	seq++
	out = append(out, ev(seq, "ev_aspect_budget_v2", BranchParamRev, service.EvAspectImported,
		map[string]any{"aspect": "budget", "version": 2, "scope": map[string]any{
			"changed_keys": []string{"budget@v2"}}}, deps("budget@v2"), 2, false))
	seq++
	revV := map[string]int{"model": 1, "identity": 1, "weights": 1, "initial_tokens": 1,
		"capacity": 1, "strategy": 2, "budget": 2}
	exEv(BranchParamRev, ExTruncated, "coverability",
		map[string]int64{"p_pool": 8, "p_loopctl": 1}, "unknown", nil, revV, 2)

	// Local dependency branch (capacity v2).
	localPins := append([]map[string]any{}, pinRev...)
	localPins[4] = map[string]any{"aspect": "capacity", "version": 2}
	out = append(out, ev(seq, "ev_"+BranchLocalDep, BranchLocalDep, service.EvBranchCreated,
		map[string]any{"branch_id": BranchLocalDep, "parent_branch_id": BranchParamRev,
			"kind": "local_dep", "pins": localPins}, deps(), 3, false))
	seq++
	out = append(out, ev(seq, "ev_aspect_capacity_v2", BranchLocalDep, service.EvAspectImported,
		map[string]any{"aspect": "capacity", "version": 2, "scope": map[string]any{
			"changed_keys": []string{"capacity@v2"},
			"explorations": []string{ExCapBoundary, ExEnumerate, ExTruncated}}},
		deps("capacity@v2"), 3, false))
	seq++

	// Decision branches: same graph, different initial marking (v2).
	decPins := append([]map[string]any{}, basePins...)
	decPins[3] = map[string]any{"aspect": "initial_tokens", "version": 2}
	for _, bid := range []string{BranchDecisionA, BranchDecisionB} {
		rev := int64(4)
		if bid == BranchDecisionB {
			rev = 5
		}
		out = append(out, ev(seq, "ev_"+bid, bid, service.EvBranchCreated,
			map[string]any{"branch_id": bid, "parent_branch_id": BranchBaseline,
				"kind": "decision", "pins": decPins}, deps(), rev, false))
		seq++
		out = append(out, ev(seq, "ev_aspect_initial_tokens_v2_"+bid, bid, service.EvAspectImported,
			map[string]any{"aspect": "initial_tokens", "version": 2},
			deps("initial_tokens@v2"), rev, false))
		seq++
		decV := map[string]int{"model": 1, "identity": 1, "weights": 1, "initial_tokens": 2,
			"capacity": 1, "strategy": 1, "budget": 1}
		exEv(bid, ExDeadlock+"-"+bid, "enumerate",
			map[string]int64{},
			"deadlocked",
			[]string{"cand_" + ExDeadlock + "-" + bid + "_1"}, decV, rev)
	}
	return out
}

func seedAdjudications() []service.BundleAdjud {
	// Both adopted and rejected outcomes are kept; they conflict on the same
	// logical counterexample candidate on two branches.
	return []service.BundleAdjud{
		{
			ID: "adj-accept-deadlock", BranchID: BranchDecisionA,
			CandidateID:   "cand_" + ExDeadlock + "-" + BranchDecisionA + "_1",
			ExplorationID: ExDeadlock + "-" + BranchDecisionA,
			Decision:      "accepted", BaseRev: 4,
			Rationale: "窗口 A：接受死锁反例，路径与容量/输入判定一致",
			EventSeq:  0,
		},
		{
			ID: "adj-reject-deadlock", BranchID: BranchDecisionB,
			CandidateID:   "cand_" + ExDeadlock + "-" + BranchDecisionB + "_1",
			ExplorationID: ExDeadlock + "-" + BranchDecisionB,
			Decision:      "rejected", BaseRev: 4, ConflictStatus: "conflict",
			Rationale: "窗口 B：基于旧 rev=4 否定同一候选；后提交，看到与窗口 A 的版本冲突",
			EventSeq:  0,
		},
	}
}

func sourceSlug(aspect string) string {
	return map[string]string{
		"model": "model", "identity": "identity", "weights": "weights",
		"initial_tokens": "initial-tokens", "capacity": "capacity",
		"strategy": "strategy", "budget": "budget",
	}[aspect]
}

func jsonNum(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
