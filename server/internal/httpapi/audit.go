package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

func auditFilter(r *http.Request) (audit.Filter, bool) {
	q := r.URL.Query()
	f := audit.Filter{ActorType: q.Get("actor"), Action: q.Get("action"), Q: q.Get("q")}
	parse := func(k string, end bool) (time.Time, bool) {
		v := q.Get(k)
		if v == "" {
			return time.Time{}, true
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
		if t, err := time.Parse("2006-01-02", v); err == nil {
			if end {
				t = t.Add(24*time.Hour - time.Nanosecond)
			}
			return t, true
		}
		return time.Time{}, false
	}
	var ok1, ok2 bool
	f.From, ok1 = parse("from", false)
	f.To, ok2 = parse("to", true)
	return f, ok1 && ok2
}

// RegisterAudit mounts the per-workspace activity log (PRD F27).
func (s *Server) RegisterAudit(rec *audit.Recorder, wsSvc *workspace.Service) {
	s.Handle("GET /api/workspaces/{id}/audit", func(w http.ResponseWriter, r *http.Request) {
		ws, err := wsSvc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		f, ok := auditFilter(r)
		if !ok {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: "invalid", Message: "Use dates like 2026-03-31.", Field: "from"}})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		p, err := rec.Query(r.Context(), ws.ID, f, r.URL.Query().Get("after"), limit)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, p)
	})
	s.Handle("GET /api/workspaces/{id}/audit/export", func(w http.ResponseWriter, r *http.Request) {
		ws, err := wsSvc.Get(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		f, ok := auditFilter(r)
		if !ok {
			WriteError(w, http.StatusUnprocessableEntity, "invalid", "Use dates like 2026-03-31.")
			return
		}
		format := r.URL.Query().Get("format")
		ctype, ext := "text/csv; charset=utf-8", "csv"
		if format == "json" {
			ctype, ext = "application/json", "json"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Disposition", `attachment; filename="`+ws.Name+`-activity.`+ext+`"`)
		w.Header().Set("Cache-Control", "no-store")
		if err := rec.Export(r.Context(), ws.ID, f, format, w); err != nil {
			s.log.Error("audit export", "err", err)
		}
		if _, err := wsSvc.Get(r.Context(), Owner(r), ws.ID); err == nil {
			rec.User(r.Context(), Owner(r), ws.ID, "audit.exported", format, nil)
		}
	})
}
