package modelprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

func decode(body []byte, v any) *Error {
	if err := json.Unmarshal(body, v); err != nil {
		return errf(KindProtocol, "The endpoint answered, but not in the expected %s format.", "JSON")
	}
	return nil
}

func sortModels(m []Model) []Model {
	sort.Slice(m, func(i, j int) bool { return m[i].ID < m[j].ID })
	return m
}

// ---- Anthropic

type anthropicAdapter struct{}

func (anthropicAdapter) auth(c *Client) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("anthropic-version", "2023-06-01")
		if c.cfg.APIKey != "" {
			applyKey(r, c.cfg, "x-api-key")
		}
	}
}

func (a anthropicAdapter) ListModels(ctx context.Context, c *Client) ([]Model, *Error) {
	body, e := c.do(ctx, http.MethodGet, "/v1/models?limit=100", nil, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if e := decode(body, &out); e != nil {
		return nil, e
	}
	var ms []Model
	for _, m := range out.Data {
		ms = append(ms, Model{ID: m.ID, Name: m.DisplayName})
	}
	return ms, nil
}

func (a anthropicAdapter) Ping(ctx context.Context, c *Client) *Error {
	_, e := c.do(ctx, http.MethodPost, "/v1/messages", map[string]any{
		"model": c.cfg.ModelID, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "ping"}},
	}, a.auth(c))
	return e
}

// ---- OpenAI and OpenAI-compatible servers

type openaiAdapter struct{}

func (openaiAdapter) auth(c *Client) func(*http.Request) {
	return func(r *http.Request) {
		if c.cfg.APIKey != "" {
			applyKey(r, c.cfg, "Authorization")
		}
		if c.cfg.Organization != "" {
			r.Header.Set("OpenAI-Organization", c.cfg.Organization)
		}
		if c.cfg.Project != "" {
			r.Header.Set("OpenAI-Project", c.cfg.Project)
		}
	}
}

// chatModel filters the hosted catalog to chat-capable models; compatible
// servers list only what they serve, so they are passed through.
func chatModel(id string, hosted bool) bool {
	if !hosted {
		return true
	}
	for _, bad := range []string{"embedding", "whisper", "tts", "dall-e", "moderation", "davinci", "babbage", "audio", "transcribe", "image", "realtime", "search"} {
		if strings.Contains(id, bad) {
			return false
		}
	}
	return strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "chatgpt") || strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4") || strings.HasPrefix(id, "o5")
}

func (a openaiAdapter) ListModels(ctx context.Context, c *Client) ([]Model, *Error) {
	body, e := c.do(ctx, http.MethodGet, "/models", nil, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e := decode(body, &out); e != nil {
		return nil, e
	}
	hosted := strings.Contains(c.cfg.BaseURL, "api.openai.com")
	var ms []Model
	for _, m := range out.Data {
		if chatModel(m.ID, hosted) {
			ms = append(ms, Model{ID: m.ID})
		}
	}
	return sortModels(ms), nil
}

func (a openaiAdapter) Ping(ctx context.Context, c *Client) *Error {
	req := map[string]any{"model": c.cfg.ModelID, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 1}
	_, e := c.do(ctx, http.MethodPost, "/chat/completions", req, a.auth(c))
	if e != nil && e.Kind == KindProtocol && strings.Contains(e.Msg, "max_completion_tokens") {
		// Newer models renamed the parameter.
		delete(req, "max_tokens")
		req["max_completion_tokens"] = 16
		_, e = c.do(ctx, http.MethodPost, "/chat/completions", req, a.auth(c))
	}
	return e
}

// ---- Google Gemini API

type geminiAdapter struct{}

func (geminiAdapter) auth(c *Client) func(*http.Request) {
	return func(r *http.Request) {
		if c.cfg.APIKey != "" {
			applyKey(r, c.cfg, "x-goog-api-key")
		}
	}
}

func (a geminiAdapter) ListModels(ctx context.Context, c *Client) ([]Model, *Error) {
	body, e := c.do(ctx, http.MethodGet, "/v1beta/models?pageSize=200", nil, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Models []struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			InputLimit  int      `json:"inputTokenLimit"`
			Methods     []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if e := decode(body, &out); e != nil {
		return nil, e
	}
	var ms []Model
	for _, m := range out.Models {
		gen := false
		for _, x := range m.Methods {
			gen = gen || x == "generateContent"
		}
		if gen {
			ms = append(ms, Model{ID: strings.TrimPrefix(m.Name, "models/"), Name: m.DisplayName, ContextWindow: m.InputLimit})
		}
	}
	return sortModels(ms), nil
}

func (a geminiAdapter) Ping(ctx context.Context, c *Client) *Error {
	_, e := c.do(ctx, http.MethodPost, "/v1beta/models/"+c.cfg.ModelID+":generateContent", map[string]any{
		"contents":         []map[string]any{{"parts": []map[string]string{{"text": "ping"}}}},
		"generationConfig": map[string]any{"maxOutputTokens": 1},
	}, a.auth(c))
	return e
}

// ---- Ollama native API

type ollamaAdapter struct{}

func (ollamaAdapter) auth(c *Client) func(*http.Request) {
	return func(r *http.Request) {
		if c.cfg.APIKey != "" {
			applyKey(r, c.cfg, "Authorization")
		}
	}
}

func (a ollamaAdapter) ListModels(ctx context.Context, c *Client) ([]Model, *Error) {
	body, e := c.do(ctx, http.MethodGet, "/api/tags", nil, a.auth(c))
	if e != nil {
		return nil, e
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if e := decode(body, &out); e != nil {
		return nil, e
	}
	var ms []Model
	for _, m := range out.Models {
		ms = append(ms, Model{ID: m.Name})
	}
	return sortModels(ms), nil
}

func (a ollamaAdapter) Ping(ctx context.Context, c *Client) *Error {
	_, e := c.do(ctx, http.MethodPost, "/api/generate", map[string]any{
		"model": c.cfg.ModelID, "prompt": "ping", "stream": false, "options": map[string]any{"num_predict": 1},
	}, a.auth(c))
	return e
}

// applyKey sets the key per the configured authentication style. Providers
// default to their own header; custom endpoints may choose bearer or a custom header.
func applyKey(r *http.Request, cfg Config, providerHeader string) {
	switch {
	case cfg.AuthStyle == "header" && cfg.HeaderName != "":
		r.Header.Set(cfg.HeaderName, cfg.APIKey)
	case providerHeader == "Authorization" || cfg.AuthStyle == "bearer":
		r.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	default:
		r.Header.Set(providerHeader, cfg.APIKey)
	}
}
