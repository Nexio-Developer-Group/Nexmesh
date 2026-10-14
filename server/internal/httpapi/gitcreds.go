package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
)

func (s *Server) RegisterGitCredentials(svc *gitcred.Service) {
	fail := func(w http.ResponseWriter, err error) {
		var ve *gitcred.ValidationError
		if errors.As(err, &ve) {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: ve.Msg, Field: ve.Field}})
			return
		}
		s.HandleError(w, err)
	}
	s.Handle("GET /api/workspaces/{id}/git/credentials", func(w http.ResponseWriter, r *http.Request) {
		l, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"credentials": l})
	})
	s.Handle("POST /api/workspaces/{id}/git/credentials", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Provider string `json:"provider"`
			Host     string `json:"host"`
			Username string `json:"username"`
			Token    string `json:"token"`
			Label    string `json:"label"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.Add(r.Context(), Owner(r), r.PathValue("id"), gitcred.AddInput{Provider: in.Provider, Host: in.Host, Username: in.Username, Token: in.Token, Label: in.Label})
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, c)
	})
	s.Handle("PUT /api/workspaces/{id}/git/credentials/{cid}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Label *string `json:"label"`
			Token *string `json:"token"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.Update(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), in.Label, in.Token)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})
	s.Handle("DELETE /api/workspaces/{id}/git/credentials/{cid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Revoke(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
