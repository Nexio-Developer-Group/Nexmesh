package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/inspector"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
)

var inspectStatus = map[string]int{
	"read_only": http.StatusUnprocessableEntity, "bad_args": http.StatusUnprocessableEntity, "empty_query": http.StatusUnprocessableEntity,
	"multiple_statements": http.StatusUnprocessableEntity, "unknown_op": http.StatusUnprocessableEntity, "not_inspectable": http.StatusUnprocessableEntity,
	"needs_management_url": http.StatusUnprocessableEntity, "not_found": http.StatusNotFound, "unsupported": http.StatusUnprocessableEntity,
	"bad_connection": http.StatusUnprocessableEntity,
}

// RegisterInspector mounts the database and broker tools (PRD F13).
func (s *Server) RegisterInspector(svc *inspector.Service) {
	s.Handle("GET /api/workspaces/{id}/components/{cid}/inspect", func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.Describe(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("cid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})
	s.Handle("POST /api/workspaces/{id}/components/{cid}/inspect", func(w http.ResponseWriter, r *http.Request) {
		var in probe.InspectRequest
		if !DecodeJSONLimit(w, r, &in, 64<<10) {
			return
		}
		res, err := svc.Run(r.Context(), domain.ActorUser, Owner(r), Owner(r), r.PathValue("id"), r.PathValue("cid"), in)
		if err != nil {
			var f *inspector.Failure
			switch {
			case errors.As(err, &f):
				st, ok := inspectStatus[f.Code]
				if !ok {
					st = http.StatusBadGateway
				}
				WriteError(w, st, f.Code, f.Message)
			case errors.Is(err, inspector.ErrRateLimited):
				WriteError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
			default:
				s.HandleError(w, err)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store") // results can contain business data
		WriteJSON(w, http.StatusOK, res)
	})
}
