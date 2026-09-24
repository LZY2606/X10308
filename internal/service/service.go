// Package service orchestrates the store, the ingest derived layer and the
// exploration engine. It enforces the workflow invariants:
//
//   - one transaction per import, fully rolled back on failure;
//   - source bytes stored apart from interpretations;
//   - scoped invalidation through explicit dependency keys (never a global
//     replacement of old conclusions);
//   - append-only events with machine-readable payloads, supporting as-of
//     replay to any historical event;
//   - reversible adjudications and optimistic-concurrency conflicts.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"pnwork/internal/explore"
	"pnwork/internal/ingest"
	"pnwork/internal/pnnet"
	"pnwork/internal/store"
)

// RuleVersion identifies the derivation rules embedded in this binary.
const RuleVersion = "rules.tokenflow.v1"

// Service holds dependencies.
type Service struct{ st *store.Store }

// New constructs a service.
func New(st *store.Store) *Service { return &Service{st: st} }

// Store exposes the persistence layer (used by backup/export tooling).
func (s *Service) Store() *store.Store { return s.st }

func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// DepKey is the versioned dependency reference for one aspect.
func DepKey(aspect string, version int) string { return aspect + "@v" + fmt.Sprint(version) }

// RuleDep is the dependency key for the rule set.
func RuleDep() string { return "rule@" + RuleVersion }

// EffectiveNet builds the derived net for a branch by overlaying the pinned
// versions of identity, weights and capacity over the structural model.
type Snapshot struct {
	Net         *pnnet.Net
	Initial     pnnet.Marking
	InitialID   string
	Strategy    explore.Config
	AspectVers  map[string]int
	InterpSHA   map[string]string
	Diagnostics []ingest.Diagnostic
	Deps        []string
}

func (s *Service) loadPins(ctx context.Context, branchID string) (map[string]store.BranchAspect, error) {
	pins, err := s.st.BranchAspects(ctx, branchID)
	if err != nil {
		return nil, err
	}
	m := map[string]store.BranchAspect{}
	for _, p := range pins {
		m[p.Aspect] = p
	}
	return m, nil
}

func (s *Service) aspectBytes(ctx context.Context, pins map[string]store.BranchAspect, aspect string) ([]byte, int, error) {
	pin, ok := pins[aspect]
	if !ok {
		return nil, 0, fmt.Errorf("branch missing aspect %q", aspect)
	}
	sv, err := s.st.GetSourceVersion(ctx, pin.SourceID, pin.Version)
	if err != nil {
		return nil, 0, err
	}
	return sv.RawBytes, pin.Version, nil
}

