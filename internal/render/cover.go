package render

import (
	"fmt"
	"sort"
	"strings"

	"tflocal/internal/domain"
	"tflocal/internal/engine"
)

func omegaText(m engine.OmegaMarking, placeIDs []string) string {
	keys := append([]string(nil), placeIDs...)
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == 0 {
			continue
		}
		if v == engine.Omega {
			parts = append(parts, k+"=ω")
		} else {
			parts = append(parts, fmt.Sprintf("%s=%d", k, v))
		}
	}
	if len(parts) == 0 {
		return "∅"
	}
	return strings.Join(parts, ",")
}

// CoverSVG draws the Karp-Miller tree with omega nodes and cover edges.
func CoverSVG(s *domain.Scenario, r *engine.CovResult) string {
	var b strings.Builder
	var placeIDs []string
	for _, p := range s.Places {
		placeIDs = append(placeIDs, p.ID)
	}
	maxDepth := 0
	byDepth := map[int][]*engine.CovNode{}
	pos := map[string][2]float64{}
	for _, nd := range r.Nodes {
		byDepth[nd.Depth] = append(byDepth[nd.Depth], nd)
		if nd.Depth > maxDepth {
			maxDepth = nd.Depth

		}
	}
	hcol := 190.0
	height := int(float64(maxDepth+1)*hcol) + 60
	fmt.Fprintf(&b, `<svg id="svg-cover-%s" xmlns="http://www.w3.org/2000/svg" width="1200" height="%d">`, s.ID, height)
	b.WriteString(`<defs><marker id="arrC" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto"><path d="M0,0 L9,3 L0,6 Z" fill="#6b46c1"/></marker></defs>`)
	b.WriteString(`<rect width="100%" height="100%" fill="#faf5ff"/>`)
	for d := 0; d <= maxDepth; d++ {
		col := byDepth[d]
		sort.Slice(col, func(i, j int) bool { return col[i].ID < col[j].ID })
		for i, nd := range col {
			x := 80 + float64(i%5)*230
			y := 40 + float64(d)*hcol
			pos[nd.ID] = [2]float64{x, y}
		}
	}
	for _, e := range r.Edges {
		p1, ok1 := pos[e.FromID]
		p2, ok2 := pos[e.ToID]
		if !ok1 || !ok2 {
			continue
		}
		if e.FromID == e.ToID {
			continue
		}
		fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="#9f7aea" stroke-width="1.5" marker-end="url(#arrC)"/>`,
			p1[0], p1[1]+28, p2[0], p2[1]-28)
		mx, my := (p1[0]+p2[0])/2, (p1[1]+p2[1])/2
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="10" fill="#553c9a">%s</text>`, mx, my, esc(e.Transition))
	}
	// cover pairs drawn as dashed annotation between ids
	for i, cp := range r.CoverPairs {
		fmt.Fprintf(&b, `<text x="20" y="%d" font-size="11" fill="#6b46c1" data-cover-pair="%d">覆盖: %s ⊱ %s (ω: %s)</text>`,
			height-70-i*16, i, esc(cp.CoveringID), esc(cp.CoveredID), esc(strings.Join(cp.Places, ",")))
	}
	for _, nd := range r.Nodes {
		p := pos[nd.ID]
		x, y := p[0], p[1]
		fill := "#ffffff"
		switch {
		case len(nd.OmegaPlaces) > 0:
			fill = omegaFill
		case nd.Deadlock:
			fill = deadFill
		case nd.CoversGoal:
			fill = goalFill
		case nd.Frontier:
			fill = frontierFill
		}
		tag := ""
		if len(nd.OmegaPlaces) > 0 {
			tag = " OMEGA"
		}
		if nd.Duplicate {
			tag += " DUP"
		}
		if nd.Deadlock {
			tag += " DEADLOCK"
		}
		if nd.Frontier {
			tag += " FRONTIER"
		}
		if nd.CoversGoal {
			tag += " COVERS-GOAL"
		}
		fmt.Fprintf(&b, `<g data-cov-state-id="%s" data-seq="%d">`, esc(nd.ID), nd.Seq)
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="200" height="56" rx="8" fill="%s" stroke="#6b46c1"/>`, x-100, y-28, fill)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="10" fill="#553c9a">%s #%d d=%d</text>`, x-92, y-14, esc(nd.ID), nd.Seq, nd.Depth)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="11" fill="#1a202c">%s token</text>`, x-92, y+4, esc(omegaText(nd.Marking, placeIDs)))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="9" fill="#4a5568">%s</text>`, x-92, y+20, esc(tag))
		b.WriteString(`</g>`)
	}
	fmt.Fprintf(&b, `<text x="20" y="%d" font-size="12" fill="#2d3748">unbounded=%v truncated=%v steps_used=%d/%d nodes=%d frontier=%d</text>`,
		height-16, r.Unbounded, r.Truncated, r.Fired, r.Budget, len(r.Nodes), len(r.Frontier))
	b.WriteString(`</svg>`)
	return b.String()
}
