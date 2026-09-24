package petri

import "fmt"

// Enablement is the verdict for one transition at one marking, with
// machine-readable reasons when disabled.
type Enablement struct {
	Enabled bool     `json:"enabled"`
	Reasons []string `json:"reasons"`
}

func consumed(n *Net, tid, pid string) int {
	total := 0
	for _, a := range n.Arcs {
		if a.Kind == ArcInput && a.To == tid && a.From == pid {
			total += a.Weight
		}
	}
	return total
}

func produced(n *Net, tid, pid string) int {
	total := 0
	for _, a := range n.Arcs {
		if a.Kind == ArcOutput && a.From == tid && a.To == pid {
			total += a.Weight
		}
	}
	return total
}

// Enabled evaluates transition enablement considering, simultaneously:
//   - input arcs: marking must cover each input weight;
//   - inhibitor arcs: place tokens must stay below the inhibitor threshold;
//   - capacity: the post-fire marking of every affected place must respect
//     its capacity (capacity 0 = unbounded).
func Enabled(n *Net, m Marking, tid string) Enablement {
	var reasons []string
	for _, a := range n.Arcs {
		switch a.Kind {
		case ArcInput:
			if a.To == tid && m[a.From] < a.Weight {
				reasons = append(reasons, fmt.Sprintf("input %s: have %d need %d", a.From, m[a.From], a.Weight))
			}
		case ArcInhibitor:
			if a.To == tid && m[a.From] >= a.Weight {
				reasons = append(reasons, fmt.Sprintf("inhibitor %s: tokens %d >= threshold %d", a.From, m[a.From], a.Weight))
			}
		}
	}
	for _, p := range n.Places {
		if p.Capacity <= 0 {
			continue
		}
		delta := produced(n, tid, p.ID) - consumed(n, tid, p.ID)
		if delta == 0 {
			continue
		}
		post := m[p.ID] + delta
		if post > p.Capacity {
			reasons = append(reasons, fmt.Sprintf("capacity %s: would hold %d > capacity %d", p.ID, post, p.Capacity))
		}
	}
	return Enablement{Enabled: len(reasons) == 0, Reasons: reasons}
}

// Fire returns the post-fire marking. The caller must have checked
// enablement. Self-loops net out (consume and produce in one atomic step).
func Fire(n *Net, m Marking, tid string) Marking {
	next := make(Marking, len(m))
	for k, v := range m {
		next[k] = v
	}
	for _, p := range n.Places {
		delta := produced(n, tid, p.ID) - consumed(n, tid, p.ID)
		if delta != 0 {
			next[p.ID] = m[p.ID] + delta
		}
	}
	return next
}
