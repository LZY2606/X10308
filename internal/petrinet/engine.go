package petrinet

import (
	"fmt"
	"sort"
)

// StateRec 是状态空间中的一个节点，携带稳定身份。
type StateRec struct {
	ID        string  `json:"id"`
	Marking   Marking `json:"marking"`
	Canonical string  `json:"canonical"`
	Depth     int     `json:"depth"`
}

// EdgeRec 是状态间的一次 transition 发射。
type EdgeRec struct {
	From string `json:"from"`
	To   string `json:"to"`
	TID  string `json:"transition_id"`
}

// CoverPair 记录探索出的状态间覆盖关系（a 严格覆盖 b）。
type CoverPair struct {
	Covering string `json:"covering"`
	Covered  string `json:"covered"`
}

// DeadlockInfo 给出死锁状态及每个 transition 的禁用原因。
type DeadlockInfo struct {
	StateID string          `json:"state_id"`
	Reasons []DisableReason `json:"reasons"`
}

// RuleEvent 是机器可读的规则判断依据事件。
type RuleEvent struct {
	Seq     int            `json:"seq"`
	Kind    string         `json:"kind"` // enablement | canonicalization | budget_exhausted | unbounded_detected
	Subject string         `json:"subject"`
	Payload map[string]any `json:"payload"`
}

// Result 是一次探索的完整产物。
type Result struct {
	Status     string         `json:"status"`
	Strategy   string         `json:"strategy"`
	Budget     int64          `json:"budget"`
	States     []StateRec     `json:"states"`
	Edges      []EdgeRec      `json:"edges"`
	Frontier   []StateRec     `json:"frontier"`
	Deadlocks  []DeadlockInfo `json:"deadlocks"`
	Covers     []CoverPair    `json:"covers"`
	Unbounded  bool           `json:"unbounded"`
	RuleEvents []RuleEvent    `json:"rule_events"`
}

// Explore 运行可达性探索。
// 规则：enablement 同时考虑输入/抑制/容量；canonicalization 不合并 token 不同的
// marking；预算耗尽返回 unknown 并保留 frontier。
func Explore(md *Model) *Result {
	order := md.placeOrder()
	res := &Result{Strategy: md.Strategy, Budget: md.Budget, Status: StatusComplete}
	seq := 0
	emit := func(kind, subject string, payload map[string]any) {
		seq++
		res.RuleEvents = append(res.RuleEvents, RuleEvent{Seq: seq, Kind: kind, Subject: subject, Payload: payload})
	}

	stateSeq := 0
	byKey := map[string]string{}
	states := map[string]*StateRec{}
	parent := map[string]string{} // stateID -> parent stateID

	addState := func(m Marking, depth int) (*StateRec, bool) {
		key := m.Canonical(order)
		if id, ok := byKey[key]; ok {
			return states[id], false
		}
		stateSeq++
		st := &StateRec{ID: fmt.Sprintf("s%d", stateSeq), Marking: m, Canonical: key, Depth: depth}
		states[st.ID] = st
		byKey[key] = st.ID
		res.States = append(res.States, *st)
		emit("canonicalization", st.ID, map[string]any{
			"rule":      "状态 canonicalization 不合并 token 不同的 marking",
			"canonical": key,
			"basis":     "place_id",
		})
		return st, true
	}

	initial := Marking{}
	for _, p := range md.Places {
		initial[p.ID] = md.Initial[p.ID]
	}
	initSt, _ := addState(initial, 0)

	// 待扩展队列；BFS 用队首，DFS 用队尾。
	queue := []*StateRec{initSt}
	expanded := int64(0)

	for len(queue) > 0 {
		if expanded >= md.Budget {
			res.Status = StatusTruncated
			for _, st := range queue {
				res.Frontier = append(res.Frontier, *st)
			}
			frontierIDs := []string{}
			for _, st := range queue {
				frontierIDs = append(frontierIDs, st.ID)
			}
			emit("budget_exhausted", "exploration", map[string]any{
				"rule":     "预算耗尽返回未知并保留 frontier",
				"budget":   md.Budget,
				"expanded": expanded,
				"frontier": frontierIDs,
			})
			break
		}
		var cur *StateRec
		if md.Strategy == StrategyDFS {
			cur = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		} else {
			cur = queue[0]
			queue = queue[1:]
		}
		expanded++

		enabledAny := false
		for _, t := range md.Transitions {
			ok, checks, reasons := md.Enabled(t.ID, cur.Marking)
			emit("enablement", t.ID+"@"+cur.ID, map[string]any{
				"rule":    "transition enablement 同时考虑输入、抑制和容量",
				"state":   cur.ID,
				"enabled": ok,
				"checks":  checks,
				"reasons": reasons,
			})
			if !ok {
				continue
			}
			enabledAny = true
			next := md.Fire(t.ID, cur.Marking)
			nst, fresh := addState(next, cur.Depth+1)
			res.Edges = append(res.Edges, EdgeRec{From: cur.ID, To: nst.ID, TID: t.ID})
			if fresh {
				parent[nst.ID] = cur.ID
				// 无界检测：新状态严格覆盖其任一祖先。
				for anc := cur.ID; anc != ""; anc = parent[anc] {
					if nst.Marking.StrictlyCovers(states[anc].Marking) {
						res.Unbounded = true
						emit("unbounded_detected", nst.ID, map[string]any{
							"rule":     "后代 marking 严格覆盖祖先，疑似无界增长",
							"covering": nst.ID,
							"covered":  anc,
						})
						break
					}
				}
				queue = append(queue, nst)
			}
		}
		if !enabledAny {
			dl := DeadlockInfo{StateID: cur.ID}
			for _, t := range md.Transitions {
				_, _, reasons := md.Enabled(t.ID, cur.Marking)
				dl.Reasons = append(dl.Reasons, reasons...)
			}
			res.Deadlocks = append(res.Deadlocks, dl)
		}
	}

	// 覆盖关系：探索到的状态两两比较（小规模案例足够）。
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, a := range ids {
		for _, b := range ids {
			if a == b {
				continue
			}
			if states[a].Marking.StrictlyCovers(states[b].Marking) {
				res.Covers = append(res.Covers, CoverPair{Covering: a, Covered: b})
			}
		}
	}
	return res
}
