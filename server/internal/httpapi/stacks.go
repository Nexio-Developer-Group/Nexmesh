package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/stacks"
)

func (s *Server) RegisterStacks(svc *stacks.Service) {
	fail := func(w http.ResponseWriter, err error) {
		var ve *stacks.ValidationError
		if errors.As(err, &ve) {
			status := http.StatusUnprocessableEntity
			if ve == stacks.ErrNameTaken {
				status = http.StatusConflict
			}
			WriteJSON(w, status, map[string]ValidationBody{"error": {Code: "invalid", Message: ve.Msg, Field: ve.Field}})
			return
		}
		s.HandleError(w, err)
	}
	s.Handle("GET /api/templates", func(w http.ResponseWriter, r *http.Request) {
		g, err := svc.List(r.Context(), Owner(r))
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, g)
	})
	s.Handle("POST /api/workspaces/{id}/templates/{tid}/apply", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.Apply(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("tid"))
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, res)
	})
	s.Handle("POST /api/workspaces/{id}/templates", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		e, err := svc.SaveCanvas(r.Context(), Owner(r), r.PathValue("id"), in.Name, in.Description)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, e)
	})
	s.Handle("DELETE /api/templates/{tid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Delete(r.Context(), Owner(r), r.PathValue("tid")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
