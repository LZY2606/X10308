// Package casepack ships the offline domain case "令牌流网：Petri net
// 可达状态、覆盖关系与反例路径". It seeds the versioned baseline (model,
// node identities, arc weights, initial tokens, capacities, exploration
// strategy, truncation budget), then a parameter revision, a local source
// change and conflicting human rulings, plus the seven-scenario
// counterexample archive.
package casepack

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"petriws/internal/petri"
	"petriws/internal/store"
)

// Snapshot is the versioned content of a model version: the normalized
// net plus every parameter that influences exploration.
type Snapshot struct {
	Net         *petri.Net `json:"net"`
	Strategy    string     `json:"strategy"`
	Budget      int        `json:"budget"` // unit: states
	RuleVersion int        `json:"rule_version"`
}

var seedClock = int64(0)

func ts() string {
	seedClock++
	return time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC).
		Add(time.Duration(seedClock) * time.Minute).Format(time.RFC3339Nano)
}

func rawJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

// ---- raw document builders (quantities keep units, coords keep basis) ----

type rawPlace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity string `json:"capacity"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
}
type rawTrans struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}
type rawArc struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Weight string `json:"weight"`
}
type rawInit struct {
	Place  string `json:"place"`
	Tokens string `json:"tokens"`
}
type rawModel struct {
	Places      []rawPlace `json:"places"`
	Transitions []rawTrans `json:"transitions"`
	Arcs        []rawArc   `json:"arcs"`
	Initial     []rawInit  `json:"initial"`
}

func tok(n int64) string { return fmt.Sprintf("%d token", n) }

// mainModel is the 令牌流网 baseline. weights maps arc ID -> weight so the
// local source change can flip exactly one.
func mainModel(loadWeight int64) *rawModel {
	return &rawModel{
		Places: []rawPlace{
			{ID: "p_in", Name: "原料", Capacity: "unbounded", X: 70, Y: 70},
			{ID: "p_stage", Name: "在制", Capacity: tok(2), X: 260, Y: 70},
			{ID: "p_tool", Name: "工装", Capacity: tok(1), X: 260, Y: 210},
			{ID: "p_done", Name: "成品", Capacity: "unknown", X: 450, Y: 70}, // unconvertible on purpose
			{ID: "p_log", Name: "审计记录", Capacity: tok(1), X: 450, Y: 210},
			{ID: "p_alarm", Name: "告警", Capacity: tok(1), X: 260, Y: 330},
		},
		Transitions: []rawTrans{
			{ID: "t_load", Name: "装载", X: 160, Y: 70},
			{ID: "t_work", Name: "加工", X: 350, Y: 140},
			{ID: "t_audit", Name: "审计", X: 160, Y: 210},
			{ID: "t_alarm", Name: "告警", X: 350, Y: 290},
		},
		Arcs: []rawArc{
			{ID: "a_in_load", From: "p_in", To: "t_load", Kind: "normal", Weight: tok(loadWeight)},
			{ID: "a_load_stage", From: "t_load", To: "p_stage", Kind: "normal", Weight: tok(1)},
			{ID: "a_stage_work", From: "p_stage", To: "t_work", Kind: "normal", Weight: tok(1)},
			{ID: "a_work_done", From: "t_work", To: "p_done", Kind: "normal", Weight: tok(1)},
			{ID: "a_tool_in", From: "p_tool", To: "t_work", Kind: "normal", Weight: tok(1)},
			{ID: "a_tool_out", From: "t_work", To: "p_tool", Kind: "normal", Weight: tok(1)},
			{ID: "a_in_audit", From: "p_in", To: "t_audit", Kind: "normal", Weight: tok(1)},
			{ID: "a_audit_log", From: "t_audit", To: "p_log", Kind: "normal", Weight: tok(1)},
			{ID: "a_log_alarm", From: "p_log", To: "t_alarm", Kind: "normal", Weight: tok(1)},
			{ID: "a_alarm_inh", From: "p_alarm", To: "t_load", Kind: "inhibitor", Weight: tok(1)},
		},
		Initial: []rawInit{
			{Place: "p_in", Tokens: tok(2)},
			{Place: "p_tool", Tokens: tok(1)},
		},
	}
}

// toDoc converts to the petri.RawDoc shape for normalization.
func toDoc(m *rawModel) *petri.RawDoc {
	b := rawJSON(m)
	var doc petri.RawDoc
	json.Unmarshal(b, &doc)
	return &doc
}

type seedCtx struct {
	tx *sql.Tx
}

func (c *seedCtx) source(id, kind, name string, raw []byte, unit, basis string) error {
	return store.CreateSource(c.tx, &store.Source{
		ID: id, Kind: kind, Name: name, Raw: raw, Unit: unit, Basis: basis, CreatedAt: ts(),
	})
}

func (c *seedCtx) modelVersion(id string, seq int, parent, label string, snap *Snapshot, srcIDs []string) error {
	return store.CreateModelVersion(c.tx, &store.ModelVersion{
		ID: id, Seq: seq, ParentID: parent, Label: label,
		ModelJSON: string(rawJSON(snap)), SourceIDs: srcIDs, CreatedAt: ts(),
	})
}

func (c *seedCtx) event(branchID, kind string, payload any, mvID string, rule int) (int64, error) {
	e := &store.Event{
		BranchID: branchID, Kind: kind, PayloadJSON: string(rawJSON(payload)),
		ModelVersionID: mvID, RuleVersion: rule, CreatedAt: ts(),
	}
	return store.AppendEvent(c.tx, e)
}

func (c *seedCtx) run(runID, branchID, mvID string, snap *Snapshot, srcIDs []string) (*petri.Result, error) {
	res := petri.Explore(snap.Net, petri.Strategy(snap.Strategy), snap.Budget, petri.RulesFor(snap.RuleVersion))
	r := &store.Run{
		ID: runID, BranchID: branchID, ModelVersionID: mvID,
		RuleVersion: snap.RuleVersion, Strategy: snap.Strategy, Budget: snap.Budget,
		Status: res.Status, ResultJSON: string(rawJSON(res)), CreatedAt: ts(),
	}
	if err := store.CreateRun(c.tx, r); err != nil {
		return nil, err
	}
	for _, sid := range srcIDs {
		if err := store.AddDependency(c.tx, &store.Dependency{
			DerivedKind: "run", DerivedID: runID, SourceID: sid, VersionSeq: 1,
		}); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// Seed loads the case pack. It is idempotent and atomic: any failure
// rolls back the whole import, leaving no half-written records.
func Seed(s *store.Store) error {
	if _, err := s.GetSource("src-model-v1"); err == nil {
		return nil // already seeded
	}
	return s.InTx(func(tx *sql.Tx) error {
		c := &seedCtx{tx: tx}
		if err := seedMainBranch(c); err != nil {
			return err
		}
		if err := seedEnablementBranch(c); err != nil {
			return err
		}
		return seedCounterexamples(c)
	})
}
