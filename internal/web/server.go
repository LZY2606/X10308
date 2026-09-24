// Package web renders Petri nets to local SVG and serves the review UI/API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"pnwork/internal/cases"
	"pnwork/internal/explore"
	"pnwork/internal/pnnet"
	"pnwork/internal/service"
	"pnwork/internal/store"
)

//go:embed static/*
var staticFS embed.FS

// Server holds the application service.
type Server struct {
	svc *service.Service
}

// NewServer builds the HTTP server.
func NewServer(svc *service.Service) *Server { return &Server{svc: svc} }

// Routes registers all endpoints.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/bootstrap", s.bootstrap)
	mux.HandleFunc("GET /api/cases", s.listCases)
	mux.HandleFunc("GET /api/cases/{id}", s.getCase)
	mux.HandleFunc("GET /api/cases/{id}/branches", s.listBranches)
	mux.HandleFunc("POST /api/cases/{id}/branches", s.forkBranch)
	mux.HandleFunc("GET /api/branches/{bid}", s.getBranch)
	mux.HandleFunc("GET /api/branches/{bid}/snapshot", s.branchSnapshot)
	mux.HandleFunc("GET /api/branches/{bid}/events", s.branchEvents)
	mux.HandleFunc("POST /api/branches/{bid}/explore", s.runExplore)
	mux.HandleFunc("GET /api/explorations/{id}", s.getExploration)
	mux.HandleFunc("GET /api/explorations/{id}/candidates", s.listCandidates)
	mux.HandleFunc("POST /api/branches/{bid}/adjudicate", s.adjudicate)
	mux.HandleFunc("POST /api/adjudications/{id}/undo", s.undoAdjudication)
	mux.HandleFunc("GET /api/branches/{bid}/adjudications", s.listAdjudications)
	mux.HandleFunc("GET /api/branches/{bid}/replay", s.replay)
	mux.HandleFunc("PUT /api/branches/{bid}/aspects/{aspect}", s.updateAspect)
	mux.HandleFunc("GET /api/sources/{sid}/versions/{version}/raw", s.rawVersion)
	mux.HandleFunc("GET /api/branches/{bid}/graph.svg", s.graphSVG)
	mux.HandleFunc("GET /api/cases/{id}/export", s.exportCase)
	mux.HandleFunc("POST /api/import", s.importBundle)
	mux.HandleFunc("POST /api/cases/{id}/conflict-preview", s.conflictPreview)

	stripped, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(stripped)))
	return logging(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "rule_version": service.RuleVersion})
}

// bootstrap loads the bundled offline case if no case exists. Idempotent.
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bun := cases.Bundle()
	if _, err := s.svc.Store().GetCase(ctx, cases.CaseID); err == nil {
		writeJSON(w, 200, map[string]any{"case_id": cases.CaseID, "already_present": true})
		return
	}
	if err := s.svc.ImportBundle(ctx, bun, false); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"case_id": cases.CaseID, "loaded": true})
}

func (s *Server) listCases(w http.ResponseWriter, r *http.Request) {
	cs, err := s.svc.Store().ListCases(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"cases": cs})
}

