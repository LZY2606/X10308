package petrinet

import (
	"fmt"
	"strconv"
	"strings"
)

// ArcKind 区分加权输入/输出弧与抑制弧。
type ArcKind string

const (
	ArcInput     ArcKind = "input"     // place -> transition，加权消耗
	ArcOutput    ArcKind = "output"    // transition -> place，加权产出
	ArcInhibitor ArcKind = "inhibitor" // place -> transition，抑制弧
)

// Strategy 为探索策略。
const (
	StrategyBFS = "bfs"
	StrategyDFS = "dfs"
)

// Status 值。
const (
	StatusComplete  = "complete"
	StatusTruncated = "unknown_truncated"
)

// Quantity 同时保存原始表示与派生的规范化值。
// 无法转换时 Normalized 保持 nil，Diagnostic 给出原因，原值不丢失。
type Quantity struct {
	ID         string `json:"id"`
	Field      string `json:"field"`
	OwnerID    string `json:"owner_id"`
	Raw        string `json:"raw"`
	Unit       string `json:"unit,omitempty"`
	Basis      string `json:"basis,omitempty"` // 坐标基准（marking 用）
	Normalized *int64 `json:"normalized,omitempty"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// Place 携带容量约束（可能无界，可能无法转换）。
type Place struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Capacity *Quantity `json:"capacity,omitempty"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Arc struct {
	ID     string   `json:"id"`
	From   string   `json:"from"`
	To     string   `json:"to"`
	Kind   ArcKind  `json:"kind"`
	Weight Quantity `json:"weight"`
}

// Marking 是以 place id 为坐标的 token 向量；坐标基准固定为 place id。
type Marking map[string]int64

// Model 是一次版本化导入得到的完整模型快照。
type Model struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Places      []Place      `json:"places"`
	Transitions []Transition `json:"transitions"`
	Arcs        []Arc        `json:"arcs"`
	InitialRaw  []Quantity   `json:"initial_raw"`
	Strategy    string       `json:"strategy"`
	Budget      int64        `json:"budget"`
	Initial     Marking      `json:"initial"`
	Diagnostics []Quantity   `json:"diagnostics"`
}

// ParseQuantity 在派生层做规范化；原始表示始终由调用方另外保留。
func ParseQuantity(id, field, owner, raw, unit, basis string) Quantity {
	q := Quantity{ID: id, Field: field, OwnerID: owner, Raw: raw, Unit: unit, Basis: basis}
	raw = strings.TrimSpace(raw)
	if raw == "∞" || strings.EqualFold(raw, "inf") || strings.EqualFold(raw, "unbounded") {
		v := int64(-1)
		q.Normalized = &v
		q.Diagnostic = "原始表示为非有限数值，已归一为无界(-1)，原始表示保留"
		return q
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		q.Diagnostic = fmt.Sprintf("无法转换为整数（单位 %s），值留在原位置：%v", unit, err)
		return q
	}
	if v < 0 {
		q.Diagnostic = "负值非法，保留原值待人工处理"
		return q
	}
	q.Normalized = &v
	return q
}

// Canonical 返回以 place id 排序的坐标串。
// token 不同的 marking 必然产生不同 key（精确匹配，不做合并）。
func (m Marking) Canonical(placeOrder []string) string {
	parts := make([]string, 0, len(placeOrder))
	for _, p := range placeOrder {
		parts = append(parts, p+"="+strconv.FormatInt(m[p], 10))
	}
	return strings.Join(parts, ";")
}

// Covers 报告 a 是否逐坐标覆盖 b（a>=b）。
func (a Marking) Covers(b Marking) bool {
	for k, v := range b {
		if a[k] < v {
			return false
		}
	}
	return true
}

// StrictlyCovers 要求 a>=b 且至少一个坐标严格更大。
func (a Marking) StrictlyCovers(b Marking) bool {
	if !a.Covers(b) {
		return false
	}
	strict := false
	for k, v := range b {
		if a[k] > v {
			strict = true
		}
	}
	// 还要考虑 a 中存在而 b 为 0 的坐标
	for k, v := range a {
		if v > b[k] {
			strict = true
		}
	}
	return strict
}

