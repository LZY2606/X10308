package web

import (
	"fmt"
	"html"
	"math"
	"strings"

	"petriws/internal/petri"
)

// RenderNetSVG draws the Petri net. Places carry token counts of the
// selected marking; inhibitor arcs end in a small circle. Coordinates
// come from the model on the "px" basis.
func RenderNetSVG(net *petri.Net, marking petri.Marking) string {
	var b strings.Builder
	maxX, maxY := 0, 0
	for _, p := range net.Places {
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	for _, t := range net.Transitions {
		if t.X > maxX {
			maxX = t.X
		}
		if t.Y > maxY {
			maxY = t.Y
		}
	}
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="net" xmlns="http://www.w3.org/2000/svg">`, maxX+120, maxY+120)
	b.WriteString(`<defs><marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#555"/></marker></defs>`)
	pos := map[string][2]int{}
	for _, p := range net.Places {
		pos[p.ID] = [2]int{p.X, p.Y}
	}
	for _, t := range net.Transitions {
		pos[t.ID] = [2]int{t.X, t.Y}
	}
	for _, a := range net.Arcs {
		from, to := pos[a.From], pos[a.To]
		x1, y1 := float64(from[0]), float64(from[1])
		x2, y2 := float64(to[0]), float64(to[1])
		dx, dy := x2-x1, y2-y1
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		ux, uy := dx/l, dy/l
		sx, sy := x1+ux*18, y1+uy*18
		ex, ey := x2-ux*16, y2-uy*16
		if a.Kind == petri.ArcInhibitor {
			fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="#b03030" stroke-width="1.5"/>`, sx, sy, ex, ey)
			fmt.Fprintf(&b, `<circle cx="%.0f" cy="%.0f" r="4" fill="#fff" stroke="#b03030" stroke-width="1.5"/>`, ex, ey)
		} else {
			fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="#555" stroke-width="1.5" marker-end="url(#arr)"/>`, sx, sy, ex, ey)
		}
		if a.Weight > 1 {
			fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" class="w">%d</text>`, (sx+ex)/2+6, (sy+ey)/2-4, a.Weight)
		}
	}
	for _, p := range net.Places {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="16" class="place"/>`, p.X, p.Y)
		tokens := marking[p.ID]
		label := fmt.Sprintf("%d", tokens)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="tok">%s</text>`, p.X, p.Y+4, label)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="lbl">%s</text>`, p.X, p.Y+32, html.EscapeString(p.ID))
		if p.Capacity != nil {
			fmt.Fprintf(&b, `<text x="%d" y="%d" class="cap">≤%d</text>`, p.X, p.Y-24, *p.Capacity)
		}
	}
	for _, t := range net.Transitions {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="10" height="26" class="trans"/>`, t.X-5, t.Y-13)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="lbl">%s</text>`, t.X, t.Y+40, html.EscapeString(t.ID))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// RenderStateGraphSVG draws the reachability graph layered by depth.
func RenderStateGraphSVG(res *petri.Result, runID, selected string) string {
	byDepth := map[int][]petri.State{}
	maxDepth := 0
	for _, s := range res.States {
		byDepth[s.Depth] = append(byDepth[s.Depth], s)
		if s.Depth > maxDepth {
			maxDepth = s.Depth
		}
	}
	pos := map[string][2]int{}
	for d := 0; d <= maxDepth; d++ {
		states := byDepth[d]
		for i, s := range states {
			pos[s.ID] = [2]int{60 + d*150, 50 + i*64}
		}
	}
	maxCount := 0
	for _, states := range byDepth {
		if len(states) > maxCount {
			maxCount = len(states)
		}
	}
	w := 60 + maxDepth*150 + 160
	h := 50 + maxCount*64 + 40
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="graph" xmlns="http://www.w3.org/2000/svg">`, w, h)
	b.WriteString(`<defs><marker id="arr2" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#888"/></marker></defs>`)
	for _, e := range res.Edges {
		a, c := pos[e.From], pos[e.To]
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#888" marker-end="url(#arr2)"/>`, a[0]+14, a[1], c[0]-14, c[1])
		mx, my := (a[0]+c[0])/2, (a[1]+c[1])/2-4
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="w">%s</text>`, mx, my, html.EscapeString(e.Transition))
	}
	frontier := map[string]bool{}
	for _, f := range res.Frontier {
		frontier[f] = true
	}
	for _, s := range res.States {
		p := pos[s.ID]
		cls := "state"
		if s.ID == selected {
			cls += " sel"
		}
		if s.Deadlock {
			cls += " dead"
		}
		if frontier[s.ID] {
			cls += " front"
		}
		fmt.Fprintf(&b, `<a href="/run?id=%s&amp;state=%s"><circle cx="%d" cy="%d" r="14" class="%s"/>`, runID, s.ID, p[0], p[1], cls)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="tok">%s</text></a>`, p[0], p[1]+4, html.EscapeString(s.ID))
	}
	b.WriteString(`</svg>`)
	return b.String()
}
