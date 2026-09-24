package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"pnwork/internal/cases"
	"pnwork/internal/ingest"
	"pnwork/internal/service"
	"pnwork/internal/store"
)

type aspectUpdateReq struct {
	Content json.RawMessage `json:"content"`
	Label   string          `json:"label"`
	Note    string          `json:"note"`
	BaseRev int64           `json:"base_rev"`
}

// updateAspect appends a single-aspect version; bad JSON rolls the whole
// transaction back so no half record survives.
func (s *Server) updateAspect(w http.ResponseWriter, r *http.Request) {
	var req aspectUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	aspect := r.PathValue("aspect")
	branch, err := s.svc.Store().GetBranch(r.Context(), r.PathValue("bid"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	out, err := s.svc.UpdateAspect(r.Context(), service.AspectUpdate{
		CaseID: branch.CaseID, BranchID: branch.ID, Aspect: aspect,
		Content: req.Content, Label: req.Label, Note: req.Note, BaseRev: req.BaseRev,
		Validate: func(b []byte) error {
			return validateAspect(aspect, b)
		},
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, out)
}

func validateAspect(aspect string, b []byte) error {
	if !json.Valid(b) {
		return errors.New("fatal: content is not valid json")
	}
	switch aspect {
	case "model":
		if _, err := ingest.Parse(b); err != nil {
			return err
		}
	case "initial_tokens":
		if _, err := ingest.ParseMarking(b); err != nil {
			return err
		}
	case "strategy":
		if _, err := ingest.ParseStrategy(b); err != nil {
			return err
		}
	case "budget":
		if _, err := ingest.ParseBudget(b); err != nil {
			return err
		}
	case "weights":
		var doc struct {
			Arcs map[string]json.RawMessage `json:"arcs"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
	case "capacity":
		var doc struct {
			Places map[string]json.RawMessage `json:"places"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
	case "identity":
		var doc struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
	}
	return nil
}

// rawVersion serves the verbatim stored bytes of one source version.
func (s *Server) rawVersion(w http.ResponseWriter, r *http.Request) {
	version, err := parseInt(r.PathValue("version"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	sv, err := s.svc.Store().GetSourceVersion(r.Context(), r.PathValue("sid"), version)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.Header().Set("Content-Type", sv.MediaType)
	w.Header().Set("X-Sha256", sv.SHA256)
	_, _ = w.Write(sv.RawBytes)
}

func (s *Server) graphSVG(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.SnapshotForBranch(r.Context(), r.PathValue("bid"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	marking := snap.Initial
	if g := r.URL.Query().Get("marking"); g != "" {
		var m map[string]int64
		if err := json.Unmarshal([]byte(g), &m); err == nil {
			marking = m
		}
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = io.WriteString(w, SVG(snap.Net, marking))
}

func (s *Server) exportCase(w http.ResponseWriter, r *http.Request) {
	bun, err := s.svc.Export(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="tokenflow-archive.json"`)
	_ = json.NewEncoder(w).Encode(bun)
}

func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	var bun service.Bundle
	if err := json.NewDecoder(r.Body).Decode(&bun); err != nil {
		writeError(w, 400, err)
		return
	}
	replace := r.URL.Query().Get("replace") == "1"
	if err := s.svc.ImportBundle(r.Context(), &bun, replace); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, 201, map[string]any{"case_id": bun.Case.ID, "replaced": replace})
}

type conflictReq struct {
	BaseRev int64    `json:"base_rev"`
	Changed []string `json:"changed"`
}

func (s *Server) conflictPreview(w http.ResponseWriter, r *http.Request) {
	var req conflictReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	info, err := s.svc.ExplainConflict(r.Context(), r.PathValue("id"), req.BaseRev, req.Changed)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 409, info)
}

func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("bad integer")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, store.ErrConflict):
		return 409
	case errors.Is(err, store.ErrNotFound):
		return 404
	default:
		return 400
	}
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "status": code})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

var _ = cases.CaseID
