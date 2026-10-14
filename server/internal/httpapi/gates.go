package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/gates"
)

// RegisterGates mounts quality gate endpoints (PRD F23).
func (s *Server) RegisterGates(svc *gates.Service, d FilesDeps) {
	base := "/api/workspaces/{id}/gates"
	s.Handle("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.GetConfig(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})
	s.Handle("PUT "+base, func(w http.ResponseWriter, r *http.Request) {
		var in gates.Config
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.SaveConfig(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})
	s.Handle("GET "+base+"/detect", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		gs, err := svc.Detect(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"gates": gs})
	})
	s.Handle("POST "+base+"/runs", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		var in gates.StartInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		run, err := svc.Start(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, run)
	})
	s.Handle("GET "+base+"/runs", func(w http.ResponseWriter, r *http.Request) {
		l, err := svc.ListRuns(r.Context(), Owner(r), r.PathValue("id"), r.URL.Query().Get("setId"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"runs": l})
	})
	s.Handle("GET "+base+"/runs/{rid}", func(w http.ResponseWriter, r *http.Request) {
		run, err := svc.GetRun(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("rid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, run)
	})
	s.Handle("POST "+base+"/runs/{rid}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Cancel(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("rid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
