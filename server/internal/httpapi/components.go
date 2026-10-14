package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/connstr"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// RegisterComponents mounts canvas card endpoints.
func (s *Server) RegisterComponents(svc *component.Service) {
	s.Handle("GET /api/workspaces/{id}/components", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		out := make([]*domain.Component, len(list))
		for i, c := range list {
			out[i] = component.Public(c)
		}
		WriteJSON(w, http.StatusOK, map[string]any{"components": out})
	})

	s.Handle("GET /api/workspaces/{id}/components/{cid}/health", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		h, err := svc.Health(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), limit)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, h)
	})

	s.Handle("GET /api/workspaces/{id}/components/{cid}/logs", func(w http.ResponseWriter, r *http.Request) {
		tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		out, err := svc.Logs(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), tail)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"logs": out})
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
		WriteJSON(w, http.StatusCreated, component.Public(c))
	})

	s.Handle("GET /api/workspaces/{id}/components/{cid}", func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.Get(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, component.Public(c))
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
		WriteJSON(w, http.StatusOK, component.Public(c))
	})

	s.Handle("DELETE /api/workspaces/{id}/components/{cid}", func(w http.ResponseWriter, r *http.Request) {
		id, cid := r.PathValue("id"), r.PathValue("cid")
		c, err := svc.Get(r.Context(), Owner(r), id, cid)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if c.Mode == "installed" && c.Observed != "draft" {
			op, err := svc.RemoveInstalled(r.Context(), Owner(r), id, cid, r.URL.Query().Get("deleteData") == "true")
			if err != nil {
				s.HandleError(w, err)
				return
			}
			WriteJSON(w, http.StatusAccepted, op)
			return
		}
		if err := svc.Remove(r.Context(), Owner(r), id, cid); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.Handle("POST /api/workspaces/{id}/components/{cid}/install", func(w http.ResponseWriter, r *http.Request) {
		var in component.InstallInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		op, err := svc.Install(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, op)
	})

	lifecycle := func(fn func(ctx context.Context, owner, ws, id string) (*domain.Operation, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			op, err := fn(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"))
			if err != nil {
				s.HandleError(w, err)
				return
			}
			WriteJSON(w, http.StatusAccepted, op)
		}
	}
	s.Handle("POST /api/workspaces/{id}/components/{cid}/start", lifecycle(svc.Start))
	s.Handle("POST /api/workspaces/{id}/components/{cid}/stop", lifecycle(svc.Stop))
	s.Handle("POST /api/workspaces/{id}/components/{cid}/retry", lifecycle(svc.Retry))

	s.Handle("GET /api/workspaces/{id}/capacity", func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.Capacity(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})

	s.Handle("GET /api/workspaces/{id}/runtimes", func(w http.ResponseWriter, r *http.Request) {
		rt, err := svc.Runtimes(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"runtimes": rt})
	})

	s.Handle("POST /api/workspaces/{id}/runtimes/{name}/install", func(w http.ResponseWriter, r *http.Request) {
		var in component.RuntimeInstall
		if !DecodeJSON(w, r, &in) {
			return
		}
		op, err := svc.InstallRuntime(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("name"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, op)
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

// RegisterConnections mounts Connect-mode endpoints (PRD 11).
func (s *Server) RegisterConnections(svc *component.Service) {
	base := "/api/workspaces/{id}/components/{cid}/connection"

	s.Handle("POST /api/connections/parse", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CatalogID string `json:"catalogId"`
			URI       string `json:"uri"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		fields, err := connstr.Parse(in.CatalogID, in.URI)
		if err != nil {
			WriteError(w, http.StatusUnprocessableEntity, "invalid_connection_string", err.Error())
			return
		}
		// Returned only to the caller, never stored: it may contain the password.
		WriteJSON(w, http.StatusOK, map[string]any{"fields": fields})
	})

	s.Handle("POST "+base+"/test", func(w http.ResponseWriter, r *http.Request) {
		var in component.ConnectionInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		res, err := svc.Test(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	save := func(w http.ResponseWriter, r *http.Request) {
		var in component.ConnectionInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.Save(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, connectionDTO{Component: component.Public(c), ExposedAs: svc.ExposedAs(c)})
	}
	s.Handle("POST "+base, save)
	s.Handle("PUT "+base, save)

	s.Handle("DELETE "+base, func(w http.ResponseWriter, r *http.Request) {
		if _, err := svc.Disconnect(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.Handle("POST "+base+"/retest", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.Retest(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	s.Handle("POST "+base+"/rotate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Secrets map[string]string `json:"secrets"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		res, c, err := svc.Rotate(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"), in.Secrets)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if !res.Passed {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":  ErrorBody{Code: "test_failed", Message: res.Message},
				"result": res,
			})
			return
		}
		WriteJSON(w, http.StatusOK, connectionDTO{Component: component.Public(c), ExposedAs: svc.ExposedAs(c)})
	})

	// Connected cards include the variable names they expose to projects.
	s.Handle("GET /api/workspaces/{id}/connections", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		out := []connectionDTO{}
		for _, c := range list {
			if c.Mode == "connected" {
				out = append(out, connectionDTO{Component: component.Public(c), ExposedAs: svc.ExposedAs(c)})
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"connections": out})
	})
}

type connectionDTO struct {
	*domain.Component
	ExposedAs []string `json:"exposedAs,omitempty"`
}

func init() {
	extraErrors = append(extraErrors, func(err error) (int, string, bool) {
		switch {
		case errors.Is(err, component.ErrRateLimited):
			return http.StatusTooManyRequests, "rate_limited", true
		case errors.Is(err, component.ErrNotConnectable):
			return http.StatusUnprocessableEntity, "not_connectable", true
		}
		return 0, "", false
	})
}
