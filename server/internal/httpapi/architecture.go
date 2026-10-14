package httpapi

import (
	"net/http"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/architecture"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
)

// RegisterArchitecture mounts the architecture view and its exports (PRD F2).
func (s *Server) RegisterArchitecture(svc *architecture.Service, aud *audit.Recorder) {
	highlight := func(r *http.Request) map[string]bool {
		out := map[string]bool{}
		for _, id := range strings.Split(r.URL.Query().Get("highlight"), ",") {
			if id = strings.TrimSpace(id); id != "" {
				out[id] = true
			}
		}
		return out
	}
	s.Handle("GET /api/workspaces/{id}/architecture", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.Build(r.Context(), Owner(r), r.PathValue("id"), highlight(r))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.Handle("GET /api/workspaces/{id}/architecture/export", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		v, err := svc.Build(r.Context(), Owner(r), id, highlight(r))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		format := r.URL.Query().Get("format")
		var body, ctype, ext string
		switch format {
		case "mermaid":
			body, ctype, ext = architecture.Mermaid(v), "text/plain; charset=utf-8", "mmd"
		case "markdown", "md":
			body, ctype, ext = architecture.Markdown(v), "text/markdown; charset=utf-8", "md"
		case "svg", "":
			body, ctype, ext = architecture.SVG(v), "image/svg+xml", "svg"
			// The image is ours, but never let it run anything if it is opened directly.
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		default:
			WriteError(w, http.StatusUnprocessableEntity, "invalid_format", "Format must be svg, mermaid or markdown.")
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", `attachment; filename="architecture.`+ext+`"`)
			aud.User(r.Context(), Owner(r), id, "architecture.export", ext, map[string]any{"nodes": len(v.Nodes), "edges": len(v.Edges)})
		}
		_, _ = w.Write([]byte(body))
	})
}
