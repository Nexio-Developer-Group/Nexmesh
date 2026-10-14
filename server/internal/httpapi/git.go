package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/gitops"
)

// RegisterGit mounts the Git API. Every route verifies ownership and that the
// workspace is running with a connected agent before touching Git.
func (s *Server) RegisterGit(d FilesDeps, svc *gitops.Service) {
	fail := func(w http.ResponseWriter, err error) {
		if errors.Is(err, errAlreadyWritten) {
			return
		}
		var ge *gitops.Error
		if errors.As(err, &ge) {
			status := http.StatusUnprocessableEntity
			switch ge.Code {
			case "auth_failed":
				status = http.StatusBadGateway
			case "repo_not_found", "not_a_repo", "no_remote":
				status = http.StatusNotFound
			case "push_rejected", "pull_conflict", "dirty_tree", "dir_exists", "nothing_to_commit", "no_upstream", "detached":
				status = http.StatusConflict
			case "network", "timeout":
				status = http.StatusBadGateway
			case "git_failed":
				status = http.StatusBadGateway
			}
			body := map[string]any{"code": ge.Code, "message": ge.Message}
			if ge.Field != "" {
				body["field"] = ge.Field
			}
			if ge.Detail != "" {
				body["detail"] = ge.Detail
			}
			WriteJSON(w, status, map[string]any{"error": body})
			return
		}
		s.agentFail(w, err)
	}
	// run gates the request and gives the handler a bounded context.
	run := func(fn func(ctx context.Context, wsID, actor string, w http.ResponseWriter, r *http.Request) (any, int, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ws, ok := s.workspaceAgent(d, w, r)
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 16*time.Minute)
			defer cancel()
			out, code, err := fn(ctx, ws.ID, Owner(r), w, r)
			if err != nil {
				fail(w, err)
				return
			}
			if out == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			WriteJSON(w, code, out)
		}
	}
	q := func(r *http.Request, k string) string { return r.URL.Query().Get(k) }
	author := func(r *http.Request) gitops.Author {
		if u := CurrentUser(r.Context()); u != nil {
			return gitops.Author{Name: u.Name, Email: u.Email}
		}
		return gitops.Author{Name: "Nexmesh User", Email: "user@nexmesh.local"}
	}
	type repoBody struct {
		Repo string `json:"repo"`
	}

	s.Handle("GET /api/workspaces/{id}/git/repos", run(func(ctx context.Context, ws, _ string, _ http.ResponseWriter, _ *http.Request) (any, int, error) {
		r, err := svc.Repos(ctx, ws)
		return map[string]any{"repos": r}, 200, err
	}))
	s.Handle("POST /api/workspaces/{id}/git/clone", run(func(ctx context.Context, ws, actor string, w http.ResponseWriter, r *http.Request) (any, int, error) {
		var in struct {
			URL    string `json:"url"`
			Dir    string `json:"dir"`
			Branch string `json:"branch"`
		}
		if !DecodeJSON(w, r, &in) {
			return nil, 0, errAlreadyWritten
		}
		dir, err := svc.Clone(ctx, ws, actor, in.URL, in.Dir, in.Branch)
		return map[string]any{"repo": dir}, 201, err
	}))
	s.Handle("POST /api/workspaces/{id}/git/check-access", run(func(ctx context.Context, ws, _ string, w http.ResponseWriter, r *http.Request) (any, int, error) {
		var in struct {
			URL string `json:"url"`
		}
		if !DecodeJSON(w, r, &in) {
			return nil, 0, errAlreadyWritten
		}
		authed, err := svc.CheckAccess(ctx, ws, in.URL)
		return map[string]any{"ok": true, "authenticated": authed}, 200, err
	}))
	s.Handle("GET /api/workspaces/{id}/git/status", run(func(ctx context.Context, ws, _ string, _ http.ResponseWriter, r *http.Request) (any, int, error) {
		st, err := svc.Status(ctx, ws, q(r, "repo"))
		return st, 200, err
	}))
	s.Handle("GET /api/workspaces/{id}/git/branches", run(func(ctx context.Context, ws, _ string, _ http.ResponseWriter, r *http.Request) (any, int, error) {
		b, err := svc.Branches(ctx, ws, q(r, "repo"))
		return map[string]any{"branches": b}, 200, err
	}))
	s.Handle("POST /api/workspaces/{id}/git/switch", run(func(ctx context.Context, ws, actor string, w http.ResponseWriter, r *http.Request) (any, int, error) {
		var in struct {
			Repo   string `json:"repo"`
			Branch string `json:"branch"`
			Create bool   `json:"create"`
		}
		if !DecodeJSON(w, r, &in) {
			return nil, 0, errAlreadyWritten
		}
		return nil, 0, svc.Switch(ctx, ws, actor, in.Repo, in.Branch, in.Create)
	}))
	s.Handle("POST /api/workspaces/{id}/git/stage", run(func(ctx context.Context, ws, _ string, w http.ResponseWriter, r *http.Request) (any, int, error) {
		var in struct {
			Repo    string   `json:"repo"`
			Paths   []string `json:"paths"`
			Unstage bool     `json:"unstage"`
		}
		if !DecodeJSON(w, r, &in) {
			return nil, 0, errAlreadyWritten
		}
		return nil, 0, svc.Stage(ctx, ws, in.Repo, in.Paths, in.Unstage)
	}))
	s.Handle("POST /api/workspaces/{id}/git/discard", run(func(ctx context.Context, ws, actor string, w http.ResponseWriter, r *http.Request) (any, int, error) {
		var in struct {
			Repo  string   `json:"repo"`
			Paths []string `json:"paths"`
		}
		if !DecodeJSON(w, r, &in) {
			return nil, 0, errAlreadyWritten
		}
		return nil, 0, svc.Discard(ctx, ws, actor, in.Repo, in.Paths)
	}))
	s.Handle("POST /api/workspaces/{id}/git/commit", run(func(ctx context.Context, ws, actor string, w http.ResponseWriter, r *http.Request) (any, int, error) {
		var in struct {
			Repo    string `json:"repo"`
			Message string `json:"message"`
		}
		if !DecodeJSON(w, r, &in) {
			return nil, 0, errAlreadyWritten
		}
		return nil, 0, svc.Commit(ctx, ws, actor, in.Repo, in.Message, author(r))
	}))
	for _, op := range []string{"fetch", "pull", "push"} {
		s.Handle("POST /api/workspaces/{id}/git/"+op, run(func(ctx context.Context, ws, actor string, w http.ResponseWriter, r *http.Request) (any, int, error) {
			var in repoBody
			if !DecodeJSON(w, r, &in) {
				return nil, 0, errAlreadyWritten
			}
			out, err := svc.Sync(ctx, ws, actor, in.Repo, op)
			return map[string]any{"output": out}, 200, err
		}))
	}
	s.Handle("GET /api/workspaces/{id}/git/diff", run(func(ctx context.Context, ws, _ string, _ http.ResponseWriter, r *http.Request) (any, int, error) {
		d, trunc, err := svc.Diff(ctx, ws, q(r, "repo"), q(r, "path"), q(r, "staged") == "true")
		return map[string]any{"diff": d, "truncated": trunc}, 200, err
	}))
	s.Handle("GET /api/workspaces/{id}/git/log", run(func(ctx context.Context, ws, _ string, _ http.ResponseWriter, r *http.Request) (any, int, error) {
		c, err := svc.Log(ctx, ws, q(r, "repo"), 30)
		return map[string]any{"commits": c}, 200, err
	}))
	s.Handle("GET /api/workspaces/{id}/git/pr-url", run(func(ctx context.Context, ws, _ string, _ http.ResponseWriter, r *http.Request) (any, int, error) {
		u, err := svc.PullRequestURL(ctx, ws, q(r, "repo"))
		return map[string]any{"url": u}, 200, err
	}))
}

var errAlreadyWritten = errors.New("response already written")
