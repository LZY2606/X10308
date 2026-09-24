package petri

import "fmt"

// Diagnostic records a value that could not be normalized into the canonical
// unit/coordinate basis. The raw value stays in place; nothing is dropped for
// the sake of rendering or computation.
type Diagnostic struct {
	Object  string `json:"object"`  // e.g. "place:p_buf.capacity"
	Field   string `json:"field"`   // e.g. "capacity"
	Raw     string `json:"raw"`     // raw representation as imported
	Unit    string `json:"unit"`    // unrecognized unit / basis
	Message string `json:"message"` // human readable explanation
}

// NormalizeQuantity converts a token quantity expressed in unit into
// canonical tokens. Supported: "token", "" (defaults to token), "dozen".
// Unknown units yield ok=false and a diagnostic; callers keep the raw value.
func NormalizeQuantity(v int, unit string) (int, bool) {
	switch unit {
	case "", "token":
		return v, true
	case "dozen":
		return v * 12, true
	}
	return v, false
}

// NormalizeLength converts a coordinate expressed in the given basis into
// canonical px. Supported: "px", "" (defaults to px), "mm".
func NormalizeLength(v float64, unit string) (float64, bool) {
	switch unit {
	case "", "px":
		return v, true
	case "mm":
		return v * 96.0 / 25.4, true
	}
	return v, false
}

// DerivedPlace is the normalized view of a Place used by exploration and
// rendering. CapacityPx... Capacity is in canonical tokens; X/Y in px.
// RawCapacity/RawUnit keep the original representation for display.
type DerivedPlace struct {
	Place
	RawCapacity int     `json:"raw_capacity"`
	RawX        float64 `json:"raw_x"`
	RawY        float64 `json:"raw_y"`
}

// DerivedNet is the derived layer: units and coordinate bases normalized,
// diagnostics attached, raw values preserved on the side.
type DerivedNet struct {
	*Net
	Places      []DerivedPlace `json:"places"`
	Diagnostics []Diagnostic   `json:"diagnostics"`
}

// Derive normalizes a raw net. Values that cannot be converted stay at their
// raw position and produce a diagnostic; they are never silently dropped.
func Derive(n *Net) *DerivedNet {
	d := &DerivedNet{Net: n}
	for _, p := range n.Places {
		dp := DerivedPlace{Place: p, RawCapacity: p.Capacity, RawX: p.X, RawY: p.Y}
		if cap2, ok := NormalizeQuantity(p.Capacity, p.Unit); ok {
			dp.Capacity = cap2
		} else {
			d.Diagnostics = append(d.Diagnostics, Diagnostic{
				Object:  "place:" + p.ID + ".capacity",
				Field:   "capacity",
				Raw:     fmt.Sprintf("%d", p.Capacity),
				Unit:    p.Unit,
				Message: fmt.Sprintf("unit %q not convertible to token; raw capacity %d kept in place", p.Unit, p.Capacity),
			})
		}
		if x, ok := NormalizeLength(p.X, p.CoordUnit); ok {
			dp.X = x
		} else {
			d.Diagnostics = append(d.Diagnostics, Diagnostic{
				Object:  "place:" + p.ID + ".x",
				Field:   "x",
				Raw:     fmt.Sprintf("%g", p.X),
				Unit:    p.CoordUnit,
				Message: fmt.Sprintf("coordinate basis %q unknown; raw x=%g kept in place", p.CoordUnit, p.X),
			})
		}
		if y, ok := NormalizeLength(p.Y, p.CoordUnit); ok {
			dp.Y = y
		} else {
			d.Diagnostics = append(d.Diagnostics, Diagnostic{
				Object:  "place:" + p.ID + ".y",
				Field:   "y",
				Raw:     fmt.Sprintf("%g", p.Y),
				Unit:    p.CoordUnit,
				Message: fmt.Sprintf("coordinate basis %q unknown; raw y=%g kept in place", p.CoordUnit, p.Y),
			})
		}
		d.Places = append(d.Places, dp)
	}
	return d
}

// ExplorationNet returns a *Net whose place capacities come from the derived
// (normalized) layer, for use by the explorer.
func (d *DerivedNet) ExplorationNet() *Net {
	out := *d.Net
	out.Places = make([]Place, len(d.Places))
	for i, dp := range d.Places {
		out.Places[i] = dp.Place
	}
	return &out
}
