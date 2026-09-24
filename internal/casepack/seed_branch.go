package casepack

import (
	"petriws/internal/petri"
	"petriws/internal/store"
)

// seedEnablementBranch forks br-enablement from mv-1 to verify the rule:
// enablement weighs inputs + inhibitor + capacity together;
// canonicalization never merges token-distinct markings; budget
// exhaustion yields unknown with the frontier kept. All legal candidates
// are preserved where no unique decision exists.
func seedEnablementBranch(c *seedCtx) error {
	if err := store.CreateBranch(c.tx, &store.Branch{
		ID: "br-enablement", Name: "第二分支：enablement 规则检验", ParentID: "br-main",
		ModelVersionID: "mv-1", RuleVersion: 2, CreatedAt: ts(),
	}); err != nil {
		return err
	}
	mv, err := getSnapshot(c, "mv-1")
	if err != nil {
		return err
	}
	if _, err := c.event("br-enablement", "reinterpret", map[string]any{
		"summary": "对 mv-1 重新解释：新建分支，旧分支与原始字节保持可读",
		"base":    "mv-1", "parent_branch": "br-main",
	}, "mv-1", 2); err != nil {
		return err
	}

	// Legacy rule v1 run: inhibitor ignored, capacity ignored.
	snapV1 := &Snapshot{Net: mv.Net, Strategy: "bfs", Budget: 64, RuleVersion: 1}
	resV1, err := c.run("run-enab-legacy", "br-enablement", "mv-1", snapV1, mv.SourceIDs)
	if err != nil {
		return err
	}
	// Current rule v2 run on the same model version.
	snapV2 := &Snapshot{Net: mv.Net, Strategy: "bfs", Budget: 64, RuleVersion: 2}
	resV2, err := c.run("run-enab-v2", "br-enablement", "mv-1", snapV2, mv.SourceIDs)
	if err != nil {
		return err
	}
	if _, err := c.event("br-enablement", "run", map[string]any{
		"run_id": "run-enab-legacy", "rule_version": 1, "status": resV1.Status,
		"states": len(resV1.States),
		"note":   "旧规则 v1：忽略抑制弧与容量，仅作历史对照",
	}, "mv-1", 1); err != nil {
		return err
	}
	if _, err := c.event("br-enablement", "run", map[string]any{
		"run_id": "run-enab-v2", "rule_version": 2, "status": resV2.Status,
		"states": len(resV2.States),
		"note":   "规则 v2：enablement 同时考虑输入、抑制与容量",
	}, "mv-1", 2); err != nil {
		return err
	}

	// Multi-candidate preservation: at the initial marking both t_load
	// and t_audit are legal; no unique decision exists, so every legal
	// candidate is stored.
	legal := []string{}
	for _, tr := range mv.Net.Transitions {
		if ok, _ := mv.Net.Fireable(&tr, mv.Net.Initial, petri.RulesFor(2)); ok {
			legal = append(legal, tr.ID)
		}
	}
	ids := []string{}
	for _, tid := range legal {
		cid := "cand-first-" + tid
		ids = append(ids, cid)
		if err := store.CreateCandidate(c.tx, &store.Candidate{
			ID: cid, BranchID: "br-enablement", RunID: "run-enab-v2", Kind: "target",
			PayloadJSON: string(rawJSON(map[string]any{
				"marking": mv.Net.Initial, "transition": tid,
				"basis": "enablement 同时满足输入、抑制与容量约束",
			})),
			CreatedAt: ts(),
		}); err != nil {
			return err
		}
	}
	last, err := c.event("br-enablement", "candidates", map[string]any{
		"summary": "初始标识下无法唯一决定：全部合法候选保留",
		"ids":     ids,
	}, "mv-1", 2)
	if err != nil {
		return err
	}
	return store.SetBranchHead(c.tx, "br-enablement", last)
}

// getSnapshot loads a model version snapshot from the DB (read via tx not
// needed: seeds run before commit, so read directly from model_versions
// would miss uncommitted rows; instead we rebuild from the stored JSON in
// the same tx).
func getSnapshot(c *seedCtx, mvID string) (*struct {
	Net       *petri.Net
	SourceIDs []string
}, error,
) {
	var modelJSON, srcIDs string
	err := c.tx.QueryRow(`SELECT model_json, source_ids FROM model_versions WHERE id=?`, mvID).
		Scan(&modelJSON, &srcIDs)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := jsonUnmarshal(modelJSON, &snap); err != nil {
		return nil, err
	}
	var ids []string
	if err := jsonUnmarshal(srcIDs, &ids); err != nil {
		return nil, err
	}
	return &struct {
		Net       *petri.Net
		SourceIDs []string
	}{Net: snap.Net, SourceIDs: ids}, nil
}
