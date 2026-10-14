package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/definition"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

func (s *Server) RegisterDefinition(svc *definition.Service) {
	fail := func(w http.ResponseWriter, err error) {
		var inv *definition.InvalidError
		var pe *definition.ParseError
		switch {
		case errors.As(err, &inv):
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "invalid_definition", "message": err.Error(), "issues": inv.Issues}})
		case errors.As(err, &pe):
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "unreadable_definition", "message": pe.Msg, "issues": []definition.Issue{}}})
		default:
			s.HandleError(w, err)
		}
	}
	s.Handle("GET /api/workspaces/{id}/definition", func(w http.ResponseWriter, r *http.Request) {
		d, err := svc.Export(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		if r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Disposition", `attachment; filename="`+d.Workspace.Name+`.nexmesh.json"`)
			WriteJSON(w, http.StatusOK, d)
			return
		}
		b, err := definition.Marshal(d)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+d.Workspace.Name+`.nexmesh.yaml"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	s.Handle("POST /api/workspaces/{id}/drift", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Definition string `json:"definition"`
		}
		if !DecodeJSONLimit(w, r, &in, 2<<20) {
			return
		}
		d, err := definition.Parse([]byte(in.Definition))
		if err != nil {
			fail(w, err)
			return
		}
		rep, err := svc.Drift(r.Context(), Owner(r), r.PathValue("id"), d)
		if err != nil {
			fail(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, rep)
	})
	s.Handle("POST /api/workspaces/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Definition  string `json:"definition"`
			Name        string `json:"name"`
			AutoInstall bool   `json:"autoInstall"`
			DryRun      bool   `json:"dryRun"`
		}
		if !DecodeJSONLimit(w, r, &in, 2<<20) {
			return
		}
		d, err := definition.Parse([]byte(in.Definition))
		if err != nil {
			fail(w, err)
			return
		}
		res, err := svc.Import(r.Context(), Owner(r), d, definition.ImportOptions{Name: in.Name, AutoInstall: in.AutoInstall, DryRun: in.DryRun})
		if err != nil {
			if errors.Is(err, workspace.ErrNameTaken) {
				WriteJSON(w, http.StatusConflict, map[string]ValidationBody{"error": {Code: "name_taken", Message: err.Error(), Field: "name"}})
				return
			}
			fail(w, err)
			return
		}
		status := http.StatusCreated
		if res.DryRun {
			status = http.StatusOK
		}
		WriteJSON(w, status, res)
	})
}
