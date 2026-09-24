// Package cases contains the repository's dedicated offline case package for
// "令牌流网：Petri net 可达状态、覆盖关系与反例路径".
//
// The material is domain-specific (places/transitions/weighted arcs/initial
// tokens/capacities/strategy/budget), never a generic spreadsheet. Every
// quantity keeps its unit or coordinate basis and its raw representation.
package cases

import (
	"embed"
	"encoding/json"
	"fmt"

	"pnwork/internal/service"
)

//go:embed pack/*.json
var packFS embed.FS

func mustFile(name string) json.RawMessage {
	b, err := packFS.ReadFile("pack/" + name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded case file %s: %v", name, err))
	}
	if !json.Valid(b) {
		panic("invalid embedded json: " + name)
	}
	return json.RawMessage(b)
}

// Stable ids used by the demo archive (stable across export/re-import).
const (
	CaseID = "case-tokenflow-01"

	BranchBaseline  = "br-baseline"
	BranchParamRev  = "br-param-revision"
	BranchLocalDep  = "br-local-dependency"
	BranchDecisionA = "br-decision-a"
	BranchDecisionB = "br-decision-b"

	SourceModel   = "src-model"
	SourceIdent   = "src-identity"
	SourceWeights = "src-weights"
	SourceInitial = "src-initial-tokens"
	SourceCap     = "src-capacity"
	SourceStrat   = "src-strategy"
	SourceBudget  = "src-budget"

	ExConcurrency = "ex-concurrency"
	ExUnbounded   = "ex-unbounded"
	ExCapBoundary = "ex-cap-boundary"
	ExDeadlock    = "ex-deadlock"
	ExInhibitor   = "ex-inhibitor"
	ExEnumerate   = "ex-enumerate"
	ExTruncated   = "ex-truncated"
)

// Bundle constructs the complete offline archive bundle.
func Bundle() *service.Bundle {
	asp := func(aspect, src string, version int, label, file string) service.BundleAspect {
		return service.BundleAspect{
			Aspect: aspect, SourceID: src, Version: version,
			Label: label, MediaType: "application/json", Content: mustFile(file),
		}
	}
	baselineAspects := []service.BundleAspect{
		asp("model", SourceModel, 1, "原模型 v1", "model.v1.json"),
		asp("identity", SourceIdent, 1, "节点身份 v1", "identity.v1.json"),
		asp("weights", SourceWeights, 1, "arc 权重 v1", "weights.v1.json"),
		asp("initial_tokens", SourceInitial, 1, "初始 token v1", "initial_tokens.v1.json"),
		asp("capacity", SourceCap, 1, "容量约束 v1", "capacity.v1.json"),
		asp("strategy", SourceStrat, 1, "探索策略 v1 (bfs)", "strategy.v1.json"),
		asp("budget", SourceBudget, 1, "截断预算 v1 (500 state)", "budget.v1.json"),
	}
	paramAspects := append([]service.BundleAspect{}, baselineAspects...)
	paramAspects[5] = asp("strategy", SourceStrat, 2, "探索策略 v2 (dfs)", "strategy.v2.json")
	paramAspects[6] = asp("budget", SourceBudget, 2, "截断预算 v2 (12 state)", "budget.v2.json")

	localAspects := append([]service.BundleAspect{}, paramAspects...)
	// local dependency change: capacity v2 on top of parameter revision
	localAspects[4] = asp("capacity", SourceCap, 2, "容量约束 v2 (p_buffer=1)", "capacity.v2.json")

	decisionAspects := func(initialVersion int, initialFile, initialLabel string) []service.BundleAspect {
		a := append([]service.BundleAspect{}, baselineAspects...)
		a[3] = asp("initial_tokens", SourceInitial, initialVersion, initialLabel, initialFile)
		return a
	}

	b := &service.Bundle{
		Format: "pnwork-tokenflow-archive/v1",
		Case: service.BundleCase{
			ID:          CaseID,
			Slug:        "tokenflow-petri-reachability",
			Title:       "令牌流网：Petri net 可达状态、覆盖关系与反例路径",
			Description: caseDescription(),
		},
		Baseline: service.BundleBranch{
			ID: BranchBaseline, Name: "基线：七要素版本化导入",
			Kind:    "baseline",
			Note:    "原模型、节点身份、arc 权重、初始 token、容量、探索策略、截断预算全部版本化",
			Aspects: baselineAspects,
		},
		Branches: []service.BundleBranch{
			{
				ID: BranchParamRev, ParentBranchID: BranchBaseline,
				Name: "参数修订：DFS + 预算 6", Kind: "revision",
				Note:    "一次参数修订：只改 strategy 与 budget，失效范围按依赖计算",
				Aspects: paramAspects,
			},
			{
				ID: BranchLocalDep, ParentBranchID: BranchParamRev,
				Name: "局部依赖改动：p_buffer 容量 2→1", Kind: "local_dep",
				Note:    "只影响引用 capacity 的结论；其它结论保持有效",
				Aspects: localAspects,
			},
			{
				ID: BranchDecisionA, ParentBranchID: BranchBaseline,
				Name: "人工决定 A：接受死锁反例", Kind: "decision",
				Note:    "同一图不同 marking；采纳的决定保留",
				Aspects: decisionAspects(2, "initial_tokens.v2.json", "初始 token v2 (死锁 marking)"),
			},
			{
				ID: BranchDecisionB, ParentBranchID: BranchBaseline,
				Name: "人工决定 B：否定同一候选（冲突）", Kind: "decision",
				Note:    "互相冲突的人工决定；未采纳的决定同样保留",
				Aspects: decisionAspects(2, "initial_tokens.v2.json", "初始 token v2 (死锁 marking)"),
			},
		},
		Questions:  questions(),
		Scenarios:  scenarios(),
		Narrative:  narrative(),
		SeedEvents: seedEvents(),
		SeedAdjuds: seedAdjudications(),
	}
	return b
}

func caseDescription() string {
	return `{"domain":"令牌流网","narrative_present":true}`
}
