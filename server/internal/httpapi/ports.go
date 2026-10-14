package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/preview"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type portRow struct {
	Port     int    `json:"port"`
	Label    string `json:"label,omitempty"`
	Declared bool   `json:"declared"`
	Detected bool   `json:"detected"`
	Local    bool   `json:"local"` // bound to loopback only inside the workspace
	Process  string `json:"process,omitempty"`
}

type agentPort struct {
	Port    int    `json:"port"`
	Local   bool   `json:"local"`
	Process string `json:"process"`
}

// RegisterPorts mounts the ports API and the owner preview proxy.
func (s *Server) RegisterPorts(d FilesDeps, st store.Store, pv *preview.Service) {
	s.Handle("GET /api/workspaces/{id}/ports", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		rows := map[int]*portRow{}
		for _, p := range ws.Ports {
			rows[p.Port] = &portRow{Port: p.Port, Label: p.Label, Declared: true}
		}
		online := ws.Status == domain.WSReady && d.Agent.Online(ws.ID)
		if online {
			var out struct {
				Ports []agentPort `json:"ports"`
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			if err := d.Agent.Call(ctx, ws.ID, "ports.list", struct{}{}, &out); err != nil {
				s.agentFail(w, err)
				return
			}
			for _, p := range out.Ports {
				row := rows[p.Port]
				if row == nil {
					row = &portRow{Port: p.Port}
					rows[p.Port] = row
				}
				row.Detected, row.Local, row.Process = true, p.Local, p.Process
			}
		}
		list := make([]portRow, 0, len(rows))
		for _, r := range rows {
			list = append(list, *r)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Port < list[j].Port })
		WriteJSON(w, http.StatusOK, map[string]any{"ports": list, "online": online, "previewMode": map[bool]string{true: "subdomain", false: "path"}[pv.SubdomainMode()]})
	})

	s.Handle("PUT /api/workspaces/{id}/ports/{port}", func(w http.ResponseWriter, r *http.Request) {
		port, ok := parsePort(w, r)
		if !ok {
			return
		}
		var in struct {
			Label string `json:"label"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		label := strings.TrimSpace(in.Label)
		if len(label) > 60 {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: "Label must be under 60 characters.", Field: "label"}})
			return
		}
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if _, err := st.UpdateWorkspace(r.Context(), ws.ID, func(w *domain.Workspace) error {
			for i := range w.Ports {
				if w.Ports[i].Port == port {
					w.Ports[i].Label = label
					return nil
				}
			}
			if len(w.Ports) >= 50 {
				return &validationErr{"port", "A workspace can declare at most 50 ports."}
			}
			w.Ports = append(w.Ports, domain.DeclaredPort{Port: port, Label: label})
			return nil
		}); err != nil {
			s.portErr(w, err)
			return
		}
		d.Audit.User(r.Context(), Owner(r), ws.ID, "port.declared", strconv.Itoa(port), map[string]any{"label": label})
		w.WriteHeader(http.StatusNoContent)
	})

	s.Handle("DELETE /api/workspaces/{id}/ports/{port}", func(w http.ResponseWriter, r *http.Request) {
		port, ok := parsePort(w, r)
		if !ok {
			return
		}
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if _, err := st.UpdateWorkspace(r.Context(), ws.ID, func(w *domain.Workspace) error {
			out := w.Ports[:0]
			for _, p := range w.Ports {
				if p.Port != port {
					out = append(out, p)
				}
			}
			w.Ports = out
			return nil
		}); err != nil {
			s.HandleError(w, err)
			return
		}
		d.Audit.User(r.Context(), Owner(r), ws.ID, "port.undeclared", strconv.Itoa(port), nil)
		w.WriteHeader(http.StatusNoContent)
	})

	// Returns the URL that opens the preview for the signed-in owner.
	s.Handle("POST /api/workspaces/{id}/ports/{port}/preview", func(w http.ResponseWriter, r *http.Request) {
		port, ok := parsePort(w, r)
		if !ok {
			return
		}
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		u, exp := pv.OwnerURL(r, ws.ID, port)
		WriteJSON(w, http.StatusOK, map[string]any{"url": u, "expiresAt": exp, "mode": map[bool]string{true: "subdomain", false: "path"}[pv.SubdomainMode()]})
	})

	// Path-mode owner proxy (sandboxed). Auth: session cookie via the API middleware.
	s.Handle("/api/preview/{id}/{port}/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		port, ok := parsePort(w, r)
		if !ok {
			return
		}
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		pv.ServePath(w, r, ws.ID, port)
	})
}

type validationErr struct{ field, msg string }

func (e *validationErr) Error() string { return e.msg }

func (s *Server) portErr(w http.ResponseWriter, err error) {
	if ve, ok := err.(*validationErr); ok {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: ve.msg, Field: ve.field}})
		return
	}
	s.HandleError(w, err)
}

func parsePort(w http.ResponseWriter, r *http.Request) (int, bool) {
	p, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || p < 1 || p > 65535 {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: "Enter a port between 1 and 65535.", Field: "port"}})
		return 0, false
	}
	return p, true
}

// RegisterShares mounts share-link management (owner) and the public share route.
func (s *Server) RegisterShares(d FilesDeps, pv *preview.Service) {
	fail := func(w http.ResponseWriter, err error) {
		var se *preview.ShareError
		switch {
		case errors.As(err, &se):
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: se.Msg, Field: se.Field}})
		case errors.Is(err, preview.ErrSharingDisabled):
			WriteError(w, http.StatusForbidden, "sharing_disabled", err.Error())
		case errors.Is(err, preview.ErrShareLimit):
			WriteError(w, http.StatusConflict, "share_limit", err.Error())
		default:
			s.HandleError(w, err)
		}
	}
	s.Handle("GET /api/workspaces/{id}/shares", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		l, err := pv.ListShares(r.Context(), ws.ID)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"enabled": pv.SharingEnabled(), "links": l})
	})
	s.Handle("POST /api/workspaces/{id}/shares", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Port       int    `json:"port"`
			TTLMinutes int    `json:"ttlMinutes"`
			Label      string `json:"label"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		link, secret, err := pv.CreateShare(r.Context(), Owner(r), ws.ID, in.Port, time.Duration(in.TTLMinutes)*time.Minute, in.Label)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{"link": link, "url": pv.ShareURL(r, ws.ID, in.Port, secret)})
	})
	s.Handle("DELETE /api/workspaces/{id}/shares/{sid}", func(w http.ResponseWriter, r *http.Request) {
		ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if err := pv.RevokeShare(r.Context(), Owner(r), ws.ID, r.PathValue("sid")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Public: anonymous visitors with a share link (not under /api, so no session is required).
	s.Handle("/share/{id}/{port}/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		port, err := strconv.Atoi(r.PathValue("port"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		pv.ServeShare(w, r, r.PathValue("id"), port)
	})
}
