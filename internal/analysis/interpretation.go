package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"petristudio/internal/petri"
	"petristudio/internal/sourcemodel"
)

// Aspect is one versioned derivation dimension.
type Aspect struct {
	Name           string   `json:"name"`
	SourceDocID    string   `json:"source_doc_id"`
	SourceRevision string   `json:"source_revision"`
	ContentHash    string   `json:"content_hash"`
	ObjectIDs      []string `json:"object_ids"`
}

// TargetSpec is a goal marking expressed in raw source form plus its key.
type TargetSpec struct {
	ID     string            `json:"id"`
	Tokens map[string]string `json:"tokens"`
	Key    string            `json:"key,omitempty"`
}

// Interpretation binds a net document and a strategy document to a
// normalized model. It never mutates source: unconvertible raw values
// stay in the documents and are reported via Diagnostics.
type Interpretation struct {
	ID                       string                    `json:"id"`
	ParentInterpretationID   string                    `json:"parent_interpretation_id,omitempty"`
	NetDocID                 string                    `json:"net_doc_id"`
	NetDocRevision           string                    `json:"net_doc_revision"`
	StrategyDocID            string                    `json:"strategy_doc_id"`
	StrategyDocRevision      string                    `json:"strategy_doc_revision"`
	Aspects                  map[string]Aspect         `json:"aspects"`
	Net                      *petri.Net                `json:"net"`
	Initial                  petri.Marking             `json:"initial"`
	Strategy                 petri.Strategy            `json:"strategy"`
	Targets                  []TargetSpec              `json:"targets"`
	Positions                map[string][2]float64     `json:"positions"`
	Diagnostics              []sourcemodel.Diagnostic  `json:"diagnostics"`
	Rules                    []string                  `json:"rules"`
	ChangeNote               string                    `json:"change_note,omitempty"`
}

func hash(parts ...interface{}) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	for _, p := range parts {
		_ = enc.Encode(p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func rawInt(r *sourcemodel.Raw, d *[]sourcemodel.Diagnostic, objectID, field string, defaultVal int64, acceptInfinity bool) (int64, bool) {
	if r == nil {
		return defaultVal, true
	}
	t := strings.TrimSpace(r.Text)
	if acceptInfinity && (t == "∞" || strings.EqualFold(t, "inf") || strings.EqualFold(t, "infinity")) {
		return -1, true
	}
	v, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		*d = append(*d, sourcemodel.Diagnostic{
			Severity: "error", Code: "unconvertible-value",
			Message:  fmt.Sprintf("field %s value %q cannot be normalized to token count", field, r.Text),
			ObjectID: objectID, Field: field, Raw: r,
		})
		return defaultVal, false
	}
	return v, true
}

func rawCoord(r *sourcemodel.Raw, d *[]sourcemodel.Diagnostic, objectID, field string) (float64, bool) {
	if r == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(r.Text), 64)
	if err != nil {
		*d = append(*d, sourcemodel.Diagnostic{
			Severity: "warning", Code: "unconvertible-coordinate",
			Message:  fmt.Sprintf("coordinate %s=%q kept raw; using auto layout for %s", field, r.Text, objectID),
			ObjectID: objectID, Field: field, Raw: r,
		})
		return 0, false
	}
	return v, true
}

