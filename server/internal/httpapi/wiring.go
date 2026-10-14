package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
)

// RegisterWiring mounts connection-line endpoints (PRD F1).
func (s *Server) RegisterWiring(svc *wiring.Service) {
	s.Handle("GET /api/workspaces/{id}/wires", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"wires": list})
	})
	// Preview validates a pair and lists the variables the line would create.
	s.Handle("POST /api/workspaces/{id}/wires/preview", func(w http.ResponseWriter, r *http.Request) {
		var in wiring.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		pv, err := svc.Preview(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, pv)
	})
	s.Handle("POST /api/workspaces/{id}/wires", func(w http.ResponseWriter, r *http.Request) {
		var in wiring.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.Create(r.Context(), domain.ActorUser, Owner(r), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.Handle("DELETE /api/workspaces/{id}/wires/{wid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Delete(r.Context(), domain.ActorUser, Owner(r), Owner(r), r.PathValue("id"), r.PathValue("wid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
