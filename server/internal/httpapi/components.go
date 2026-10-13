package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/component"
)

// RegisterComponents mounts canvas card endpoints.
func (s *Server) RegisterComponents(svc *component.Service) {
	s.Handle("GET /api/workspaces/{id}/components", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"components": list})
	})

	s.Handle("POST /api/workspaces/{id}/components", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CatalogID string  `json:"catalogId"`
			X         float64 `json:"x"`
			Y         float64 `json:"y"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.Add(r.Context(), Owner(r), r.PathValue("id"), in.CatalogID, in.X, in.Y)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, c)
	})

	s.Handle("GET /api/workspaces/{id}/components/{cid}", func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.Get(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})

	s.Handle("PATCH /api/workspaces/{id}/components/{cid}", func(w http.ResponseWriter, r *http.Request) {
		var p component.Patch
		if !DecodeJSON(w, r, &p) {
			return
		}
		c, err := svc.Update(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), p)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})

	s.Handle("DELETE /api/workspaces/{id}/components/{cid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Remove(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func init() {
	extraErrors = append(extraErrors, func(err error) (int, string, bool) {
		switch {
		case errors.Is(err, component.ErrWorkspaceState), errors.Is(err, component.ErrBadComponent):
			return http.StatusConflict, "conflict", true
		}
		return 0, "", false
	})
}
