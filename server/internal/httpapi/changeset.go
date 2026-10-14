package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// RegisterChangeSets mounts staged-change review and checkpoint endpoints (PRD F18).
func (s *Server) RegisterChangeSets(svc *changeset.Service, d FilesDeps) {
	base := "/api/workspaces/{id}"
	s.Handle("GET "+base+"/changes", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"changeSets": list})
	})
	s.Handle("POST "+base+"/changes", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		var in changeset.ProposeInput
		if !DecodeJSONLimit(w, r, &in, 8<<20) {
			return
		}
		set, err := svc.Propose(r.Context(), Owner(r), r.PathValue("id"), domain.ActorUser, Owner(r), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		v, err := svc.Get(r.Context(), Owner(r), r.PathValue("id"), set.ID)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.Handle("GET "+base+"/changes/{cs}", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.Get(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cs"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.Handle("POST "+base+"/changes/{cs}/accept", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		var in changeset.Decision
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.Accept(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cs"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.Handle("POST "+base+"/changes/{cs}/reject", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.Reject(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cs"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.Handle("GET "+base+"/checkpoints", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.Checkpoints(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"checkpoints": list})
	})
	s.Handle("POST "+base+"/checkpoints/{cp}/revert", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.workspaceAgent(d, w, r); !ok {
			return
		}
		var in struct {
			Force bool `json:"force"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.Revert(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cp"), in.Force)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
}