// SnapshotForBranch reconstructs the effective model of a branch from pinned
// source versions.
func (s *Service) SnapshotForBranch(ctx context.Context, branchID string) (*Snapshot, error) {
	pins, err := s.loadPins(ctx, branchID)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{AspectVers: map[string]int{}, InterpSHA: map[string]string{}}

	modelRaw, mv, err := s.aspectBytes(ctx, pins, "model")
	if err != nil {
		return nil, err
	}
	out, err := ingest.Parse(modelRaw)
	if err != nil {
		return nil, err
	}
	net := out.Net
	snap.Diagnostics = append(snap.Diagnostics, out.Diagnostics...)
	snap.InterpSHA["model"] = out.SHA256

	// Identity aspect: authoritative node registry; labels are overlaid.
	idRaw, idv, err := s.aspectBytes(ctx, pins, "identity")
	if err != nil {
		return nil, err
	}
	var identityDoc struct {
		IdentityID string `json:"identity_id"`
		Nodes      []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Label string `json:"label"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(idRaw, &identityDoc); err != nil {
		return nil, fmt.Errorf("identity aspect: %w", err)
	}
	placeByID := net.PlaceByID()
	transByID := net.TransitionByID()
	net.Nodes = net.Nodes[:0]
	for _, n := range identityDoc.Nodes {
		if p, ok := placeByID[n.ID]; ok {
			if n.Label != "" {
				p.Label = n.Label
			}
			net.Nodes = append(net.Nodes, pnnet.Node{ID: n.ID, Kind: pnnet.KindPlace, Label: p.Label})
		} else if t, ok := transByID[n.ID]; ok {
			if n.Label != "" {
				t.Label = n.Label
			}
			net.Nodes = append(net.Nodes, pnnet.Node{ID: n.ID, Kind: pnnet.KindTransition, Label: t.Label})
		} else {
			return nil, fmt.Errorf("identity aspect lists unknown node %q", n.ID)
		}
	}
	snap.InterpSHA["identity"] = ingest.Digest(idRaw)

	// Weights aspect: overlay raw arc weights.
	wRaw, wv, err := s.aspectBytes(ctx, pins, "weights")
	if err != nil {
		return nil, err
	}
	var weightsDoc struct {
		WeightsID string `json:"weights_id"`
		Unit      string `json:"unit"`
		Arcs      map[string]struct {
			Raw  string `json:"raw"`
			Unit string `json:"unit"`
		} `json:"arcs"`
	}
	if err := json.Unmarshal(wRaw, &weightsDoc); err != nil {
		return nil, fmt.Errorf("weights aspect: %w", err)
	}
	arcByID := map[string]*pnnet.Arc{}
	for i := range net.Arcs {
		arcByID[net.Arcs[i].ID] = &net.Arcs[i]
	}
	diags := []ingest.Diagnostic{}
	arcIDs := make([]string, 0, len(weightsDoc.Arcs))
	for id := range weightsDoc.Arcs {
		arcIDs = append(arcIDs, id)
	}
	sort.Strings(arcIDs)
	for _, id := range arcIDs {
		entry := weightsDoc.Arcs[id]
		arc, ok := arcByID[id]
		if !ok {
			return nil, fmt.Errorf("weights aspect references unknown arc %q", id)
		}
		unit := entry.Unit
		if unit == "" {
			unit = pnnet.UnitTokenCount
		}
		arc.Weight = pnnet.Raw{Text: entry.Raw, Unit: unit, Basis: pnnet.UnitTokenCount}
		if entry.Raw == "" {
			one := int64(1)
			arc.Weight.Value = &one
			continue
		}
		n, perr := parsePositive(entry.Raw)
		if perr != nil {
			arc.Weight.Diagnostics = []string{perr.Error()}
			diags = append(diags, ingest.Diagnostic{Object: "arc:" + id, Field: "weight", Text: entry.Raw, Reason: perr.Error()})
			continue
		}
		arc.Weight.Value = &n
	}
	snap.Diagnostics = append(snap.Diagnostics, diags...)
	snap.InterpSHA["weights"] = ingest.Digest(wRaw)

	// Capacity aspect: overlay raw place capacities.
	cRaw, cv, err := s.aspectBytes(ctx, pins, "capacity")
	if err != nil {
		return nil, err
	}
	var capDoc struct {
		CapacityID string `json:"capacity_id"`
		Unit       string `json:"unit"`
		Places     map[string]struct {
			Raw  string `json:"raw"`
			Unit string `json:"unit"`
		} `json:"places"`
	}
	if err := json.Unmarshal(cRaw, &capDoc); err != nil {
		return nil, fmt.Errorf("capacity aspect: %w", err)
	}
	placeIDs2 := make([]string, 0, len(capDoc.Places))
	for id := range capDoc.Places {
		placeIDs2 = append(placeIDs2, id)
	}
	sort.Strings(placeIDs2)
	for _, id := range placeIDs2 {
		entry := capDoc.Places[id]
		p, ok := placeByID[id]
		if !ok {
			return nil, fmt.Errorf("capacity aspect references unknown place %q", id)
		}
		unit := entry.Unit
		if unit == "" {
			unit = pnnet.UnitTokenCount
		}
		p.Capacity = pnnet.Raw{Text: entry.Raw, Unit: unit, Basis: pnnet.UnitTokenCount}
		txt := entry.Raw
		if txt == "" || txt == "∞" || txt == "inf" || txt == "unbounded" || txt == "ω" {
			continue
		}
		n, perr := parsePositiveOrMinusOne(txt)
		if perr != nil {
			p.Capacity.Diagnostics = []string{perr.Error()}
			snap.Diagnostics = append(snap.Diagnostics, ingest.Diagnostic{
				Object: "place:" + id, Field: "capacity", Text: entry.Raw, Reason: perr.Error(),
			})
			continue
		}
		if n >= 0 {
			p.Capacity.Value = &n
		}
	}
	snap.InterpSHA["capacity"] = ingest.Digest(cRaw)

	if err := net.Validate(); err != nil {
		return nil, fmt.Errorf("effective net invalid: %w", err)
	}
	snap.Net = net

	// Initial tokens aspect.
	tRaw, tv, err := s.aspectBytes(ctx, pins, "initial_tokens")
	if err != nil {
		return nil, err
	}
	pm, err := ingest.ParseMarking(tRaw)
	if err != nil {
		return nil, err
	}
	for pid := range pm.Marking {
		if _, ok := placeByID[pid]; !ok {
			return nil, fmt.Errorf("initial marking references unknown place %q", pid)
		}
	}
	snap.Initial = pm.Marking
	snap.InitialID = pm.ID
	snap.Diagnostics = append(snap.Diagnostics, pm.Diagnostics...)
	snap.InterpSHA["initial_tokens"] = pm.SHA256

	// Strategy aspect.
	sRaw, svv, err := s.aspectBytes(ctx, pins, "strategy")
	if err != nil {
		return nil, err
	}
	ps, err := ingest.ParseStrategy(sRaw)
	if err != nil {
		return nil, err
	}
	snap.InterpSHA["strategy"] = ps.SHA256

	// Budget aspect.
	bRaw, bv, err := s.aspectBytes(ctx, pins, "budget")
	if err != nil {
		return nil, err
	}
	pb, err := ingest.ParseBudget(bRaw)
	if err != nil {
		return nil, err
	}
	snap.InterpSHA["budget"] = pb.SHA256
	snap.Strategy = explore.Config{Strategy: ps.Strategy, Budget: pb.Budget}
	snap.Diagnostics = append(snap.Diagnostics, pb.Diagnostics...)

	snap.AspectVers = map[string]int{
		"model": mv, "identity": idv, "weights": wv, "capacity": cv,
		"initial_tokens": tv, "strategy": svv, "budget": bv,
	}
	deps := []string{RuleDep()}
	for _, a := range store.Aspects {
		deps = append(deps, DepKey(a, snap.AspectVers[a]))
	}
	sort.Strings(deps)
	snap.Deps = deps
	return snap, nil
}

func parsePositive(s string) (int64, error) {
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("unconvertible integer %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("non-positive count %q", s)
	}
	return n, nil
}

func parsePositiveOrMinusOne(s string) (int64, error) {
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("unconvertible capacity %q", s)
	}
	if n == 0 || n < -1 {
		return 0, fmt.Errorf("capacity must be positive, -1 or infinity, got %q", s)
	}
	return n, nil
}

func digestOf(b []byte) string { return ingest.Digest(b) }
