package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"tflocal/internal/domain"
	"tflocal/internal/service"
)

func (s *Server) loadCase(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		sum, err := s.Svc.EnsureBaseline(r.Context())
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, sum)
	case http.MethodGet:
		raw, fn, err := service.LoadBaseline()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		doc, err := service.ParseDocument(raw)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"filename": fn, "doc": doc})
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) counterexamples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var req struct {
		Branch     string `json:"branch_id"`
		AdoptFirst bool   `json:"adopt_first"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.Branch == "" {
		req.Branch = "br_tokenflow-pack_v1"
	}
	recs, err := s.Svc.BuildCounterexamples(r.Context(), req.Branch, req.AdoptFirst)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"records": recs})
}

func (s *Server) listBranches(w http.ResponseWriter, r *http.Request) {
	bs, err := s.Svc.ListBranches(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branches": bs})
}

func (s *Server) branchDetail(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		writeErr(w, 400, errBranchRequired)
		return
	}
	doc, raw, sourceID, err := s.Svc.BranchDoc(r.Context(), branchID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	params, pv, err := s.Svc.Params(r.Context(), branchID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	cands, err := s.Svc.ListCandidates(r.Context(), branchID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	invs, err := s.Svc.Invalidations(r.Context(), branchID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// summary per scenario
	type scsum struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Exact     any    `json:"exact"`
		Cover     any    `json:"cover"`
		NetHash   string `json:"net_hash"`
		Diagnostics []*domain.Diagnosis `json:"diagnostics"`
	}
	var scs []scsum
	for _, sc := range doc.Scenarios {
		n, err := domain.Interpret(sc)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		ex, _, _ := s.Svc.RunResult(r.Context(), branchID, sc.ID, "exact")
		cv, _ := s.Svc.CoverResult(r.Context(), branchID, sc.ID)
		scs = append(scs, scsum{ID: sc.ID, Name: sc.Name, Exact: ex, Cover: cv,
			NetHash: service.NetHashExport(sc, n), Diagnostics: n.ParseDiagnostics})
	}
	writeJSON(w, 200, map[string]any{
		"branch_id": branchID, "source_id": sourceID,
		"doc": doc, "params": params, "params_version": pv,
		"candidates": cands, "invalidations": invs, "scenarios": scs,
		"branch_version": branchVersionOf(r.Context(), s, branchID),
	})
}

func (s *Server) candidates(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	cs, err := s.Svc.ListCandidates(r.Context(), branchID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"candidates": cs})
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	tl, err := s.Svc.Timeline(r.Context(), branchID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"timeline": tl})
}

func (s *Server) invalidations(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	invs, err := s.Svc.Invalidations(r.Context(), branchID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"invalidations": invs})
}

func (s *Server) asOf(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	seq, _ := strconv.Atoi(r.URL.Query().Get("seq"))
	dec, tl, err := s.Svc.AsOf(r.Context(), branchID, seq)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"as_of_seq": seq, "decisions": dec, "timeline": tl})
}

func (s *Server) runResult(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	sc := r.URL.Query().Get("scenario_id")
	res, _, err := s.Svc.RunResult(r.Context(), branchID, sc, "exact")
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) coverResult(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	sc := r.URL.Query().Get("scenario_id")
	res, err := s.Svc.CoverResult(r.Context(), branchID, sc)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) rawFragment(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	scID := r.URL.Query().Get("scenario_id")
	doc, _, _, err := s.Svc.BranchDoc(r.Context(), branchID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	for _, sc := range doc.Scenarios {
		if sc.ID == scID {
			writeJSON(w, 200, sc)
			return
		}
	}
	writeErr(w, 404, errNotFound)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var req struct {
		BranchID      string `json:"branch_id"`
		CandidateID   string `json:"candidate_id"`
		Decision      string `json:"decision"`
		Reviewer      string `json:"reviewer"`
		ExpectVersion int    `json:"expected_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	ver, err := s.Svc.Decide(r.Context(), req.BranchID, req.CandidateID, req.Decision, req.Reviewer, req.ExpectVersion)
	if err != nil {
		if cd, ok := err.(service.ConflictDetail); ok {
			writeJSON(w, 409, map[string]any{"error": cd.Error(), "conflict": cd})
			return
		}
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branch_version": ver, "status": "recorded"})
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var req struct {
		BranchID    string `json:"branch_id"`
		CandidateID string `json:"candidate_id"`
		Reviewer    string `json:"reviewer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	ver, err := s.Svc.UndoDecision(r.Context(), req.BranchID, req.CandidateID, req.Reviewer)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branch_version": ver, "status": "undone"})
}

func (s *Server) reviseParams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var req struct {
		ParentBranch string        `json:"parent_branch"`
		Params       domain.Params `json:"params"`
		Reason       string        `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	br, err := s.Svc.ReviseParams(r.Context(), req.ParentBranch, req.Params, req.Reason)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branch_id": br})
}

func (s *Server) reviseSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var req struct {
		ParentBranch string          `json:"parent_branch"`
		Doc          json.RawMessage `json:"doc"`
		Reason       string          `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	br, err := s.Svc.ReviseSource(r.Context(), req.ParentBranch, req.Doc, req.Reason)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branch_id": br})
}

// conflictDemo simulates two review windows saving against the same branch
// version: the first commit wins, the second gets 409 with both differences.
func (s *Server) conflictDemo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var req struct {
		BranchID string `json:"branch_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.BranchID == "" {
		req.BranchID = "br_tokenflow-pack_v1"
	}
	cs, err := s.Svc.ListCandidates(r.Context(), req.BranchID)
	if err != nil || len(cs) == 0 {
		writeErr(w, 400, errNoCandidate)
		return
	}
	cid := cs[0].CandidateID
	v1, err1 := s.Svc.Decide(r.Context(), req.BranchID, cid, "accepted", "window-A", 0)
	if err1 != nil {
		// candidate may have been decided in a prior demo; undo first
		_, _ = s.Svc.UndoDecision(r.Context(), req.BranchID, cid, "reset")
		v1, err1 = s.Svc.Decide(r.Context(), req.BranchID, cid, "accepted", "window-A", 0)
		if err1 != nil {
			writeErr(w, 400, err1)
			return
		}
	}
	v2, err2 := s.Svc.Decide(r.Context(), req.BranchID, cid, "rejected", "window-B", 0)
	writeJSON(w, 200, map[string]any{
		"candidate_id": cid,
		"window_a":     map[string]any{"decision": "accepted", "version_after": v1, "error": nil},
		"window_b":     map[string]any{"decision": "rejected", "expected_version": 0, "version_after": v2, "error": errString(err2)},
		"resolution":   "second commiter must refresh: accept the conflict, undo or open a parameter-adjust branch",
	})
}

func errString(e error) any {
	if e == nil {
		return nil
	}
	if cd, ok := e.(service.ConflictDetail); ok {
		return map[string]any{"type": "version_conflict", "message": cd.Error(), "detail": cd}
	}
	return e.Error()
}

func (s *Server) exportArchive(w http.ResponseWriter, r *http.Request) {
	a, err := s.Svc.ExportArchive(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	b, err := a.JSON()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="tflocal-archive.json"`)
	_, _ = w.Write(b)
}

func (s *Server) importArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var a service.Archive
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.Svc.ImportArchive(r.Context(), &a); err != nil {
		code := 400
		if err == service.ErrDBNotEmpty {
			code = 409
		}
		writeErr(w, code, err)
		return
	}
	max, _ := s.Svc.MaxSeq(r.Context())
	writeJSON(w, 200, map[string]any{"status": "restored", "max_event_seq": max})
}

var _ = context.Background
