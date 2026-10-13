package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentcfg"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// agentConfigDTO is what clients see: the key is never included, only whether one is stored.
type agentConfigDTO struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Provider  string             `json:"provider"`
	ModelID   string             `json:"modelId"`
	Endpoint  string             `json:"endpoint,omitempty"`
	Protocol  string             `json:"protocol,omitempty"`
	Params    map[string]any     `json:"params"`
	HasKey    bool               `json:"hasKey"`
	LastTest  *domain.TestResult `json:"lastTest,omitempty"`
	IsDefault bool               `json:"isDefault"`
	CreatedAt time.Time          `json:"createdAt"`
}

func toAgentDTO(c *domain.AgentConfig) agentConfigDTO {
	return agentConfigDTO{ID: c.ID, Name: c.Name, Provider: c.Provider, ModelID: c.ModelID, Endpoint: c.Endpoint, Protocol: c.Protocol,
		Params: c.Params, HasKey: c.SecretRef != "", LastTest: c.LastTest, IsDefault: c.IsDefault, CreatedAt: c.CreatedAt}
}

// RegisterAgent mounts code agent configuration endpoints (PRD 12).
func (s *Server) RegisterAgent(svc *agentcfg.Service) {
	s.Handle("GET /api/agent/providers", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"providers": svc.Providers()})
	})

	s.Handle("POST /api/agent/test", func(w http.ResponseWriter, r *http.Request) {
		var in agentcfg.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		res, err := svc.Test(r.Context(), Owner(r), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	s.Handle("POST /api/agent/models", func(w http.ResponseWriter, r *http.Request) {
		var in agentcfg.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		ms, err := svc.Models(r.Context(), Owner(r), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"models": ms})
	})

	base := "/api/workspaces/{id}/agent-configs"

	s.Handle("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context(), Owner(r), r.PathValue("id"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		out := make([]agentConfigDTO, len(list))
		for i, c := range list {
			out[i] = toAgentDTO(c)
		}
		WriteJSON(w, http.StatusOK, map[string]any{"configs": out})
	})

	s.Handle("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		var in agentcfg.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.Create(r.Context(), Owner(r), r.PathValue("id"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, toAgentDTO(c))
	})

	s.Handle("PUT "+base+"/{aid}", func(w http.ResponseWriter, r *http.Request) {
		var in agentcfg.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		c, err := svc.Update(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("aid"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, toAgentDTO(c))
	})

	s.Handle("POST "+base+"/{aid}/test", func(w http.ResponseWriter, r *http.Request) {
		var in agentcfg.Input
		if !DecodeJSON(w, r, &in) {
			return
		}
		res, err := svc.TestEdit(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("aid"), in)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	s.Handle("DELETE "+base+"/{aid}", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Delete(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("aid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.Handle("POST "+base+"/{aid}/default", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.SetDefault(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("aid")); err != nil {
			s.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.Handle("POST "+base+"/{aid}/retest", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.Retest(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("aid"))
		if err != nil {
			s.HandleError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	s.Handle("PUT "+base+"/{aid}/key", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			APIKey string `json:"apiKey"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		res, c, err := svc.ReplaceKey(r.Context(), Owner(r), r.PathValue("id"), r.PathValue("aid"), in.APIKey)
		if err != nil {
			s.HandleError(w, err)
			return
		}
		if !res.Passed {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ErrorBody{Code: "test_failed", Message: res.Message}, "result": res})
			return
		}
		WriteJSON(w, http.StatusOK, toAgentDTO(c))
	})
}

func init() {
	extraErrors = append(extraErrors, func(err error) (int, string, bool) {
		if errors.Is(err, agentcfg.ErrRateLimited) {
			return http.StatusTooManyRequests, "rate_limited", true
		}
		return 0, "", false
	})
}
