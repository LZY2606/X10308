package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"tflocal/internal/domain"
)

func sha12(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:12]
}

// NetHash fingerprints the DERIVED, normalized interpretation of a scenario:
// node identities, arc weights, initial tokens, capacities. Raw representation
// changes that normalize to the same value produce the same hash; changing a
// weight/token/capacity changes the hash.
func NetHash(s *domain.Scenario, n *domain.Net) string {
	type arcH struct {
		ID, From, To string
		Weight       int
		Kind         domain.ArcKind
	}
	arcs := make([]arcH, 0, len(n.Arcs))
	for _, a := range n.Arcs {
		arcs = append(arcs, arcH{a.ID, a.From, a.To, a.Weight, a.Kind})
	}
	sort.Slice(arcs, func(i, j int) bool { return arcs[i].ID < arcs[j].ID })
	pcaps := map[string]string{}
	for _, p := range s.Places {
		if p.Capacity == nil {
			pcaps[p.ID] = "omega"
		} else if p.Capacity.Finite {
			pcaps[p.ID] = fmt.Sprintf("%d", p.Capacity.Value)
		} else {
			pcaps[p.ID] = "raw:" + p.Capacity.Raw + ":unconverted"
		}
	}
	toks := map[string]int{}
	for _, rt := range s.Tokens {
		if rt.ConvertOK {
			toks[rt.PlaceID] = rt.Value
		}
	}
	struc := struct {
		Scn        string
		Places     []string
		Transitions []string
		Arcs       []arcH
		Tokens     map[string]int
		Capacities map[string]string
	}{s.ID, sortedPlaceIDs(n), sortedTransitionIDs(n), arcs, toks, pcaps}
	b, _ := json.Marshal(struc)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:16]
}

func sortedPlaceIDs(n *domain.Net) []string {
	ids := make([]string, 0, len(n.Places))
	for id := range n.Places {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func sortedTransitionIDs(n *domain.Net) []string {
	ids := make([]string, 0, len(n.Transitions))
	for id := range n.Transitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func paramsHash(p domain.Params) string {
	b, _ := json.Marshal(p)
	return "prm_" + sha12(b)
}

// NetHashExport exposes NetHash for the API layer.
func NetHashExport(sc *domain.Scenario, n *domain.Net) string { return NetHash(sc, n) }
