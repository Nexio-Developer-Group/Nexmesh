package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/rules"
)

// RegisterRules mounts the agent rules editor endpoints (PRD F21). Rules are
// ordinary files in the repository; these endpoints read and write them.
func (s *Server) RegisterRules(svc *rules.Service, d FilesDeps) {
	s.Handle("GET /api/workspaces/{id}/rules", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		l, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if l == nil {
			l = []*rules.Rule{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"rules": l, "directory": rules.Dir})
	})
	s.Handle("PUT /api/workspaces/{id}/rules", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		var in rules.SaveInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.Save(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.Handle("DELETE /api/workspaces/{id}/rules/{name}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		if err := svc.Delete(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("name")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
