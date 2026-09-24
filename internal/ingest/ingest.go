// Package ingest turns the modeling team's raw JSON material into a pnnet.
//
// Import discipline:
//
//   - the source bytes are always stored verbatim before interpretation;
//   - normalization (text -> int, units) happens only here, in the derived
//     layer; a failure to convert a scalar never deletes it: the raw text stays
//     on the node/arc with a diagnostic, and drawing falls back gracefully;
//   - structural problems (bad json, duplicate ids, dangling arc endpoints)
//     are fatal: the caller must roll back the whole import so no half record
//     survives.
package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"pnwork/internal/pnnet"
)

// Source formats accepted in the "model" aspect JSON document.
type rawDoc struct {
	ModelID     string            `json:"model_id"`
	Name        string            `json:"name"`
	Places      []rawPlace        `json:"places"`
	Transitions []rawTrans        `json:"transitions"`
	Arcs        []rawArc          `json:"arcs"`
	Nodes       []rawNode         `json:"nodes"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type rawPlace struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	X        any    `json:"x"`
	Y        any    `json:"y"`
	Capacity any    `json:"capacity"`
	Unit     string `json:"unit"`
}

type rawTrans struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	X     any    `json:"x"`
	Y     any    `json:"y"`
}

type rawNode struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type rawArc struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Weight any    `json:"weight"`
	Unit   string `json:"unit"`
}

// Diagnostic is a machine-readable conversion note.
type Diagnostic struct {
	Object string `json:"object"`
	Field  string `json:"field"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// Outcome is the derived layer result of an import.
type Outcome struct {
	Net         *pnnet.Net   `json:"-"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	SHA256      string       `json:"sha256"`
	Raw         []byte       `json:"-"`
}

// Digest returns the stable content hash of source bytes.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Parse validates JSON structurally and derives the net.
func Parse(raw []byte) (*Outcome, error) {
	var doc rawDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("fatal: model json invalid: %w", err)
	}
	if strings.TrimSpace(doc.ModelID) == "" {
		return nil, fmt.Errorf("fatal: model_id is required")
	}
	out := &Outcome{Raw: raw, SHA256: Digest(raw)}
	net := &pnnet.Net{
		ModelID:     doc.ModelID,
		Name:        doc.Name,
		Annotations: doc.Annotations,
	}
	placeIDs := map[string]bool{}
	for _, rp := range doc.Places {
		if rp.ID == "" {
			return nil, fmt.Errorf("fatal: place with empty id")
		}
		if placeIDs[rp.ID] {
			return nil, fmt.Errorf("fatal: duplicate place id %q", rp.ID)
		}
		placeIDs[rp.ID] = true
		p := pnnet.Place{
			ID:    rp.ID,
			Label: rp.Label,
			X:     coordRaw(rp.X, "place:"+rp.ID+":x", &out.Diagnostics),
			Y:     coordRaw(rp.Y, "place:"+rp.ID+":y", &out.Diagnostics),
			Capacity: capacityRaw(rp.Capacity, unitOr(rp.Unit, pnnet.UnitTokenCount),
				"place:"+rp.ID, &out.Diagnostics),
		}
		net.Places = append(net.Places, p)
	}
	transIDs := map[string]bool{}
	for _, rt := range doc.Transitions {
		if rt.ID == "" {
			return nil, fmt.Errorf("fatal: transition with empty id")
		}
		if placeIDs[rt.ID] || transIDs[rt.ID] {
			return nil, fmt.Errorf("fatal: duplicate node id %q", rt.ID)
		}
		transIDs[rt.ID] = true
		net.Transitions = append(net.Transitions, pnnet.Transition{
			ID:    rt.ID,
			Label: rt.Label,
			X:     coordRaw(rt.X, "transition:"+rt.ID+":x", &out.Diagnostics),
			Y:     coordRaw(rt.Y, "transition:"+rt.ID+":y", &out.Diagnostics),
		})
	}
	arcIDs := map[string]bool{}
	for _, ra := range doc.Arcs {
		if ra.ID == "" {
			return nil, fmt.Errorf("fatal: arc with empty id")
		}
		if arcIDs[ra.ID] {
			return nil, fmt.Errorf("fatal: duplicate arc id %q", ra.ID)
		}
		arcIDs[ra.ID] = true
		kind := pnnet.ArcNormal
		switch strings.ToLower(strings.TrimSpace(ra.Kind)) {
		case "", "normal":
			kind = pnnet.ArcNormal
		case "inhibitor":
			kind = pnnet.ArcInhibitor
		default:
			return nil, fmt.Errorf("fatal: arc %q unknown kind %q", ra.ID, ra.Kind)
		}
		if !placeIDs[ra.From] && !transIDs[ra.From] {
			return nil, fmt.Errorf("fatal: arc %q dangling from %q", ra.ID, ra.From)
		}
		if !placeIDs[ra.To] && !transIDs[ra.To] {
			return nil, fmt.Errorf("fatal: arc %q dangling to %q", ra.ID, ra.To)
		}
		net.Arcs = append(net.Arcs, pnnet.Arc{
			ID:     ra.ID,
			From:   ra.From,
			To:     ra.To,
			Kind:   kind,
			Weight: weightRaw(ra.Weight, unitOr(ra.Unit, pnnet.UnitTokenCount), "arc:"+ra.ID, &out.Diagnostics),
		})
	}
	for _, rn := range doc.Nodes {
		k := pnnet.Kind(strings.ToLower(strings.TrimSpace(rn.Kind)))
		if k != pnnet.KindPlace && k != pnnet.KindTransition {
			return nil, fmt.Errorf("fatal: node %q unknown kind %q", rn.ID, rn.Kind)
		}
		net.Nodes = append(net.Nodes, pnnet.Node{ID: rn.ID, Kind: k, Label: rn.Label})
	}
	if err := net.Validate(); err != nil {
		return nil, fmt.Errorf("fatal: %w", err)
	}
	out.Net = net
	return out, nil
}

func unitOr(u, def string) string {
	if strings.TrimSpace(u) == "" {
		return def
	}
	return u
}

func textOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func coordRaw(v any, where string, diags *[]Diagnostic) pnnet.Raw {
	r := pnnet.Raw{Text: textOf(v), Unit: pnnet.BasisSVGCoord, Basis: pnnet.BasisSVGCoord}
	if r.Text == "" {
		*diags = append(*diags, Diagnostic{Object: where, Field: "coord", Text: r.Text, Reason: "missing coordinate; layout fallback used"})
		return r
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(r.Text), 64)
	if err != nil {
		r.Diagnostics = []string{"unconvertible coordinate: " + err.Error()}
		*diags = append(*diags, Diagnostic{Object: where, Field: "coord", Text: r.Text, Reason: r.Diagnostics[0]})
		return r
	}
	iv := int64(n)
	r.Value = &iv
	return r
}

func weightRaw(v any, unit, where string, diags *[]Diagnostic) pnnet.Raw {
	r := pnnet.Raw{Text: textOf(v), Unit: unit, Basis: pnnet.UnitTokenCount}
	if r.Text == "" {
		one := int64(1)
		r.Value = &one // omitted weight defaults to 1 (standard convention), raw stays empty
		return r
	}
	n, err := parseCount(r.Text)
	if err != nil {
		r.Diagnostics = []string{err.Error()}
		*diags = append(*diags, Diagnostic{Object: where, Field: "weight", Text: r.Text, Reason: err.Error()})
		return r
	}
	r.Value = &n
	return r
}

func capacityRaw(v any, unit, where string, diags *[]Diagnostic) pnnet.Raw {
	r := pnnet.Raw{Text: textOf(v), Unit: unit, Basis: pnnet.UnitTokenCount}
	txt := strings.TrimSpace(r.Text)
	if txt == "" || txt == "inf" || txt == "∞" || txt == "unbounded" || txt == "omega" || txt == "ω" {
		return r // unbounded; Value nil, no diagnostics
	}
	n, err := parseCount(txt)
	if err != nil {
		r.Diagnostics = []string{"unconvertible capacity, kept raw: " + txt}
		*diags = append(*diags, Diagnostic{Object: where, Field: "capacity", Text: r.Text, Reason: r.Diagnostics[0]})
		return r
	}
	if n < 0 {
		return r // explicit unbounded marker -1
	}
	r.Value = &n
	return r
}

func parseCount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unconvertible integer %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("non-positive count %d", n)
	}
	return n, nil
}
