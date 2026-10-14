package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/definition"
	"github.com/nexio-developer-group/nexmesh/server/internal/devcontainer"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// RegisterDevContainer mounts dev container import and export (PRD F6).
func (s *Server) RegisterDevContainer(defs *definition.Service, cat *catalog.Catalog) {
	s.Handle("POST /api/devcontainer/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content       string `json:"content"`
			Name          string `json:"name"`
			AutoInstall   bool   `json:"autoInstall"`
			RunPostCreate bool   `json:"runPostCreate"`
			DryRun        bool   `json:"dryRun"`
		}
		if !DecodeJSONLimit(w, r, &in, 1<<20) {
			return
		}
		imp, err := devcontainer.Import([]byte(in.Content), cat)
		if err != nil {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "unreadable_devcontainer", "message": err.Error()}})
			return
		}
		opts := definition.ImportOptions{Name: in.Name, AutoInstall: in.AutoInstall, DryRun: in.DryRun}
		if in.RunPostCreate {
			opts.PostCreate = imp.PostCreate
		}
		res, err := defs.Import(r.Context(), Owner(r), imp.Definition, opts)
		if err != nil {
			var inv *definition.InvalidError
			switch {
			case errors.As(err, &inv):
				WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "invalid_definition", "message": err.Error(), "issues": inv.Issues}})
			case errors.Is(err, workspace.ErrNameTaken):
				WriteJSON(w, http.StatusConflict, map[string]ValidationBody{"error": {Code: "name_taken", Message: err.Error(), Field: "name"}})
			default:
				s.HandleError(w, err)
			}
			return
		}
		status := http.StatusCreated
		if res.DryRun {
			status = http.StatusOK
		}
		WriteJSON(w, status, map[string]any{"workspace": res.Workspace, "todos": res.Todos, "dryRun": res.DryRun,
			"report": imp.Report, "postCreate": imp.PostCreate, "resources": imp.Definition.Workspace, "name": imp.Definition.Workspace.Name})
	})

	s.Handle("GET /api/workspaces/{id}/devcontainer", func(w http.ResponseWriter, r *http.Request) {
		d, err := defs.Export(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		ex, err := devcontainer.Export(d)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if r.URL.Query().Get("format") == "report" {
			WriteJSON(w, http.StatusOK, map[string]any{"report": ex.Report, "file": string(ex.File)})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="devcontainer.json"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(ex.File)
	})
}
