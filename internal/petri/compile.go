package petri

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParseQty normalizes a textual quantity at the derived layer. The raw text
// is preserved by the caller. "∞"/"inf" means unbounded; anything that cannot
// be parsed stays in place with a diagnostic instead of being discarded.
func ParseQty(raw, unit, path, objectID string) RawQty {
	q := RawQty{Raw: raw, Unit: unit}
	s := strings.TrimSpace(raw)
	switch s {
	case "∞", "inf", "infinity", "*", "":
		if s == "" {
			q.Diagnostic = Diagnostic{
				Code:     "missing-value",
				ObjectID: objectID,
				Path:     path,
				Message:  "quantity has no representation; original value kept in place",
			}.Message
			return q
		}
		q.Unbounded = true
		return q
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			q.Diagnostic = fmt.Sprintf("negative quantity %q not allowed for %s", raw, path)
			return q
		}
		q.Value = &n
		return q
	}
	q.Diagnostic = fmt.Sprintf("cannot convert %q to a token count for %s; value retained", raw, path)
	return q
}

// Compile builds the normalized model from a bundle. Structural errors
// (unknown nodes, duplicate identities) set Blocked=true; unconvertible
// quantities are recorded as diagnostics without dropping the raw value.
func Compile(b Bundle) *Compiled {
	c := &Compiled{
		BundleID:     b.BundleID,
		ModelVersion: b.ModelVersion,
		Name:         b.Model.Name,
		Narrative:    b.Model.Narrative,
		SectionVer: map[string]string{
			"model":      b.Model.Version,
			"nodes":      b.Nodes.Version,
			"arcs":       b.Arcs.Version,
			"tokens":     b.Tokens.Version,
			"capacities": b.Capacities.Version,
			"strategy":   b.Strategy.Version,
			"budget":     b.Budget.Version,
		},
		Places:      map[string]*PlaceInfo{},
		Transitions: map[string]Node{},
		ArcByID:     map[string]Arc{},
		InputArc:    map[string][]Arc{},
		OutputArc:   map[string][]Arc{},
		Aliases:     map[string]string{},
		Initial:     map[string]int{},
	}
	addDiag := func(code, id, path, msg string) {
		c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: code, ObjectID: id, Path: path, Message: msg})
	}

	nodeIDs := map[string]bool{}
	for _, n := range b.Nodes.Nodes {
		if n.ID == "" {
			c.Blocked = true
			addDiag("empty-node-id", "", "nodes", "a node has no identity")
			continue
		}
		if nodeIDs[n.ID] {
			c.Blocked = true
			addDiag("duplicate-node-id", n.ID, "nodes", fmt.Sprintf("duplicate node identity %q", n.ID))
			continue
		}
		nodeIDs[n.ID] = true
		switch n.Kind {
		case KindPlace:
			c.Places[n.ID] = &PlaceInfo{Node: n, InitRaw: ParseQty("0", b.Tokens.Unit, "tokens", n.ID)}
			c.PlaceOrder = append(c.PlaceOrder, n.ID)
		case KindTransition:
			c.Transitions[n.ID] = n
			c.TransOrder = append(c.TransOrder, n.ID)
		default:
			c.Blocked = true
			addDiag("unknown-node-kind", n.ID, "nodes", fmt.Sprintf("node %q has kind %q", n.ID, n.Kind))
		}
	}

	capByPlace := map[string]PlaceState{}
	for _, st := range b.Capacities.States {
		capByPlace[st.Place] = st
	}
	for _, pid := range c.PlaceOrder {
		pi := c.Places[pid]
		if st, ok := capByPlace[pid]; ok {
			pi.CapRaw = ParseQty(st.Capacity.Raw, b.Capacities.Unit, "capacities", pid)
			pi.CapRaw.Unit = firstNonEmpty(pi.CapRaw.Unit, b.Capacities.Unit)
			switch {
			case pi.CapRaw.Unbounded:
				pi.Unbounded = true
			case pi.CapRaw.Value != nil:
				pi.Capacity = pi.CapRaw.Value
				if !pi.CapRaw.Unbounded && pi.CapRaw.Diagnostic != "" {
					addDiag("unconvertible-capacity", pid, "capacities", pi.CapRaw.Diagnostic)
				}
			default:
				addDiag("unconvertible-capacity", pid, "capacities", pi.CapRaw.Diagnostic)
			}
		} else {
			pi.Unbounded = true
			pi.CapRaw = ParseQty("∞", b.Capacities.Unit, "capacities", pid)
		}
	}

	tokByPlace := map[string]PlaceState{}
	for _, st := range b.Tokens.Tokens {
		tokByPlace[st.Place] = st
	}
	for _, pid := range c.PlaceOrder {
		pi := c.Places[pid]
		if st, ok := tokByPlace[pid]; ok {
			pi.HasInit = true
			pi.InitRaw = ParseQty(st.Tokens.Raw, b.Tokens.Unit, "tokens", pid)
			pi.InitRaw.Unit = firstNonEmpty(pi.InitRaw.Unit, b.Tokens.Unit)
			switch {
			case pi.InitRaw.Value != nil:
				pi.Init = *pi.InitRaw.Value
				c.Initial[pid] = pi.Init
			default:
				addDiag("unconvertible-tokens", pid, "tokens", pi.InitRaw.Diagnostic)
			}
		}
	}

	arcIDs := map[string]bool{}
	for _, a := range b.Arcs.Arcs {
		if a.ID == "" {
			c.Blocked = true
			addDiag("empty-arc-id", "", "arcs", "an arc has no identity")
			continue
		}
		if arcIDs[a.ID] {
			c.Blocked = true
			addDiag("duplicate-arc-id", a.ID, "arcs", fmt.Sprintf("duplicate arc identity %q", a.ID))
			continue
		}
		arcIDs[a.ID] = true
		from, ok1 := resolveNode(a.From, c)
		to, ok2 := resolveNode(a.To, c)
		if !ok1 {
			c.Blocked = true
			addDiag("unknown-arc-endpoint", a.ID, "arcs", fmt.Sprintf("arc %s source %q is not a node", a.ID, a.From))
		}
		if !ok2 {
			c.Blocked = true
			addDiag("unknown-arc-endpoint", a.ID, "arcs", fmt.Sprintf("arc %s target %q is not a node", a.ID, a.To))
		}
		if !ok1 || !ok2 {
			continue
		}
		w := ParseQty(a.WeightRaw, "", "arcs", a.ID)
		weight := 1
		switch {
		case w.Value != nil:
			weight = *w.Value
		case w.Unbounded:
			addDiag("unbounded-arc-weight", a.ID, "arcs", fmt.Sprintf("arc %s weight %q is invalid; weight kept raw", a.ID, a.WeightRaw))
			c.Blocked = true
		default:
			addDiag("unconvertible-arc-weight", a.ID, "arcs", w.Diagnostic)
			c.Blocked = true
		}
		compiled := Arc{ID: a.ID, From: from, To: to, Weight: weight, Inhibitor: a.Inhibitor}
		c.Arcs = append(c.Arcs, compiled)
		c.ArcByID[a.ID] = compiled
		_, isPFrom := c.Places[from]
		_, isTTo := c.Transitions[to]
		_, isTFrom := c.Transitions[from]
		_, isPTo := c.Places[to]
		if a.Inhibitor {
			if isPFrom && isTTo {
				c.InputArc[to] = append(c.InputArc[to], compiled)
			} else {
				c.Blocked = true
				addDiag("bad-inhibitor-arc", a.ID, "arcs", fmt.Sprintf("inhibitor arc %s must go place -> transition", a.ID))
			}
			continue
		}
		switch {
		case isPFrom && isTTo:
			c.InputArc[to] = append(c.InputArc[to], compiled)
		case isTFrom && isPTo:
			c.OutputArc[from] = append(c.OutputArc[from], compiled)
		default:
			c.Blocked = true
			addDiag("bad-arc-direction", a.ID, "arcs", fmt.Sprintf("arc %s must connect place and transition", a.ID))
		}
	}

	// Arc lists are ordered by identity so derived results are deterministic.
	for t := range c.InputArc {
		sort.Slice(c.InputArc[t], func(i, j int) bool { return c.InputArc[t][i].ID < c.InputArc[t][j].ID })
	}
	for t := range c.OutputArc {
		sort.Slice(c.OutputArc[t], func(i, j int) bool { return c.OutputArc[t][i].ID < c.OutputArc[t][j].ID })
	}
	sort.Strings(c.PlaceOrder)
	sort.Strings(c.TransOrder)
	sort.Slice(c.Arcs, func(i, j int) bool { return c.Arcs[i].ID < c.Arcs[j].ID })
	return c
}

func resolveNode(ref string, c *Compiled) (string, bool) {
	if _, ok := c.Places[ref]; ok {
		return ref, true
	}
	if _, ok := c.Transitions[ref]; ok {
		return ref, true
	}
	return "", false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