func (s *Server) getCase(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.Store().GetCase(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	bun := cases.Bundle()
	writeJSON(w, 200, map[string]any{
		"case":      c,
		"questions": bun.Questions,
		"scenarios": bun.Scenarios,
		"narrative": bun.Narrative,
	})
}

func (s *Server) listBranches(w http.ResponseWriter, r *http.Request) {
	bs, err := s.svc.Store().ListBranches(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branches": bs})
}

func (s *Server) getBranch(w http.ResponseWriter, r *http.Request) {
	b, err := s.svc.Store().GetBranch(r.Context(), r.PathValue("bid"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	pins, _ := s.svc.Store().BranchAspects(r.Context(), b.ID)
	writeJSON(w, 200, map[string]any{"branch": b, "aspects": pins})
}

type forkReq struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Note     string `json:"note"`
	BaseRev  int64  `json:"base_rev"`
	StableID string `json:"stable_id"`
}

func (s *Server) forkBranch(w http.ResponseWriter, r *http.Request) {
	var req forkReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	id, rev, err := s.svc.ForkBranch(r.Context(), service.ForkRequest{
		CaseID: r.PathValue("id"), ParentID: r.PathValue("bid"),
		Name: req.Name, Kind: req.Kind, Note: req.Note,
		BaseRev: req.BaseRev, StableID: req.StableID,
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 201, map[string]any{"branch_id": id, "rev": rev})
}

func (s *Server) branchSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.SnapshotForBranch(r.Context(), r.PathValue("bid"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	exs, _ := s.svc.Store().ListExplorations(r.Context(), r.PathValue("bid"))
	adjs, _ := s.svc.Store().ListAdjudications(r.Context(), r.PathValue("bid"))
	writeJSON(w, 200, snapshotView(snap, exs, adjs))
}

func snapshotView(snap *service.Snapshot, exs []*store.Exploration, adjs []*store.Adjudication) map[string]any {
	return map[string]any{
		"net":             snap.Net,
		"initial":         snap.Initial,
		"initial_id":      snap.InitialID,
		"strategy":        snap.Strategy,
		"aspect_versions": snap.AspectVers,
		"diagnostics":     snap.Diagnostics,
		"deps":            snap.Deps,
		"explorations":    exs,
		"adjudications":   adjs,
	}
}

func (s *Server) branchEvents(w http.ResponseWriter, r *http.Request) {
	evs, err := s.svc.Store().ListEvents(r.Context(), r.PathValue("bid"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": evs})
}

type exploreAPIReq struct {
	Question string        `json:"question"`
	Goal     pnnet.Marking `json:"goal"`
	BaseRev  int64         `json:"base_rev"`
	StableID string        `json:"stable_id"`
}

func (s *Server) runExplore(w http.ResponseWriter, r *http.Request) {
	var req exploreAPIReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	resp, err := s.svc.RunExploration(r.Context(), service.ExploreRequest{
		BranchID: r.PathValue("bid"), Question: req.Question,
		Goal: req.Goal, BaseRev: req.BaseRev, StableID: req.StableID,
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 201, resp)
}

func (s *Server) getExploration(w http.ResponseWriter, r *http.Request) {
	x, err := s.svc.Store().GetExploration(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, map[string]any{"exploration": x})
}

func (s *Server) listCandidates(w http.ResponseWriter, r *http.Request) {
	cs, err := s.svc.Store().ListCandidates(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"candidates": cs})
}

type adjudicateReq struct {
	CandidateID string `json:"candidate_id"`
	Decision    string `json:"decision"`
	Rationale   string `json:"rationale"`
	BaseRev     int64  `json:"base_rev"`
	StableID    string `json:"stable_id"`
}

func (s *Server) adjudicate(w http.ResponseWriter, r *http.Request) {
	var req adjudicateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	seq, rev, err := s.svc.Adjudicate(r.Context(), service.DecideRequest{
		BranchID: r.PathValue("bid"), CandidateID: req.CandidateID,
		Decision: req.Decision, Rationale: req.Rationale,
		BaseRev: req.BaseRev, StableID: req.StableID,
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 201, map[string]any{"event_seq": seq, "rev": rev})
}

type undoReq struct {
	BaseRev int64 `json:"base_rev"`
}

func (s *Server) undoAdjudication(w http.ResponseWriter, r *http.Request) {
	var req undoReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	seq, err := s.svc.UndoDecision(r.Context(), r.PathValue("id"), req.BaseRev)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, map[string]any{"event_seq": seq})
}

func (s *Server) listAdjudications(w http.ResponseWriter, r *http.Request) {
	as, err := s.svc.Store().ListAdjudications(r.Context(), r.PathValue("bid"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"adjudications": as})
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	seq, _ := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
	if seq <= 0 {
		// default to latest
		evs, err := s.svc.Store().ListEvents(r.Context(), r.PathValue("bid"))
		if err != nil {
			writeError(w, 500, err)
			return
		}
		for _, ev := range evs {
			if ev.Seq > seq {
				seq = ev.Seq
			}
		}
	}
	view, err := s.svc.Replay(r.Context(), r.PathValue("bid"), seq)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, view)
}

var _ = explore.StrategyBFS
var _ = context.Background
var _ = strings.TrimSpace
