package petri

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RenderSVG writes a deterministic SVG view of the net with an optional
// marking overlay (token dots, token counts and capacity usage).
// Coordinates use the canvas-0..1000 basis and are scaled to a 1000x620 view.
func RenderSVG(w io.Writer, n *Net, m Marking, highlightPath map[string]bool) error {
	n.Index()
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1000 640" font-family="Helvetica,Arial,sans-serif" font-size="13">`)
	b.WriteString(`<defs><marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L9,3 L0,6 Z" fill="#334"/></marker></defs>`)
	b.WriteString(`<rect width="1000" height="640" fill="#fafafa"/>`)

	type xy struct{ x, y float64 }
	ppos := map[string]xy{}
	tpos := map[string]xy{}
	for _, p := range n.Places {
		ppos[p.ID] = xy{float64(p.X), float64(p.Y)}
	}
	for _, t := range n.Transitions {
		tpos[t.ID] = xy{float64(t.X), float64(t.Y)}
	}

	// Arcs first (under nodes).
	for _, a := range n.Arc {
		from, ok1 := ppos[a.Place]
		to, ok2 := tpos[a.Trans]
		if a.Kind == "out" {
			from, to = to, from
		}
		if !ok1 || !ok2 {
			continue
		}
		edgeClass := ""
		if highlightPath[a.Trans] {
			edgeClass = ` class="onpath"`
		}
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#556" stroke-width="1.6" marker-end="url(#arrow)"%s/>`,
			from.x, from.y, to.x, to.y, edgeClass)
		mx, my := (from.x+to.x)/2, (from.y+to.y)/2
		if a.Weight > 1 {
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="20" height="16" rx="3" fill="#fff" stroke="#556"/><text x="%.1f" y="%.1f" text-anchor="middle" dominant-baseline="central" font-weight="bold">%d</text>`,
				mx-10, my-8, mx, my+1, a.Weight)
		}
	}
	// Inhibitor arcs: hollow circle head, no token flow.
	for _, ih := range n.Inhibitors {
		from, ok1 := ppos[ih.Place]
		to, ok2 := tpos[ih.Trans]
		if !ok1 || !ok2 {
			continue
		}
		// stop the line at the circle head
		dx, dy := to.x-from.x, to.y-from.y
		l := norm(dx, dy)
		hx, hy := to.x-dx/l*12, to.y-dy/l*12
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#a23" stroke-width="1.6" stroke-dasharray="5,4"/>`,
			from.x, from.y, hx, hy)
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="6" fill="#fff" stroke="#a23" stroke-width="1.6"/>`, hx, hy)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" fill="#a23" font-size="11">inh≥%d</text>`,
			(from.x+hx)/2, (from.y+hy)/2-8, ih.Threshold)
	}

	// Places (circles).
	for _, p := range n.Places {
		pos := ppos[p.ID]
		cap := ""
		if p.Capacity >= 0 {
			cap = fmt.Sprintf(` / %d`, p.Capacity)
		}
		tok := int64(0)
		if m != nil {
			tok = m[p.ID]
		}
		fill := "#fff"
		if p.Capacity >= 0 && tok > p.Capacity {
			fill = "#ffe2e2"
		}
		fmt.Fprintf(&b, `<circle cx="%.0f" cy="%.0f" r="26" fill="%s" stroke="#234" stroke-width="2"><title>%s: %d token%s (capacity %s)</title></circle>`,
			pos.x, pos.y, fill, p.ID, tok, plural(tok), capText(p.Capacity))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" dominant-baseline="central" font-weight="bold">%d</text>`,
			pos.x, pos.y, tok)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle">%s<span class="unit"></span></text>`,
			pos.x, pos.y-40, escape(p.Label))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" fill="#667" font-size="11">%s [%d%s tok]</text>`,
			pos.x, pos.y+44, p.ID, tok, cap)
	}

	// Transitions (rectangles).
	for _, t := range n.Transitions {
		pos := tpos[t.ID]
		cls := ""
		if highlightPath[t.ID] {
			cls = ` fill="#cfe8ff" stroke="#06c"`
		} else {
			cls = ` fill="#234" stroke="#234"`
		}
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="16" height="44" rx="2"%s><title>%s</title></rect>`,
			pos.x-8, pos.y-22, cls, t.ID)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" fill="#234" font-size="11">%s</text>`,
			pos.x, pos.y-34, escape(t.Label))
	}
	b.WriteString(`</svg>`)
	_, err := io.WriteString(w, b.String())
	return err
}

func norm(x, y float64) float64 {
	l := x*x + y*y
	if l < 1e-9 {
		return 1
	}
	return sqrt(l)
}

// avoid math import churn
func sqrt(x float64) float64 {
	// Newton
	z := x
	for i := 0; i < 12; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func capText(c int64) string {
	if c < 0 {
		return "unbounded"
	}
	return strconv.FormatInt(c, 10)
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
