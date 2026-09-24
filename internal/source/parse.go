// Package source parses the token-flow net authoring language (.tfn).
//
// The language is domain-specific (places/transitions/weighted arcs/
// inhibitors/capacities/units/coordinates), not a generic spreadsheet. The
// original bytes are stored verbatim. Parsing interprets them into a
// domain.NetSpec but keeps every raw representation; values that cannot be
// converted stay in place with a Diagnostic instead of being discarded.
package source

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"petriws/internal/domain"
)

// RulesVersion is the version of the interpretation/analysis rules applied
// while parsing. Bumped when rules change so derived conclusions can be
// invalidated by dependency tracking rather than silently replaced.
const RulesVersion = "rules-2026.09.1"

var (
	coordRE = regexp.MustCompile(`@\(([^,]*),([^)]*)\)`)
	unitRE  = regexp.MustCompile(`^(-?[0-9]+)\(([^)]*)\)$`)
	propRE  = regexp.MustCompile(`(weight|cap|init|inhib)=([^\s]+)`)
)

// Parse interprets raw .tfn bytes into a NetSpec. It returns a spec plus
// diagnostics; it does not error on bad domain values so that unconvertible
// material is retained in place. Structural/syntax problems are collected as
// diagnostics too. The caller decides whether exploration is admissible.
func Parse(sourceID string, sourceVer int, raw []byte) *domain.NetSpec {
	spec := &domain.NetSpec{
		SourceID:    sourceID,
		SourceVer:   sourceVer,
		RuleVersion: RulesVersion,
		RawSHA256:   domain.HashRaw(raw),
	}
	known := map[string]bool{}
	nodeStableSeq := 0
	arcStableSeq := 0
	addDiag := func(obj, field, rawv, msg string) {
		spec.Diagnostics = append(spec.Diagnostics, domain.Diagnostic{
			ObjectID: obj, Field: field, Raw: rawv, Message: msg,
		})
	}

	for lineNo, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		ref := fmt.Sprintf("%s:L%d", sourceID, lineNo+1)
		switch {
		case strings.HasPrefix(trim, "place ") || strings.HasPrefix(trim, "transition "):
			parts := strings.Fields(trim)
			kind := domain.KindPlace
			if strings.HasPrefix(trim, "transition ") {
				kind = domain.KindTransition
			}
			id := parts[1]
			if known[id] {
				addDiag(ref, "id", id, "duplicate node identity; later declaration ignored")
				continue
			}
			known[id] = true
			nodeStableSeq++
			n := domain.Node{
				ID: fmt.Sprintf("n%d", nodeStableSeq), Kind: kind,
				Label: id, StableID: id,
			}
			props := parseProps(trim)
			if c, ok := coordRE.FindStringSubmatch(trim); ok {
				n.Coord = domain.Coord{X: strings.TrimSpace(c[1]), Y: strings.TrimSpace(c[2]), Base: "svg-user-units"}
			}
			if kind == domain.KindPlace {
				n.Init = parseQuantity(props["init"], "token", addDiag, id, "init")
				if cap, ok := props["cap"]; ok {
					n.Capacity = parseQuantity(cap, "token", addDiag, id, "capacity")
					if n.Capacity.Valid {
						if n.Capacity.Value < n.Init.Value {
							addDiag(id, "capacity", cap, "capacity below initial tokens")
						}
					}
				} else {
					n.Infinite = true
				}
			}
			spec.Nodes = append(spec.Nodes, n)

		case strings.HasPrefix(trim, "arc ") || strings.Contains(trim, "->") || strings.Contains(trim, "-|") || strings.Contains(trim, "-o"):
			arcStableSeq++
			a := parseArc(trim, ref, known, addDiag, arcStableSeq)
			if a != nil {
				spec.Arc = append(spec.Arcs, a)
			}
		default:
			addDiag(ref, "line", trim, "unrecognized declaration; retained but ignored")
		}
	}

	// Reference validation after all nodes are known.
	for _, a := range spec.Arcs {
		if !known[a.Source] {
			addDiag(a.StableID, "source", a.Source, "arc references unknown node")
		}
		if !known[a.Target] {
			addDiag(a.StableID, "target", a.Target, "arc references unknown node")
		}
	}
	// Validate inhibitor arcs structurally: p -| t or t -| p.
	for _, a := range spec.Arcs {
		if a.Kind != domain.ArcInhibitor {
			continue
		}
		if !known[a.Source] || !known[a.Target] {
			continue
		}
		if nodeKind(spec, a.Source) == nodeKind(spec, a.Target) {
			addDiag(a.StableID, "inhibitor", a.StableID, "inhibitor arc must connect a place and a transition")
		}
	}
	return spec
}

func nodeKind(spec *domain.NetSpec, id string) domain.Kind {
	for _, n := range spec.Nodes {
		if n.StableID == id {
			return n.Kind
		}
	}
	return ""
}

func parseProps(line string) map[string]string {
	out := map[string]string{}
	for _, m := range propRE.FindAllStringSubmatch(line, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// parseQuantity converts "3", "3(tokens)", or raw unconvertible text into a
// Quantity, preserving the raw string and attaching a diagnostic on failure.
func parseQuantity(raw, defaultUnit string,
	addDiag func(obj, field, rawv, msg string), obj, field string) domain.Quantity {
	if raw == "" {
		return domain.Quantity{Raw: "", Unit: defaultUnit, Valid: true, Value: 0}
	}
	q := domain.Quantity{Raw: raw, Unit: defaultUnit}
	num := raw
	if m := unitRE.FindStringSubmatch(raw); m != nil {
		num = m[1]
		q.Unit = m[2]
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil || v < 0 {
		q.Valid = false
		q.Reason = "not a non-negative integer"
		addDiag(obj, field, raw, "value not convertible to non-negative integer; kept in place")
		return q
	}
	q.Valid = true
	q.Value = v
	return q
}

func parseArc(trim, ref string, known map[string]bool,
	addDiag func(obj, field, rawv, msg string), seq int) *domain.Arc {
	props := parseProps(trim)
	body := coordRE.ReplaceAllString(trim, "")
	for _, m := range propRE.FindAllStringSubmatch(body, -1) {
		_ = m
	}
	body = propRE.ReplaceAllString(body, "")
	body = strings.TrimSpace(strings.TrimPrefix(body, "arc "))

	kind := domain.ArcNormal
	var sep string
	for _, s := range []string{"-|", "-o", "->"} {
		if strings.Contains(body, s) {
			kind, sep = domain.ArcNormal, s
			if s == "-|" || s == "-o" {
				kind = domain.ArcInhibitor
			}
			break
		}
	}
	if sep == "" {
		addDiag(ref, "arc", trim, "arc missing connector (-> or -|)")
		return nil
	}
	ends := strings.SplitN(body, sep, 2)
	if len(ends) != 2 {
		addDiag(ref, "arc", trim, "arc must have exactly two endpoints")
		return nil
	}
	src := strings.TrimSpace(ends[0])
	dst := strings.TrimSpace(ends[1])
	w := parseQuantity(props["weight"], "token", addDiag, ref, "weight")
	if !w.Valid {
		w.Value = 1 // exploration is blocked at admissibility; default kept for graph rendering
	}
	if _, ok := props["inhib"]; ok {
		kind = domain.ArcInhibitor
	}
	return &domain.Arc{
		ID:       fmt.Sprintf("a%d", seq),
		Source:   src,
		Target:   dst,
		Weight:   w,
		Kind:     kind,
		StableID: fmt.Sprintf("arc-%d", seq),
	}
}