// Interpret builds a new interpretation from versioned source documents.
func Interpret(netDoc, stratDoc *sourcemodel.Doc, parentID, changeNote string) (*Interpretation, error) {
	if netDoc == nil || netDoc.Kind != "petrinet" {
		return nil, fmt.Errorf("interpret requires a petrinet source document")
	}
	if stratDoc == nil || stratDoc.Kind != "strategy" {
		return nil, fmt.Errorf("interpret requires a strategy source document")
	}
	it := &Interpretation{
		ParentInterpretationID: parentID,
		NetDocID:               netDoc.ID, NetDocRevision: netDoc.Revision,
		StrategyDocID: stratDoc.ID, StrategyDocRevision: stratDoc.Revision,
		Aspects: map[string]Aspect{}, Positions: map[string][2]float64{},
		Rules: AllRules(), ChangeNote: changeNote,
	}
	net := &petri.Net{ID: netDoc.ID, Name: netDoc.Title}
	var nodeOrder, idOrder, arcOrder []string
	initial := petri.Marking{}

	placeSet := map[string]bool{}
	for _, n := range netDoc.Nodes {
		switch n.Kind {
		case "place":
			placeSet[n.ID] = true
			capVal, capOK := int64(-1), true
			if n.Capacity != nil {
				capVal, capOK = rawInt(n.Capacity, &it.Diagnostics, n.ID, "capacity", -1, true)
			}
			tokVal := int64(0)
			if n.Tokens != nil {
				var ok bool
				tokVal, ok = rawInt(n.Tokens, &it.Diagnostics, n.ID, "tokens", 0, false)
				if !ok {
					tokVal = 0
				}
			}
			net.Places = append(net.Places, petri.Place{ID: n.ID, Name: n.Name, Capacity: capVal})
			initial[n.ID] = tokVal
			if x, ok := rawCoord(n.X, &it.Diagnostics, n.ID, "x"); ok {
				p := it.Positions[n.ID]
				p[0] = x
				it.Positions[n.ID] = p
			}
			if y, ok := rawCoord(n.Y, &it.Diagnostics, n.ID, "y"); ok {
				p := it.Positions[n.ID]
				p[1] = y
				it.Positions[n.ID] = p
			}
			nodeOrder = append(nodeOrder, n.ID)
			_ = capOK
		case "transition":
			net.Transitions = append(net.Transitions, petri.Transition{ID: n.ID, Name: n.Name})
			if x, ok := rawCoord(n.X, &it.Diagnostics, n.ID, "x"); ok {
				p := it.Positions[n.ID]
				p[0] = x
				it.Positions[n.ID] = p
			}
			if y, ok := rawCoord(n.Y, &it.Diagnostics, n.ID, "y"); ok {
				p := it.Positions[n.ID]
				p[1] = y
				it.Positions[n.ID] = p
			}
			nodeOrder = append(nodeOrder, n.ID)
		default:
			it.Diagnostics = append(it.Diagnostics, sourcemodel.Diagnostic{
				Severity: "error", Code: "unknown-node-kind",
				Message:  fmt.Sprintf("node %q has unknown kind %q", n.ID, n.Kind),
				ObjectID: n.ID,
			})
		}
		idOrder = append(idOrder, n.ID)
	}
	for _, a := range netDoc.Arcs {
		w := int64(1)
		if a.Weight != nil {
			var ok bool
			w, ok = rawInt(a.Weight, &it.Diagnostics, a.ID, "weight", 1, false)
			if !ok {
				w = 1
			}
		}
		kind := petri.ArcNormal
		if a.Kind == "inhibitor" {
			kind = petri.ArcInhibitor
		}
		net.Arcs = append(net.Arcs, petri.Arc{ID: a.ID, From: a.From, To: a.To, Weight: w, Kind: kind, RefsRaw: rawText(a.Weight)})
		arcOrder = append(arcOrder, a.ID)
	}

	// strategy normalization
	kind, _ := stratDoc.Payload["kind"].(string)
	if kind == "" {
		kind = "bfs"
	}
	var diag []sourcemodel.Diagnostic
	budget := int64(1000)
	if b, ok := stratDoc.Payload["budget"].(string); ok && b != "" {
		v, ok2 := rawInt(&sourcemodel.Raw{Text: b, Unit: "states"}, &diag, stratDoc.ID, "budget", 1000, false)
		if ok2 {
			budget = v
		}
	}
	frontier := int64(4096)
	if f, ok := stratDoc.Payload["frontier"].(string); ok && f != "" {
		v, ok2 := rawInt(&sourcemodel.Raw{Text: f, Unit: "items"}, &diag, stratDoc.ID, "frontier", 4096, false)
		if ok2 {
			frontier = v
		}
	}
	it.Diagnostics = append(it.Diagnostics, diag...)

	// Build early so target keys use canonical place order.
	idx, err := net.BuildIndex()
	if err != nil {
		return nil, fmt.Errorf("invalid normalized net: %w", err)
	}
	_ = idx
	it.Net = net
	it.Initial = initial
	it.Strategy = petri.Strategy{Kind: kind, StateBudget: int(budget), FrontierBudget: int(frontier), MaxTokensPerPlace: 1 << 40}

	if rawTargets, ok := stratDoc.Payload["targets"]; ok {
		_ = rawTargets
	}
	// targets are encoded as "target:<id>" maps in payload
	keys := make([]string, 0)
	for k := range stratDoc.Payload {
		if strings.HasPrefix(k, "target:") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		tokMap, _ := stratDoc.Payload[k].(map[string]string)
		t := TargetSpec{ID: strings.TrimPrefix(k, "target:"), Tokens: map[string]string{}}
		m := petri.Marking{}
		valid := true
		for pid, tv := range tokMap {
			t.Tokens[pid] = tv
			if !placeSet[pid] {
				it.Diagnostics = append(it.Diagnostics, sourcemodel.Diagnostic{
					Severity: "warning", Code: "unknown-target-place",
					Message:  fmt.Sprintf("target %s references unknown place %s", t.ID, pid),
					ObjectID: t.ID, Field: "tokens." + pid,
				})
				valid = false
				continue
			}
			v, ok := rawInt(&sourcemodel.Raw{Text: tv, Unit: "token"}, &it.Diagnostics, t.ID, "tokens."+pid, 0, false)
			if !ok {
				valid = false
				continue
			}
			m[pid] = v
		}
		if valid && len(tokMap) > 0 {
			t.Key = net.CanonicalKey(m)
		}
		it.Targets = append(it.Targets, t)
	}

	// Aspects: each of the seven versioned dimensions hashes its direct
	// source content so change scope is computable.
	it.Aspects[AspectOriginalModel] = aspect(AspectOriginalModel, netDoc, nodeOrder, string(netDoc.RawBytes))
	it.Aspects[AspectNodeIdentity] = aspect(AspectNodeIdentity, netDoc, idOrder, mustJSON(idOrder))
	arcHashes := []string{}
	for _, a := range netDoc.Arcs {
		arcHashes = append(arcHashes, a.ID+":"+a.From+"->"+a.To+":"+rawText(a.Weight)+":"+a.Kind)
	}
	it.Aspects[AspectArcWeights] = aspect(AspectArcWeights, netDoc, arcOrder, strings.Join(arcHashes, ";"))
	var tokParts []string
	for _, n := range netDoc.Nodes {
		if n.Kind == "place" {
			tokParts = append(tokParts, n.ID+"="+rawText(n.Tokens))
		}
	}
	sort.Strings(tokParts)
	it.Aspects[AspectInitialTokens] = aspect(AspectInitialTokens, netDoc, placeIDs(netDoc), strings.Join(tokParts, ";"))
	var capParts []string
	for _, n := range netDoc.Nodes {
		if n.Kind == "place" {
			capParts = append(capParts, n.ID+"="+rawText(n.Capacity))
		}
	}
	sort.Strings(capParts)
	it.Aspects[AspectCapacities] = aspect(AspectCapacities, netDoc, placeIDs(netDoc), strings.Join(capParts, ";"))
	it.Aspects[AspectExplorationStrategy] = aspect(AspectExplorationStrategy, stratDoc, nil, kind)
	it.Aspects[AspectTruncationBudget] = aspect(AspectTruncationBudget, stratDoc, nil, fmt.Sprintf("%s|%s", getPayload(stratDoc, "budget"), getPayload(stratDoc, "frontier")))

	aspectHashes := []string{}
	for _, name := range AspectNames() {
		aspectHashes = append(aspectHashes, name+"="+it.Aspects[name].ContentHash)
	}
	it.ID = "itp_" + hash(netDoc.ID, stratDoc.ID, aspectHashes)

	for _, d := range it.Diagnostics {
		if d.Severity == "error" {
			return it, fmt.Errorf("interpretation produced %d error diagnostics", countErrors(it.Diagnostics))
		}
	}
	return it, nil
}

func aspect(name string, doc *sourcemodel.Doc, objects []string, content string) Aspect {
	return Aspect{
		Name: name, SourceDocID: doc.ID, SourceRevision: doc.Revision,
		ContentHash: hash(name, content), ObjectIDs: append([]string(nil), objects...),
	}
}

func rawText(r *sourcemodel.Raw) string {
	if r == nil {
		return ""
	}
	return r.Text
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func placeIDs(n *petri.Net) []string {
	out := make([]string, 0, len(n.Places))
	for _, p := range n.Places {
		out = append(out, p.ID)
	}
	return out
}

func getPayload(d *sourcemodel.Doc, k string) string {
	if v, ok := d.Payload[k].(string); ok {
		return v
	}
	return ""
}

func countErrors(ds []sourcemodel.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Severity == "error" {
			n++
		}
	}
	return n
}

// ChangedAspects compares two interpretations and returns aspect names
// whose direct-source content hash differs.
func ChangedAspects(old, neu *Interpretation) []string {
	var changed []string
	for _, name := range AspectNames() {
		a, b := old.Aspects[name], neu.Aspects[name]
		if a.ContentHash != b.ContentHash {
			changed = append(changed, name)
		}
	}
	return changed
}
