package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/codeagent"
)

// RegisterCodeAgent mounts the Code Agent endpoints (PRD F17): settings,
// conversations, approvals and the context preview.
func (s *Server) RegisterCodeAgent(svc *codeagent.Service) {
	base := "/api/workspaces/{id}/agent"
	s.Handle("GET "+base+"/settings", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.GetSettings(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.Handle("PUT "+base+"/settings", func(w http.ResponseWriter, r *http.Request) {
		var in codeagent.Settings
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.SetSettings(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	// The exact context the agent is given, so the user can see what leaves the workspace.
	s.Handle("GET "+base+"/context", func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.BuildContext(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"context": c, "text": c.Render()})
	})
	s.Handle("GET "+base+"/sessions", func(w http.ResponseWriter, r *http.Request) {
		l, err := svc.ListSessions(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"sessions": l})
	})
	s.Handle("POST "+base+"/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.CreateSession(r.Context(), Owner(r), r.PathValue("id"), in.Title)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v.Public())
	})
	s.Handle("GET "+base+"/sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.GetSession(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v.Public())
	})
	s.Handle("DELETE "+base+"/sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.DeleteSession(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.Handle("POST "+base+"/sessions/{sid}/messages", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text string `json:"text"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		v, err := svc.Send(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid"), in.Text)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, v.Public())
	})
	s.Handle("POST "+base+"/sessions/{sid}/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Stop(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.Handle("POST "+base+"/sessions/{sid}/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CallID  string `json:"callId"`
			Approve bool   `json:"approve"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		if err := svc.Approve(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("sid"), in.CallID, in.Approve); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.Handle("GET "+base+"/proposals", func(w http.ResponseWriter, r *http.Request) {
		l, err := svc.ListProposals(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if l == nil {
			l = []*codeagent.Proposal{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"proposals": l})
	})
	s.Handle("POST "+base+"/proposals/{pid}/accept", func(w http.ResponseWriter, r *http.Request) {
		p, err := svc.AcceptProposal(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("pid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, p)
	})
	s.Handle("POST "+base+"/proposals/{pid}/reject", func(w http.ResponseWriter, r *http.Request) {
		p, err := svc.RejectProposal(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("pid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, p)
	})
}