// SortedPlaces 给出稳定的 place 顺序（模型顺序即导入顺序）。
func (md *Model) placeIndex() map[string]int {
	idx := map[string]int{}
	for i, p := range md.Places {
		idx[p.ID] = i
	}
	return idx
}

func (md *Model) placeOrder() []string {
	out := make([]string, len(md.Places))
	for i, p := range md.Places {
		out[i] = p.ID
	}
	return out
}

// placeCap 返回容量；nil/无界(-1) 视为 math.MaxInt64。
func (md *Model) placeCap(placeID string) (int64, bool) {
	for _, p := range md.Places {
		if p.ID == placeID {
			if p.Capacity == nil || p.Capacity.Normalized == nil {
				return 1 << 62, false
			}
			v := *p.Capacity.Normalized
			if v < 0 {
				return 1 << 62, false
			}
			return v, true
		}
	}
	return 1 << 62, false
}

// arcFlow 汇总某个 transition 在每个 place 上的净流与输入/抑制需求。
type tFlow struct {
	input   map[string]int64
	output  map[string]int64
	inhibit map[string]int64
}

func (md *Model) flow(tid string) tFlow {
	f := tFlow{input: map[string]int64{}, output: map[string]int64{}, inhibit: map[string]int64{}}
	for _, a := range md.Arcs {
		w := int64(0)
		if a.Weight.Normalized != nil {
			w = *a.Weight.Normalized
		}
		switch a.Kind {
		case ArcInput:
			if a.To == tid {
				f.input[a.From] += w
			}
		case ArcOutput:
			if a.From == tid {
				f.output[a.To] += w
			}
		case ArcInhibitor:
			if a.To == tid {
				f.inhibit[a.From] = w
			}
		}
	}
	return f
}

// DisableCode 是机器可读的禁用原因码。
const (
	ReasonInput     = "input_unsatisfied"
	ReasonInhibitor = "inhibitor_active"
	ReasonCapacity  = "capacity_exceeded"
)

// DisableReason 解释某 transition 为何不能发射。
type DisableReason struct {
	TransitionID string `json:"transition_id"`
	Code         string `json:"code"`
	Detail       string `json:"detail"`
}

// Enabled 同时考虑输入、抑制弧与容量边界。
// 返回是否可发射，以及按类别给出的判断依据。
func (md *Model) Enabled(tid string, m Marking) (bool, map[string]bool, []DisableReason) {
	f := md.flow(tid)
	checks := map[string]bool{"input": true, "inhibitor": true, "capacity": true}
	var reasons []DisableReason

	for p, need := range f.input {
		if m[p] < need {
			checks["input"] = false
			reasons = append(reasons, DisableReason{tid, ReasonInput,
				fmt.Sprintf("place %s 有 %d token，需要 %d", p, m[p], need)})
		}
	}
	for p, threshold := range f.inhibit {
		if m[p] >= threshold {
			checks["inhibitor"] = false
			reasons = append(reasons, DisableReason{tid, ReasonInhibitor,
				fmt.Sprintf("place %s 有 %d token，达到抑制阈值 %d", p, m[p], threshold)})
		}
	}
	// 容量检查基于发射后的净结果（自环净变化可能为 0）。
	if checks["input"] {
		places := map[string]bool{}
		for p := range f.output {
			places[p] = true
		}
		for p := range f.input {
			places[p] = true
		}
		for p := range places {
			capV, bounded := md.placeCap(p)
			if !bounded {
				continue
			}
			after := m[p] - f.input[p] + f.output[p]
			if after > capV {
				checks["capacity"] = false
				reasons = append(reasons, DisableReason{tid, ReasonCapacity,
					fmt.Sprintf("发射后 place %s 为 %d，超过容量 %d", p, after, capV)})
			}
		}
	}
	enabled := checks["input"] && checks["inhibitor"] && checks["capacity"]
	return enabled, checks, reasons
}

// Fire 假定 Enabled 为真，返回新 marking。
func (md *Model) Fire(tid string, m Marking) Marking {
	f := md.flow(tid)
	next := Marking{}
	for k, v := range m {
		next[k] = v
	}
	for p, w := range f.input {
		next[p] -= w
	}
	for p, w := range f.output {
		next[p] += w
	}
	return next
}
