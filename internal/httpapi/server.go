// Package httpapi exposes the local review workspace: JSON API, inline SVG
// and a single review page. No review action mutates imported raw data.
package httpapi

import (
	"encoding/json"
	"net/http"

	"tflocal/internal/service"
)

type Server struct {
	Svc *service.Service
	Mux *http.ServeMux
}

func New(svc *service.Service) *Server {
	s := &Server{Svc: svc, Mux: http.NewServeMux()}
	s.routes()
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) routes() {
	m := s.Mux
	m.HandleFunc("/", s.indexPage)
	m.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	m.HandleFunc("/api/case/load", s.loadCase)
	m.HandleFunc("/api/case/counterexamples", s.counterexamples)
	m.HandleFunc("/api/branches", s.listBranches)
	m.HandleFunc("/api/branch", s.branchDetail)
	m.HandleFunc("/api/branch/candidates", s.candidates)
	m.HandleFunc("/api/branch/timeline", s.timeline)
	m.HandleFunc("/api/branch/invalidations", s.invalidations)
	m.HandleFunc("/api/branch/asof", s.asOf)
	m.HandleFunc("/api/scenario/run", s.runResult)
	m.HandleFunc("/api/scenario/cover", s.coverResult)
	m.HandleFunc("/api/scenario/raw", s.rawFragment)
	m.HandleFunc("/api/svg/net", s.svgNet)
	m.HandleFunc("/api/svg/space", s.svgSpace)
	m.HandleFunc("/api/svg/cover", s.svgCover)
	m.HandleFunc("/api/decide", s.decide)
	m.HandleFunc("/api/decide/undo", s.undo)
	m.HandleFunc("/api/revise/params", s.reviseParams)
	m.HandleFunc("/api/revise/source", s.reviseSource)
	m.HandleFunc("/api/conflict/demo", s.conflictDemo)
	m.HandleFunc("/api/archive/export", s.exportArchive)
	m.HandleFunc("/api/archive/import", s.importArchive)
	m.HandleFunc("/static/app.js", staticAppJS)
}
