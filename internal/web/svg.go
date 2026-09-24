// Package web renders Petri nets to local SVG and serves the review UI/API.
package web

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"pnwork/internal/pnnet"
)

// SVG renders a net graph with the current marking highlighted.
//
// Coordinates keep their svg-user-unit basis. When a coordinate is
// unconvertible it is not dropped: the node is placed by an automatic fallback
// and marked with a small warning glyph carrying the raw text.
func SVG(n *pnnet.Net, m pnnet.Marking) string {
	placePos, transPos := layout(n)
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 860 540" `)
	b.WriteString(`font-family="Helvetica,Arial,sans-serif" font-size="12">`)
	b.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" `)
	b.WriteString(`markerWidth="7" markerHeight="7" orient="auto-start-reverse">`)
	b.WriteString(`<path d="M0,0 L10,5 L0,10 z" fill="#475569"/></marker>`)
	b.WriteString(`<marker id="inh" viewBox="0 0 10 10" refX="5" refY="5" `)
	b.WriteString(`markerWidth="9" markerHeight="9"><circle cx="5" cy="5" r="4" fill="#fff" stroke="#b91c1c" stroke-width="1.5"/></marker>`)
	b.WriteString(`</defs>`)
	b.WriteString(`<rect width="860" height="540" fill="#f8fafc"/>`)

	// Arcs under nodes.
	for _, a := range n.Arcs {
		var x1, y1, x2, y2 float64
		if pp, ok := placePos[a.From]; ok {
			x1, y1 = pp.x, pp.y
		} else if tp, ok := transPos[a.From]; ok {
			x1, y1 = tp.x, tp.y
		}
		if pp, ok := placePos[a.To]; ok {
			x2, y2 = pp.x, pp.y
		} else if tp, ok := transPos[a.To]; ok {
			x2, y2 = tp.x, tp.y
		}
		marker := "arrow"
		stroke := "#475569"
		if a.Kind == pnnet.ArcInhibitor {
			marker = "inh"
			stroke = "#b91c1c"
		}
		dash := ""
		weightLabel := "1"
		if a.Weight.Value != nil {
			weightLabel = strconv.FormatInt(*a.Weight.Value, 10)
		} else {
			dash = ` stroke-dasharray="4 3"`
			weightLabel = "?" + html.EscapeString(a.Weight.Text)
		}
		mx, my := (x1+x2)/2, (y1+y2)/2
		fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="%s" stroke-width="1.6"%s marker-end="url(#%s)"/>`+"\n",
			x1, y1, x2, y2, stroke, dash, marker)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" fill="#334155">%s</text>`+"\n",
			mx, my-4, html.EscapeString(weightLabel))
	}

	// Places.
	placeIDs := n.SortedPlaceIDs()
	for _, pid := range placeIDs {
		p := placeByID(n, pid)
		pos := placePos[pid]
		tokens := m.Get(pid)
		capText := ""
		if p.HasCapacity() {
			capText = " cap=" + strconv.FormatInt(*p.Capacity.Value, 10)
		} else if p.Capacity.Text != "" && len(p.Capacity.Diagnostics) > 0 {
			capText = " cap=?" + html.EscapeString(p.Capacity.Text)
		} else if p.Capacity.Text != "" {
			capText = " cap=" + html.EscapeString(p.Capacity.Text)
		}
		fmt.Fprintf(&b, `<g data-stable-id="%s">`, html.EscapeString(pid))
		fmt.Fprintf(&b, `<circle cx="%.0f" cy="%.0f" r="20" fill="#ffffff" stroke="#0f766e" stroke-width="2"/>`, pos.x, pos.y)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" dominant-baseline="middle" font-weight="bold">%d</text>`, pos.x, pos.y, tokens)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle">%s%s</text>`, pos.x, pos.y+34,
			html.EscapeString(displayLabel(pid, p.Label)), html.EscapeString(capText))
		if len(p.X.Diagnostics) > 0 || len(p.Y.Diagnostics) > 0 || len(p.Capacity.Diagnostics) > 0 {
			fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" fill="#b91c1c">⚠</text>`, pos.x-26, pos.y-16)
		}
		b.WriteString(`</g>` + "\n")
	}

	// Transitions.
	for _, tid := range n.SortedTransitionIDs() {
		t := transByID(n, tid)
		pos := transPos[tid]
		enabled, reasons := n.Enabled(tid, m)
		stroke := "#1d4ed8"
		if !enabled {
			stroke = "#94a3b8"
		}
		fmt.Fprintf(&b, `<g data-stable-id="%s" data-enabled="%v">`, html.EscapeString(tid), enabled)
		fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="46" height="22" rx="3" fill="#dbeafe" stroke="%s" stroke-width="2"/>`,
			pos.x-23, pos.y-11, stroke)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="middle" dominant-baseline="middle">%s</text>`,
			pos.x, pos.y, html.EscapeString(displayLabel(tid, t.Label)))
		fmt.Fprintf(&b, `<title>%s</title>`, html.EscapeString(enablementTitle(tid, enabled, reasons)))
		b.WriteString(`</g>` + "\n")
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func enablementTitle(tid string, enabled bool, reasons []pnnet.BlockReason) string {
	if enabled {
		return tid + ": enabled"
	}
	parts := []string{tid + ": blocked"}
	for _, r := range reasons {
		parts = append(parts, r.Code+"@"+r.Place)
	}
	return strings.Join(parts, "; ")
}

func displayLabel(id, label string) string {
	if label != "" {
		return label
	}
	return id
}

type xy struct{ x, y float64 }

func placeByID(n *pnnet.Net, id string) pnnet.Place {
	for _, p := range n.Places {
		if p.ID == id {
			return p
		}
	}
	return pnnet.Place{ID: id}
}

func transByID(n *pnnet.Net, id string) pnnet.Transition {
	for _, t := range n.Transitions {
		if t.ID == id {
			return t
		}
	}
	return pnnet.Transition{ID: id}
}

// layout computes node positions. Convertible raw coordinates are honored
// exactly; unconvertible ones fall back onto a deterministic grid so drawing
// never discards the node.
func layout(n *pnnet.Net) (map[string]xy, map[string]xy) {
	places := map[string]xy{}
	trans := map[string]xy{}
	fallbackPX, fallbackPY := 60.0, 480.0
	for _, p := range n.Places {
		if p.X.Value != nil && p.Y.Value != nil {
			places[p.ID] = xy{float64(*p.X.Value), float64(*p.Y.Value)}
		} else {
			places[p.ID] = xy{fallbackPX, fallbackPY}
			fallbackPX += 70
		}
	}
	fallbackTX, fallbackTY := 60.0, 420.0
	for _, t := range n.Transitions {
		if t.X.Value != nil && t.Y.Value != nil {
			trans[t.ID] = xy{float64(*t.X.Value), float64(*t.Y.Value)}
		} else {
			trans[t.ID] = xy{fallbackTX, fallbackTY}
			fallbackTX += 90
		}
	}
	return places, trans
}

// StableIDs returns every node/arc id in stable order for the UI identity
// check.
func StableIDs(n *pnnet.Net) map[string][]string {
	return map[string][]string{
		"places":      append([]string{}, n.SortedPlaceIDs()...),
		"transitions": append([]string{}, n.SortedTransitionIDs()...),
	}
}

var _ = sort.Strings
