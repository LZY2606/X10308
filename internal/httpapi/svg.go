package httpapi

import (
	"net/http"

	"tflocal/internal/domain"
	"tflocal/internal/render"
)

func (s *Server) svgNet(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	scID := r.URL.Query().Get("scenario_id")
	doc, _, _, err := s.Svc.BranchDoc(r.Context(), branchID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	for _, sc := range doc.Scenarios {
		if sc.ID != scID {
			continue
		}
		n, err := domain.Interpret(sc)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(render.NetSVG(sc, n, 900, 430)))
		return
	}
	writeErr(w, 404, errNotFound)
}

func (s *Server) svgSpace(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	scID := r.URL.Query().Get("scenario_id")
	doc, _, _, err := s.Svc.BranchDoc(r.Context(), branchID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	for _, sc := range doc.Scenarios {
		if sc.ID != scID {
			continue
		}
		res, _, err := s.Svc.RunResult(r.Context(), branchID, scID, "exact")
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(render.StateSpaceSVG(sc, res)))
		return
	}
	writeErr(w, 404, errNotFound)
}

func (s *Server) svgCover(w http.ResponseWriter, r *http.Request) {
	branchID := r.URL.Query().Get("branch_id")
	scID := r.URL.Query().Get("scenario_id")
	doc, _, _, err := s.Svc.BranchDoc(r.Context(), branchID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	for _, sc := range doc.Scenarios {
		if sc.ID != scID {
			continue
		}
		res, err := s.Svc.CoverResult(r.Context(), branchID, scID)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(render.CoverSVG(sc, res)))
		return
	}
	writeErr(w, 404, errNotFound)
}
