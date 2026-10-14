package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// AgentCaller is the slice of the agent registry the HTTP layer needs.
type AgentCaller interface {
	Call(ctx context.Context, wsID, method string, params, out any) error
	Online(wsID string) bool
	Subscribe(wsID string, fn func(method string, params json.RawMessage)) (cancel func())
}

type FilesDeps struct {
	Svc   *workspace.Service
	Agent AgentCaller
	Audit *audit.Recorder
}

var agentStatus = map[string]int{
	"not_found":         http.StatusNotFound,
	"exists":            http.StatusConflict,
	"conflict":          http.StatusConflict,
	"outside_workspace": http.StatusUnprocessableEntity,
	"bad_path":          http.StatusUnprocessableEntity,
	"bad_query":         http.StatusUnprocessableEntity,
	"bad_params":        http.StatusBadRequest,
	"is_directory":      http.StatusUnprocessableEntity,
	"not_a_directory":   http.StatusUnprocessableEntity,
	"too_large":         http.StatusRequestEntityTooLarge,
	"permission":        http.StatusForbidden,
	"too_many":          http.StatusConflict,
}

// agentFail maps an error from the agent link to an HTTP response.
func (s *Server) agentFail(w http.ResponseWriter, err error) {
	var ae *agentproto.Error
	if errors.As(err, &ae) {
		st, ok := agentStatus[ae.Code]
		if !ok {
			st = http.StatusBadGateway
		}
		WriteError(w, st, ae.Code, ae.Msg)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		WriteError(w, http.StatusGatewayTimeout, "agent_timeout", "The workspace did not answer in time.")
		return
	}
	WriteError(w, http.StatusBadGateway, "agent_error", "The workspace agent could not complete the request.")
}

// workspaceAgent verifies ownership, that the workspace is ready and that its
// agent is connected.
func (s *Server) workspaceAgent(d FilesDeps, w http.ResponseWriter, r *http.Request) (*domain.Workspace, bool) {
	ws, err := d.Svc.Get(r.Context(), Owner(r), r.PathValue("id"))
	if err != nil {
		s.HandleError(w, err)
		return nil, false
	}
	if ws.Status != domain.WSReady {
		WriteError(w, http.StatusConflict, "workspace_not_ready", "The workspace is not running. Start it to use files and terminals.")
		return nil, false
	}
	if !d.Agent.Online(ws.ID) {
		WriteError(w, http.StatusConflict, "agent_offline", "The workspace agent is not connected yet. Try again in a moment.")
		return nil, false
	}
	return ws, true
}

func (s *Server) RegisterFiles(d FilesDeps) {
	call := func(w http.ResponseWriter, r *http.Request, method string, params any, out any, audited string, detail func() map[string]any) bool {
		ws, ok := s.workspaceAgent(d, w, r)
		if !ok {
			return false
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := d.Agent.Call(ctx, ws.ID, method, params, out); err != nil {
			s.agentFail(w, err)
			return false
		}
		if audited != "" && d.Audit != nil {
			d.Audit.User(r.Context(), Owner(r), ws.ID, audited, "", detail())
		}
		return true
	}
	q := func(r *http.Request) string { return r.URL.Query().Get("path") }

	s.Handle("GET /api/workspaces/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		var out map[string]any
		if call(w, r, "fs.list", map[string]string{"path": q(r)}, &out, "", nil) {
			WriteJSON(w, http.StatusOK, out)
		}
	})
	s.Handle("GET /api/workspaces/{id}/files/stat", func(w http.ResponseWriter, r *http.Request) {
		var out map[string]any
		if call(w, r, "fs.stat", map[string]string{"path": q(r)}, &out, "", nil) {
			WriteJSON(w, http.StatusOK, out)
		}
	})
	s.Handle("GET /api/workspaces/{id}/files/content", func(w http.ResponseWriter, r *http.Request) {
		var out map[string]any
		if call(w, r, "fs.read", map[string]string{"path": q(r)}, &out, "", nil) {
			WriteJSON(w, http.StatusOK, out)
		}
	})
	s.Handle("PUT /api/workspaces/{id}/files/content", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path            string `json:"path"`
			Content         string `json:"content"`
			ExpectedModTime string `json:"expectedModTime,omitempty"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
		if !DecodeJSON(w, r, &in) {
			return
		}
		var out map[string]any
		if call(w, r, "fs.write", in, &out, "workspace.file_written", func() map[string]any { return map[string]any{"path": in.Path, "bytes": len(in.Content)} }) {
			WriteJSON(w, http.StatusOK, out)
		}
	})
	s.Handle("POST /api/workspaces/{id}/files/dir", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		if call(w, r, "fs.mkdir", in, nil, "workspace.dir_created", func() map[string]any { return map[string]any{"path": in.Path} }) {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	s.Handle("POST /api/workspaces/{id}/files/rename", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		if call(w, r, "fs.rename", in, nil, "workspace.file_renamed", func() map[string]any { return map[string]any{"from": in.From, "to": in.To} }) {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	s.Handle("DELETE /api/workspaces/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		rec := r.URL.Query().Get("recursive") == "true"
		p := q(r)
		if call(w, r, "fs.delete", map[string]any{"path": p, "recursive": rec}, nil, "workspace.file_deleted", func() map[string]any { return map[string]any{"path": p, "recursive": rec} }) {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	s.Handle("GET /api/workspaces/{id}/files/search", func(w http.ResponseWriter, r *http.Request) {
		var out map[string]any
		params := map[string]any{"query": r.URL.Query().Get("q"), "content": r.URL.Query().Get("content") == "true"}
		if call(w, r, "fs.search", params, &out, "", nil) {
			WriteJSON(w, http.StatusOK, out)
		}
	})
	s.Handle("POST /api/workspaces/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Command    string `json:"command"`
			TimeoutSec int    `json:"timeoutSec,omitempty"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		// Command lines can carry secrets, so the audit trail records the
		// program and size, never the full text.
		prog := ""
		if f := strings.Fields(in.Command); len(f) > 0 {
			prog = f[0]
			if len(prog) > 64 {
				prog = prog[:64]
			}
		}
		var out map[string]any
		ws, ok := s.workspaceAgent(d, w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 11*time.Minute)
		defer cancel()
		if err := d.Agent.Call(ctx, ws.ID, "exec.run", in, &out); err != nil {
			s.agentFail(w, err)
			return
		}
		if d.Audit != nil {
			d.Audit.User(r.Context(), Owner(r), ws.ID, "workspace.command_run", prog, map[string]any{"commandLength": len(in.Command), "exitCode": out["exitCode"]})
		}
		WriteJSON(w, http.StatusOK, out)
	})
	s.Handle("GET /api/workspaces/{id}/ports", func(w http.ResponseWriter, r *http.Request) {
		var out map[string]any
		if call(w, r, "ports.list", struct{}{}, &out, "", nil) {
			WriteJSON(w, http.StatusOK, out)
		}
	})
}
