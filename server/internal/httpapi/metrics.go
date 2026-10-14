package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/metrics"
)

// RegisterMetrics mounts per-card metrics (PRD F5).
func (s *Server) RegisterMetrics(svc *metrics.Service) {
	s.Handle("GET /api/workspaces/{id}/metrics", func(w http.ResponseWriter, r *http.Request) {
		snap, err := svc.Collect(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, snap)
	})
}
