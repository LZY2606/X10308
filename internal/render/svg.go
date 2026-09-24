// Package render draws SVG network diagrams, exact state-space graphs and
// Karp-Miller coverability trees. Every drawn value carries a stable id:
// node ids for places/transitions, state ids for markings, transition ids on
// edges; raw representations are shown next to derived values.
package render

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"tflocal/internal/domain"
)

const (
	placeFill      = "#eef6ff"
	placeStroke    = "#2b6cb0"
	transFill      = "#fff7e6"
	transStroke    = "#b7791f"
	goalFill       = "#c6f6d5"
	deadFill       = "#fed7d7"
	frontierFill   = "#fef3c7"
	omegaFill      = "#e9d8fd"
)

func esc(s string) string { return html.EscapeString(s) }

// NetSVG draws one scenario's Petri net from the stored coordinates.
func NetSVG(s *domain.Scenario, n *domain.Net, width, height int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg id="svg-net-%s" xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 820 420" data-basis="%s">`,
		esc(s.ID), width, height, esc(docBasis(s)))
	b.WriteString(`<defs><marker id="arr` + esc(s.ID) + `" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto"><path d="M0,0 L9,3 L0,6 Z" fill="#4a5568"/></marker>`)
	b.WriteString(`<marker id="arrI` + esc(s.ID) + `" markerWidth="12" markerHeight="12" refX="9" refY="3" orient="auto"><path d="M0,0 L9,3 L0,6 Z" fill="none" stroke="#c53030" stroke-width="1.4"/></marker></defs>`)
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)

	placeByID := map[string]*domain.Place{}
	for _, p := range s.Places {
		placeByID[p.ID] = p
	}
	transByID := map[string]*domain.Transition{}
	for _, t := range s.Transitions {
		transByID[t.ID] = t
	}
	// arcs first
	for _, a := range s.Arcs {
		var x1, y1, x2, y2 float64
		if p, ok := placeByID[a.From]; ok {
			x1, y1 = p.Coord.X, p.Coord.Y
			t := transByID[a.To]
			x2, y2 = t.Coord.X-28, t.Coord.Y
		} else {
			t := transByID[a.From]
			x1, y1 = t.Coord.X+28, t.Coord.Y
			p := placeByID[a.To]
			x2, y2 = p.Coord.X, p.Coord.Y
		}
		// shorten to place circle edge
		if _, ok := placeByID[a.To]; ok {
			x2, y2 = shortenToCircle(x1, y1, x2, y2, 26)
		}
		color, marker, dash := "#4a5568", "url(#arr"+esc(s.ID)+")", ""
		if a.Kind == domain.ArcInhibitor {
			color, marker, dash = "#c53030", "url(#arrI"+esc(s.ID)+")", ` stroke-dasharray="6,4"`
		}
		fmt.Fprintf(&b, `<line data-arc-id="%s" data-weight="%d" x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="%s" stroke-width="2"%s marker-end="%s"/>`,
			esc(a.ID), a.Weight, x1, y1, x2, y2, color, dash, marker)
		mx, my := (x1+x2)/2, (y1+y2)/2
		fmt.Fprintf(&b, `<g data-arc-label="%s"><rect x="%.0f" y="%.0f" width="26" height="18" rx="4" fill="#ffffff" stroke="#cbd5e0"/><text x="%.0f" y="%.0f" text-anchor="middle" font-size="12" fill="#2d3748">%d</text></g>`,
			esc(a.ID), mx-13, my-9, mx, my+4, a.Weight)
	}
	for _, p := range s.Places {
		cap := esc(capLabel(p))
		capUnit := "unit:capacity"
		if p.Capacity != nil {
			capUnit = string(p.Capacity.Unit)
		}
		fmt.Fprintf(&b, `<g data-node-id="%s" data-kind="place"><circle cx="%.0f" cy="%.0f" r="26" fill="%s" stroke="%s" stroke-width="2"/>`,
			esc(p.ID), p.Coord.X, p.Coord.Y, placeFill, placeStroke)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="11" fill="#1a365d">%s</text>`, p.Coord.X, p.Coord.Y+3, esc(p.Name))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="10" fill="#718096">id:%s</text>`, p.Coord.X, p.Coord.Y+40, esc(p.ID))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="10" fill="#4a5568">cap %s %s</text></g>`, p.Coord.X, p.Coord.Y-34, cap, esc(capUnit))
	}
	for _, t := range s.Transitions {
		fmt.Fprintf(&b, `<g data-node-id="%s" data-kind="transition"><rect x="%.0f" y="%.0f" width="56" height="26" rx="3" fill="%s" stroke="%s" stroke-width="2"/>`,
			esc(t.ID), t.Coord.X-28, t.Coord.Y-13, transFill, transStroke)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="10" fill="#7b341e">%s</text>`, t.Coord.X, t.Coord.Y+3, esc(t.Name))
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" font-size="9" fill="#a0aec0">id:%s</text></g>`, t.Coord.X, t.Coord.Y-20, esc(t.ID))
	}
	// initial token legend with raw values
	ly := 400
	bx := 20.0
	for _, rt := range s.Tokens {
		mark := esc(rt.Raw)
		color := "#1a202c"
		if !rt.ConvertOK {
			color = "#c53030"
		}
		fmt.Fprintf(&b, `<g data-token-place="%s"><text x="%.0f" y="%d" font-size="11" fill="%s">M0[%s]=%s %s</text></g>`,
			esc(rt.PlaceID), bx, ly, color, esc(rt.PlaceID), mark, esc(string(rt.Unit)))
		bx += 130
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func docBasis(s *domain.Scenario) string { return "doc-coordinate-system/1" }

func capLabel(p *domain.Place) string {
	if p.Capacity == nil {
		return "ω"
	}
	c := p.Capacity
	if !c.ConvertOK {
		return "raw:" + c.Raw + "(unconverted)"
	}
	if !c.Finite {
		return "ω"
	}
	return fmt.Sprintf("%d", c.Value)
}

func shortenToCircle(x1, y1, x2, y2, r float64) (float64, float64) {
	dx, dy := x2-x1, y2-y1
	l := (dx*dx + dy*dy)
	if l == 0 {
		return x2, y2
	}
	ll := sqrt2(l)
	return x2 - dx/ll*r, y2 - dy/ll*r
}

func sqrt2(v float64) float64 {
	// tiny newton to avoid math import churn
	x := v
	if x <= 0 {
		return 0
	}
	for i := 0; i < 12; i++ {
		x = (x + v/x) / 2
	}
	return x
}

// markText renders a concrete marking with units in canonical order.
func markText(m domain.Marking, ids []string) string {
	parts := []string{}
	keys := append([]string(nil), ids...)
	for k := range m {
		if !contains(keys, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ",")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
