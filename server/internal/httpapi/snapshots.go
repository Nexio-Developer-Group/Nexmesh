package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

func (s *Server) RegisterSnapshots(svc *workspace.Service) {
	fail := func(w http.ResponseWriter, err error) {
		var se *workspace.SnapshotError
		switch {
		case errors.As(err, &se):
			status := http.StatusUnprocessableEntity
			switch se.Code {
			case "name_taken", "snapshot_failed":
				status = http.StatusConflict
			}
			WriteJSON(w, status, map[string]ValidationBody{"error": {Code: se.Code, Message: se.Msg, Field: se.Field}})
		case errors.Is(err, workspace.ErrSnapshotLimit), errors.Is(err, workspace.ErrSnapshotNotStopped), errors.Is(err, workspace.ErrSnapshotBusy):
			WriteError(w, http.StatusConflict, "snapshot_conflict", err.Error())
		default:
			s.HandleError(w, err)
		}
	}
	s.Handle("GET /api/workspaces/{id}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		ws, err := svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		list := ws.Snapshots
		if list == nil {
			list = []domain.Snapshot{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"snapshots": list, "max": workspace.MaxSnapshots})
	})
	s.Handle("POST /api/workspaces/{id}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		snap, _, err := svc.CreateSnapshot(r.Context(), Owner(r), r.PathValue("id"), in.Name)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, snap)
	})
	s.Handle("POST /api/workspaces/{id}/snapshots/{sid}/restore", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Confirm bool `json:"confirm"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		op, err := svc.RestoreSnapshot(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid"), in.Confirm)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, op)
	})
	s.Handle("DELETE /api/workspaces/{id}/snapshots/{sid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.DeleteSnapshot(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
