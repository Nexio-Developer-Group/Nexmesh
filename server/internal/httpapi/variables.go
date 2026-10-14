package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
)

func (s *Server) RegisterVariables(svc *envvars.Service) {
	fail := func(w http.ResponseWriter, err error) {
		var ve *envvars.ValidationError
		if errors.As(err, &ve) {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: ve.Msg, Field: ve.Field}})
			return
		}
		s.HandleError(w, err)
	}
	s.Handle("GET /api/workspaces/{id}/variables", func(w http.ResponseWriter, r *http.Request) {
		l, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, l)
	})
	s.Handle("PUT /api/workspaces/{id}/variables/{name}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ComponentID string  `json:"componentId"`
			Secret      bool    `json:"secret"`
			Value       *string `json:"value"`
			Description string  `json:"description"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if !DecodeJSON(w, r, &in) {
			return
		}
		row, err := svc.Set(r.Context(), Owner(r), r.PathValue("id"), envvars.SetInput{
			Name: r.PathValue("name"), ComponentID: in.ComponentID, Secret: in.Secret, Value: in.Value, Description: in.Description})
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, row)
	})
	s.Handle("DELETE /api/workspaces/{id}/variables/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Delete(r.Context(), Owner(r), r.PathValue("id"), r.URL.Query().Get("componentId"), r.PathValue("name")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.Handle("POST /api/workspaces/{id}/variables/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content   string `json:"content"`
			Secrets   string `json:"secrets"` // auto | all | none
			Overwrite bool   `json:"overwrite"`
			DryRun    bool   `json:"dryRun"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if !DecodeJSON(w, r, &in) {
			return
		}
		if in.Secrets == "" {
			in.Secrets = "auto"
		}
		res, err := svc.Import(r.Context(), Owner(r), r.PathValue("id"), in.Content, in.Secrets, in.Overwrite, in.DryRun)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
	s.Handle("GET /api/workspaces/{id}/variables/export", func(w http.ResponseWriter, r *http.Request) {
		out, err := svc.Export(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename=".env.example"`)
		_, _ = w.Write([]byte(out))
	})
}
