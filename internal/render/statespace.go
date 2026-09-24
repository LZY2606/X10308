package render

import (
	"fmt"
	"sort"
	"strings"

	"tflocal/internal/domain"
	"tflocal/internal/engine"
)

// StateSpaceSVG lays out exact BFS states by depth and draws firing edges.
// Node fill encodes goal/deadlock/frontier; every node keeps its stable
// state id and canonical marking visible.
func StateSpaceSVG(s *domain.Scenario, r *engine.Result) string {
	var b strings.Builder
	var placeIDs []string
	for _, p := range s.Places {
		placeIDs = append(placeIDs, p.ID)
	}
	maxDepth := 0
	byDepth := map[int][]*engine.State{}
	for _, st := range r.States {
		byDepth[st.Depth] = append(byDepth[st.Depth], st)
		if st.Depth > maxDepth {
			maxDepth = st.Depth
		}
	}
	width := 1200
	hcol := 210.0
	height := int(float64(maxDepth+1)*hcol) + 60
	fmt.Fprintf(&b, `<svg id="svg-space-%s" xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" data-run="%s" data-outcome="%s">`,
		s.ID, width, height, esc(s.ID), string(r.Outcome))
	b.WriteString(`<defs><marker id="arrS" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto"><path d="M0,0 L9,3 L0,6 Z" fill="#718096"/></marker></defs>`)
	b.WriteString(`<rect width="100%" height="100%" fill="#fafafa"/>`)

	pos := map[string][2]float64{}
	for d := 0; d <= maxDepth; d++ {
		col := byDepth[d]
		sort.Slice(col, func(i, j int) bool { return col[i].ID < col[j].ID })
		for i, st := range col {
			x := 80 + float64(i)*260
			y := 40 + float64(d)*hcol
			pos[st.ID] = [2]float64{x, y}
		}
	}
	// edges
	for _, e := range r.Edges {
		p1, ok1 := pos[e.FromID]
		p2, ok2 := pos[e.ToID]
		if !ok1 || !ok2 {
			continue
		}
		self := e.FromID == e.ToID
		if self {
			fmt.Fprintf(&b, `<path d="M %.0f %.0f C %.0f %.0f, %.0f %.0f, %.0f %.0f" fill="none" stroke="#3182ce" stroke-width="1.6" marker-end="url(#arrS)"/>`,
				p1[0]-60, p1[1], p1[0]-140, p1[1]-70, p1[0]-140, p1[1]+30, p1[0]-60, p1[1])
		} else {
			fmt.Fprintf(&b, `<line data-edge-from="%s" data-edge-to="%s" x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="#718096" stroke-width="1.5" marker-end="url(#arrS)"/>`,
				esc(e.FromID), esc(e.ToID), p1[0], p1[1]+30, p2[0], p2[1]-30)
			mx, my := (p1[0]+p2[0])/2, (p1[1]+p2[1])/2
			fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="92" height="16" rx="3" fill="#ffffff"/><text x="%.0f" y="%.0f" font-size="10" fill="#2c5282">%s</text>`,
				mx-46, my-8, mx, my+4, esc(e.Transition))
		}
	}
	// nodes
	for _, st := range r.States {
		p := pos[st.ID]
		x, y := p[0], p[1]
		fill := "#ffffff"
		switch {
		case st.IsGoal:
			fill = goalFill
		case st.Deadlock:
			fill = deadFill
		case st.Frontier:
			fill = frontierFill
		}
		fmt.Fprintf(&b, `<g data-state-id="%s" data-seq="%d" data-depth="%d" data-canon="%s">`,
			esc(st.ID), st.Seq, st.Depth, esc(st.Canon))
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="200" height="60" rx="8" fill="%s" stroke="#4a5568" stroke-width="1.5"/>`,
			x-100, y-30, fill)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="10" fill="#4a5568">%s #%d depth=%d</text>`,
			x-92, y-16, esc(st.ID), st.Seq, st.Depth)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="11" fill="#1a202c">%s</text>`,
			x-92, y+2, esc(markText(st.Marking, placeIDs))+" token")
		tags := ""
		if st.IsGoal {
			tags += " GOAL"
		}
		if st.Deadlock {
			tags += " DEADLOCK"
		}
		if st.Frontier {
			tags += " FRONTIER"
		}
		if tags != "" {
			fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="10" font-weight="bold" fill="#22543d">%s</text>`, x-92, y+20, tags)
		}
		b.WriteString(`</g>`)
	}
	fmt.Fprintf(&b, `<text x="20" y="%d" font-size="12" fill="#2d3748">outcome=%s steps_used=%d/%d truncated=%v frontier=%d deadlocks=%d</text>`,
		height-14, string(r.Outcome), r.Fired, r.Budget, r.Truncated, len(r.Frontier), len(r.DeadlockIDs))
	b.WriteString(`</svg>`)
	return b.String()
}
