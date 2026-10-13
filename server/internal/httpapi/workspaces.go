package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

func jsonDecoder(r *http.Request) *json.Decoder {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d
}

// Owner returns the authenticated user. Authentication is a stub until the
// sign-in work lands; every request acts as the single local user.
func Owner(r *http.Request) string {
	if u := r.Header.Get("X-Nexmesh-User"); u != "" {
		return u
	}
	return "local"
}

type WorkspaceDeps struct {
	Svc    *workspace.Service
	Driver runtime.Driver
	Store  store.Store
	Bus    *events.Bus
}

func (s *Server) RegisterWorkspaces(d WorkspaceDeps) {
	s.Handle("GET /api/host", func(w http.ResponseWriter, r *http.Request) {
		caps, err := d.Driver.Capabilities(r.Context())
		if err != nil {
			WriteError(w, http.StatusServiceUnavailable, "backend_unavailable", "The execution backend is not reachable.")
			return
		}
		WriteJSON(w, http.StatusOK, caps)
	})

	s.Handle("GET /api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Svc.List(r.Context(), Owner(r))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"workspaces": list})
	})

	s.Handle("GET /api/workspaces/name-available", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if err := workspace.ValidateName(name); err != nil {
			WriteJSON(w, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
			return
		}
		ok, err := d.Svc.NameAvailable(r.Context(), Owner(r), name)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		out := map[string]any{"available": ok}
		if !ok {
			out["reason"] = workspace.ErrNameTaken.Error()
		}
		WriteJSON(w, http.StatusOK, out)
	})

	s.Handle("POST /api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		var in workspace.CreateInput
		if !DecodeJSON(w, r, &in) {
			return
		}
		ws, err := d.Svc.Create(r.Context(), Owner(r), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, ws)
	})

	s.Handle("GET /api/workspaces/{id}", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		d.Svc.Touch(r.Context(), Owner(r), ws.ID)
		WriteJSON(w, http.StatusOK, ws)
	})

	s.Handle("DELETE /api/workspaces/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Svc.Delete(r.Context(), Owner(r), r.PathValue("id"), r.URL.Query().Get("confirmName")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.Handle("POST /api/workspaces/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		op, err := d.Svc.Start(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, op)
	})

	s.Handle("POST /api/workspaces/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		op, err := d.Svc.Stop(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, op)
	})

	s.Handle("GET /api/workspaces/{id}/operations", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		list, err := d.Store.ListOperations(r.Context(), ws.ID)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"operations": list})
	})

	s.Handle("GET /api/workspaces/{id}/audit", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		evs, err := d.Store.ListAudit(r.Context(), ws.ID, 200)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"events": evs})
	})

	// Server-Sent Events: workspace, component and operation changes.
	s.Handle("GET /api/workspaces/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			WriteError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		ch, cancel := d.Bus.Subscribe(ws.ID)
		defer cancel()
		fmt.Fprint(w, ": connected\n\n")
		fl.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev, open := <-ch:
				if !open {
					return
				}
				b, _ := json.Marshal(ev)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
				fl.Flush()
			}
		}
	})
}
