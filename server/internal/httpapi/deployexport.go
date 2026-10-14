package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/deployexport"
)

// RegisterDeployExport mounts the compose and Dockerfile export (PRD F25).
func (s *Server) RegisterDeployExport(svc *deployexport.Service, d FilesDeps) {
	s.Handle("GET /api/workspaces/{id}/export/compose", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		plan, err := svc.Build(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, plan)
	})
	s.Handle("POST /api/workspaces/{id}/export/compose/write", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		var in deployexport.WriteInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		res, err := svc.Write(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
}
