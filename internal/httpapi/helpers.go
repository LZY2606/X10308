package httpapi

import (
	"context"
	"embed"
	"net/http"

	"tflocal/internal/service"
)

//go:embed static/app.js
var staticFS embed.FS

func branchVersionOf(ctx context.Context, s *Service, branchID string) int {
	bs, err := s.Svc.ListBranches(ctx)
	if err != nil {
		return 0
	}
	for _, b := range bs {
		if b.BranchID == branchID {
			return b.Version
		}
	}
	return 0
}

func staticAppJS(w http.ResponseWriter, r *http.Request) {
	b, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(b)
}
