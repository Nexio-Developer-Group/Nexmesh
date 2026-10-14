// Package agentcfg manages code agent model configurations (PRD 12): choose a
// provider and model, bring your own key, or point at a custom or local
// endpoint. Phase 1 stores, tests and selects configurations; what the agent
// does with them is specified in Phase 3.
//
// Keys are encrypted at rest, write-only after saving, absent from logs,
// audit events and errors, and never injected into the workspace (PRD 12.6).
package agentcfg

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

var ErrRateLimited = errors.New("too many configuration tests; wait a moment and try again")

type Capabilities struct {
	Streaming bool `json:"streaming"`
	Tools     bool `json:"tools"`
	Vision    bool `json:"vision"`
}

// Input is one agent model configuration as entered in the UI.
type Input struct {
	Name          string            `json:"name"`
	Provider      string            `json:"provider"`
	ModelID       string            `json:"modelId"`
	Endpoint      string            `json:"endpoint"` // custom endpoint, or a base URL override for hosted providers
	Protocol      string            `json:"protocol"` // custom only
	APIKey        string            `json:"apiKey"`   // write-only
	Organization  string            `json:"organization"`
	Project       string            `json:"project"`
	AuthStyle     string            `json:"authStyle"`
	HeaderName    string            `json:"headerName"`
	CustomHeaders map[string]string `json:"customHeaders"` // values are secrets
	TLSInsecure   bool              `json:"tlsInsecure"`
	CACertificate string            `json:"caCertificate"`
	// Common settings (PRD 12.3).
	MaxOutputTokens int          `json:"maxOutputTokens"`
	Temperature     *float64     `json:"temperature"`
	ContextWindow   int          `json:"contextWindow"`
	TimeoutSeconds  int          `json:"timeoutSeconds"`
	Retries         *int         `json:"retries"`
	Capabilities    Capabilities `json:"capabilities"`
}

// RunFunc performs Test Configuration. Production uses modelprovider.Run.
type RunFunc func(ctx context.Context, p modelprovider.Params, report func(domain.StageResult)) (*domain.TestResult, []modelprovider.Model)

// WithRunner replaces the model test runner (tests).
func (s *Service) WithRunner(fn RunFunc) *Service { s.run = fn; return s }

type Service struct {
	st        store.Store
	sec       *secrets.Service
	guard     *netguard.Guard
	aud       *audit.Recorder
	providers map[string]*catalog.Provider
	order     []*catalog.Provider
	key       []byte

	mu     sync.Mutex
	tested map[string]time.Time // fingerprint -> when it passed
	tests  map[string][]time.Time
	now    func() time.Time
	// TestFreshness bounds how long a passing test authorizes saving.
	TestFreshness time.Duration
	// probe is replaceable in tests.
	run RunFunc
}

func NewService(st store.Store, sec *secrets.Service, g *netguard.Guard, aud *audit.Recorder, providers []*catalog.Provider, fingerprintKey []byte) *Service {
	s := &Service{st: st, sec: sec, guard: g, aud: aud, providers: map[string]*catalog.Provider{}, key: fingerprintKey,
		tested: map[string]time.Time{}, tests: map[string][]time.Time{}, now: time.Now, TestFreshness: 15 * time.Minute, run: modelprovider.Run}
	for _, p := range providers {
		s.providers[p.ID] = p
		s.order = append(s.order, p)
	}
	return s
}

func (s *Service) Providers() []*catalog.Provider { return s.order }

func verr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

// ---- resolving an Input into a model provider call

type resolved struct {
	in       Input
	provider *catalog.Provider
	cfg      modelprovider.Config
}

func (s *Service) resolve(in Input) (*resolved, error) {
	p, ok := s.providers[in.Provider]
	if !ok {
		return nil, verr("provider", "unknown_provider", "Choose a provider.")
	}
	if n := strings.TrimSpace(in.Name); n == "" || len(n) > 64 {
		return nil, verr("name", "invalid_name", "Give this configuration a name of up to 64 characters.")
	}
	if in.MaxOutputTokens < 0 || in.MaxOutputTokens > 1_000_000 {
		return nil, verr("maxOutputTokens", "invalid_value", "Maximum output tokens must be between 1 and 1,000,000.")
	}
	if in.Temperature != nil && (*in.Temperature < 0 || *in.Temperature > 2) {
		return nil, verr("temperature", "invalid_value", "Temperature must be between 0 and 2.")
	}
	if in.TimeoutSeconds < 0 || in.TimeoutSeconds > 600 {
		return nil, verr("timeoutSeconds", "invalid_value", "Timeout must be between 1 and 600 seconds.")
	}
	if in.Retries != nil && (*in.Retries < 0 || *in.Retries > 5) {
		return nil, verr("retries", "invalid_value", "Retries must be between 0 and 5.")
	}
	if in.ContextWindow < 0 {
		return nil, verr("contextWindow", "invalid_value", "Context window must be positive.")
	}
	cfg := modelprovider.Config{
		ModelID: strings.TrimSpace(in.ModelID), APIKey: in.APIKey, AuthStyle: in.AuthStyle, HeaderName: in.HeaderName,
		CustomHeaders: in.CustomHeaders, Organization: in.Organization, Project: in.Project,
		TLSInsecure: in.TLSInsecure, CACertificate: in.CACertificate,
	}
	if p.Hosted {
		cfg.Protocol = p.Protocols[0]
		cfg.BaseURL = strings.TrimSpace(in.Endpoint)
		if cfg.BaseURL == "" {
			cfg.BaseURL = p.BaseURL
		}
		cfg.Timeout = 30 * time.Second
	} else {
		cfg.Protocol, cfg.BaseURL = in.Protocol, strings.TrimSpace(in.Endpoint)
		cfg.Timeout = 60 * time.Second // local models can be slow
		if cfg.Protocol != "" {
			ok := false
			for _, x := range p.Protocols {
				ok = ok || x == cfg.Protocol
			}
			if !ok {
				return nil, verr("protocol", "unsupported_protocol", "Choose OpenAI-compatible, Anthropic-compatible or Ollama.")
			}
		}
	}
	if in.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	return &resolved{in: in, provider: p, cfg: cfg}, nil
}

func (s *Service) params(r *resolved) modelprovider.Params {
	for _, v := range r.in.CustomHeaders {
		s.sec.Register(v)
	}
	if r.in.APIKey != "" {
		s.sec.Register(r.in.APIKey)
	}
	return modelprovider.Params{Provider: r.provider.ID, Config: r.cfg, Guard: s.guard, Scrub: s.sec.Scrub}
}

func (s *Service) fingerprint(owner string, r *resolved) string {
	h := hmac.New(sha256.New, s.key)
	in := r.in
	b, _ := json.Marshal(struct {
		Owner string
		In    Input
		Cfg   modelprovider.Config
	}{owner, in, r.cfg})
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) allow(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var recent []time.Time
	for _, t := range s.tests[key] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 || (len(recent) > 0 && now.Sub(recent[len(recent)-1]) < time.Second) {
		s.tests[key] = recent
		return false
	}
	s.tests[key] = append(recent, now)
	return true
}

func (s *Service) markTested(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, t := range s.tested {
		if now.Sub(t) > s.TestFreshness {
			delete(s.tested, k)
		}
	}
	s.tested[fp] = now
}

func (s *Service) isTested(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tested[fp]
	return ok && s.now().Sub(t) <= s.TestFreshness
}

// TestResult is a Test Configuration outcome plus the models the key can use.
type TestResult struct {
	*domain.TestResult
	Models []modelprovider.Model `json:"models,omitempty"`
}

// Test runs Test Configuration for an unsaved configuration (used on the
// Create Code Space screen and in the add/edit form). A pass authorizes saving
// exactly these values.
func (s *Service) Test(ctx context.Context, owner string, in Input) (*TestResult, error) {
	r, err := s.resolve(in)
	if err != nil {
		return nil, err
	}
	if !s.allow(owner) {
		return nil, ErrRateLimited
	}
	res, models := s.run(ctx, s.params(r), nil)
	res.Fingerprint = s.fingerprint(owner, r)
	if res.Passed {
		s.markTested(res.Fingerprint)
	}
	return &TestResult{TestResult: res, Models: models}, nil
}

// Models lists the models available to the key for the dropdown. It runs only
// after the credentials validate; the UI falls back to manual entry on an empty list.
func (s *Service) Models(ctx context.Context, owner string, in Input) ([]modelprovider.Model, error) {
	in.ModelID = "list"
	if in.Name == "" {
		in.Name = "list"
	}
	r, err := s.resolve(in)
	if err != nil {
		return nil, err
	}
	if !s.allow(owner + "/models") {
		return nil, ErrRateLimited
	}
	ms, e := modelprovider.ListModels(ctx, s.params(r))
	if e != nil {
		return nil, verr("apiKey", string(e.Kind), e.Msg)
	}
	return ms, nil
}

// RequireTested errors unless these exact values passed Test Configuration.
func (s *Service) RequireTested(owner string, in Input) error {
	r, err := s.resolve(in)
	if err != nil {
		return err
	}
	if !s.isTested(s.fingerprint(owner, r)) {
		return verr("test", "test_required", "Test Configuration must pass for exactly these values before they can be saved.")
	}
	return nil
}

// ---- persistence

func secretID(id string) string { return "agentsec_" + id }

func (s *Service) secretMap(in Input) map[string]string {
	m := map[string]string{}
	if in.APIKey != "" {
		m["apiKey"] = in.APIKey
	}
	for k, v := range in.CustomHeaders {
		m["hdr:"+k] = v
	}
	return m
}

func paramsOf(in Input) map[string]any {
	names := make([]string, 0, len(in.CustomHeaders))
	for k := range in.CustomHeaders {
		names = append(names, k)
	}
	sort.Strings(names)
	p := map[string]any{
		"maxOutputTokens": in.MaxOutputTokens, "contextWindow": in.ContextWindow, "timeoutSeconds": in.TimeoutSeconds,
		"authStyle": in.AuthStyle, "headerName": in.HeaderName, "customHeaderNames": names,
		"tlsInsecure": in.TLSInsecure, "caCertificate": in.CACertificate,
		"organization": in.Organization, "project": in.Project, "capabilities": in.Capabilities,
	}
	if in.Temperature != nil {
		p["temperature"] = *in.Temperature
	}
	if in.Retries != nil {
		p["retries"] = *in.Retries
	}
	return p
}

// Create saves a tested configuration for a workspace. The first one becomes the default.
func (s *Service) Create(ctx context.Context, owner, wsID string, in Input) (*domain.AgentConfig, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	if err := s.RequireTested(owner, in); err != nil {
		return nil, err
	}
	if err := s.uniqueName(ctx, wsID, "", in.Name); err != nil {
		return nil, err
	}
	existing, _ := s.st.ListAgentConfigs(ctx, wsID)
	id := store.NewID("agt")
	if m := s.secretMap(in); len(m) > 0 {
		if err := s.sec.Put(ctx, secretID(id), m); err != nil {
			return nil, err
		}
	}
	c := &domain.AgentConfig{ID: id, WorkspaceID: wsID, Name: strings.TrimSpace(in.Name), Provider: in.Provider, ModelID: strings.TrimSpace(in.ModelID),
		Endpoint: strings.TrimSpace(in.Endpoint), Protocol: in.Protocol, Params: paramsOf(in), IsDefault: len(existing) == 0, CreatedAt: s.now().UTC()}
	if len(s.secretMap(in)) > 0 {
		c.SecretRef = secretID(id)
	}
	c.LastTest = s.lastPassed(owner, in)
	if err := s.st.CreateAgentConfig(ctx, c); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "agent_config.create", c.Provider, map[string]any{"configId": id, "name": c.Name, "model": c.ModelID, "provider": c.Provider, "default": c.IsDefault})
	return c, nil
}

func (s *Service) lastPassed(owner string, in Input) *domain.TestResult {
	r, err := s.resolve(in)
	if err != nil {
		return nil
	}
	return &domain.TestResult{Passed: true, At: s.now().UTC(), Fingerprint: s.fingerprint(owner, r), Stages: []domain.StageResult{}}
}

func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

func (s *Service) config(ctx context.Context, owner, wsID, id string) (*domain.AgentConfig, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	c, err := s.st.GetAgentConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	return c, nil
}

func (s *Service) uniqueName(ctx context.Context, wsID, exceptID, name string) error {
	list, _ := s.st.ListAgentConfigs(ctx, wsID)
	for _, c := range list {
		if c.ID != exceptID && strings.EqualFold(c.Name, strings.TrimSpace(name)) {
			return verr("name", "name_taken", fmt.Sprintf("A configuration named %q already exists.", name))
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, owner, wsID string) ([]*domain.AgentConfig, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	return s.st.ListAgentConfigs(ctx, wsID)
}

// inputOf rebuilds an Input from a saved configuration, with secrets filled in
// from the store (in memory only) so a retest or edit never needs the key again.
func (s *Service) inputOf(ctx context.Context, c *domain.AgentConfig) Input {
	p := c.Params
	in := Input{Name: c.Name, Provider: c.Provider, ModelID: c.ModelID, Endpoint: c.Endpoint, Protocol: c.Protocol,
		AuthStyle: str(p["authStyle"]), HeaderName: str(p["headerName"]), Organization: str(p["organization"]), Project: str(p["project"]),
		TLSInsecure: p["tlsInsecure"] == true, CACertificate: str(p["caCertificate"]),
		MaxOutputTokens: num(p["maxOutputTokens"]), ContextWindow: num(p["contextWindow"]), TimeoutSeconds: num(p["timeoutSeconds"])}
	if v, ok := p["temperature"].(float64); ok {
		in.Temperature = &v
	}
	if v, ok := p["retries"].(float64); ok {
		n := int(v)
		in.Retries = &n
	}
	if cp, ok := p["capabilities"].(map[string]any); ok {
		in.Capabilities = Capabilities{Streaming: cp["streaming"] == true, Tools: cp["tools"] == true, Vision: cp["vision"] == true}
	}
	if c.SecretRef != "" {
		if m, err := s.sec.Get(ctx, c.SecretRef); err == nil {
			in.APIKey = m["apiKey"]
			for k, v := range m {
				if h, ok := strings.CutPrefix(k, "hdr:"); ok {
					if in.CustomHeaders == nil {
						in.CustomHeaders = map[string]string{}
					}
					in.CustomHeaders[h] = v
				}
			}
		}
	}
	return in
}

func str(v any) string { s, _ := v.(string); return s }
func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// merge overlays edits onto the stored configuration: an omitted key keeps the stored one.
func merge(stored, edit Input) Input {
	out := edit
	if out.APIKey == "" {
		out.APIKey = stored.APIKey
	}
	if out.CustomHeaders == nil {
		out.CustomHeaders = stored.CustomHeaders
	}
	return out
}

// Update edits a configuration. Like creation it requires a passing test for
// the resulting values; an omitted key keeps the stored one.
func (s *Service) Update(ctx context.Context, owner, wsID, id string, edit Input) (*domain.AgentConfig, error) {
	c, err := s.config(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	in := merge(s.inputOf(ctx, c), edit)
	if err := s.RequireTested(owner, in); err != nil {
		return nil, err
	}
	if err := s.uniqueName(ctx, wsID, id, in.Name); err != nil {
		return nil, err
	}
	if m := s.secretMap(in); len(m) > 0 {
		if err := s.sec.Put(ctx, secretID(id), m); err != nil {
			return nil, err
		}
	}
	out, err := s.st.UpdateAgentConfig(ctx, id, func(x *domain.AgentConfig) error {
		x.Name, x.Provider, x.ModelID, x.Endpoint, x.Protocol = strings.TrimSpace(in.Name), in.Provider, strings.TrimSpace(in.ModelID), strings.TrimSpace(in.Endpoint), in.Protocol
		x.Params, x.LastTest = paramsOf(in), s.lastPassed(owner, in)
		if len(s.secretMap(in)) > 0 {
			x.SecretRef = secretID(id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "agent_config.update", out.Provider, map[string]any{"configId": id, "name": out.Name, "model": out.ModelID})
	return out, nil
}

// Retest re-runs Test Configuration with the stored credentials.
func (s *Service) Retest(ctx context.Context, owner, wsID, id string) (*TestResult, error) {
	c, err := s.config(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	in := s.inputOf(ctx, c)
	r, err := s.resolve(in)
	if err != nil {
		return nil, err
	}
	if !s.allow(owner + "/" + id) {
		return nil, ErrRateLimited
	}
	res, models := s.run(ctx, s.params(r), nil)
	res.Fingerprint = s.fingerprint(owner, r)
	if _, err := s.st.UpdateAgentConfig(ctx, id, func(x *domain.AgentConfig) error { x.LastTest = res; return nil }); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "agent_config.retest", c.Provider, map[string]any{"configId": id, "passed": res.Passed, "stage": res.FailedStage})
	return &TestResult{TestResult: res, Models: models}, nil
}

// ReplaceKey swaps the API key. The new key must pass a test first; the old
// one stays in place if it does not.
func (s *Service) ReplaceKey(ctx context.Context, owner, wsID, id, newKey string) (*TestResult, *domain.AgentConfig, error) {
	c, err := s.config(ctx, owner, wsID, id)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(newKey) == "" {
		return nil, nil, verr("apiKey", "key_required", "Enter the new API key.")
	}
	in := s.inputOf(ctx, c)
	in.APIKey = newKey
	r, err := s.resolve(in)
	if err != nil {
		return nil, nil, err
	}
	if !s.allow(owner + "/" + id) {
		return nil, nil, ErrRateLimited
	}
	res, models := s.run(ctx, s.params(r), nil)
	res.Fingerprint = s.fingerprint(owner, r)
	if !res.Passed {
		s.aud.User(ctx, owner, wsID, "agent_config.replace_key_failed", c.Provider, map[string]any{"configId": id, "stage": res.FailedStage})
		return &TestResult{TestResult: res, Models: models}, nil, nil
	}
	if err := s.sec.Put(ctx, secretID(id), s.secretMap(in)); err != nil {
		return nil, nil, err
	}
	out, err := s.st.UpdateAgentConfig(ctx, id, func(x *domain.AgentConfig) error { x.SecretRef, x.LastTest = secretID(id), res; return nil })
	if err != nil {
		return nil, nil, err
	}
	s.aud.User(ctx, owner, wsID, "agent_config.replace_key", c.Provider, map[string]any{"configId": id})
	return &TestResult{TestResult: res, Models: models}, out, nil
}

// SetDefault marks one configuration as the workspace default.
func (s *Service) SetDefault(ctx context.Context, owner, wsID, id string) error {
	if _, err := s.config(ctx, owner, wsID, id); err != nil {
		return err
	}
	list, _ := s.st.ListAgentConfigs(ctx, wsID)
	for _, c := range list {
		want := c.ID == id
		if c.IsDefault != want {
			if _, err := s.st.UpdateAgentConfig(ctx, c.ID, func(x *domain.AgentConfig) error { x.IsDefault = want; return nil }); err != nil {
				return err
			}
		}
	}
	s.aud.User(ctx, owner, wsID, "agent_config.set_default", "", map[string]any{"configId": id})
	return nil
}

// Delete removes a configuration and its stored credentials. It does not
// revoke the key at the provider (the UI says so).
func (s *Service) Delete(ctx context.Context, owner, wsID, id string) error {
	c, err := s.config(ctx, owner, wsID, id)
	if err != nil {
		return err
	}
	if c.SecretRef != "" {
		_ = s.sec.Delete(ctx, c.SecretRef)
	}
	if err := s.st.DeleteAgentConfig(ctx, id); err != nil {
		return err
	}
	if c.IsDefault {
		if rest, _ := s.st.ListAgentConfigs(ctx, wsID); len(rest) > 0 {
			_, _ = s.st.UpdateAgentConfig(ctx, rest[0].ID, func(x *domain.AgentConfig) error { x.IsDefault = true; return nil })
		}
	}
	s.aud.User(ctx, owner, wsID, "agent_config.delete", c.Provider, map[string]any{"configId": id, "name": c.Name})
	return nil
}

// TestEdit runs Test Configuration for an edit of a saved configuration. An
// omitted key keeps the stored one, so the user never has to retype it.
func (s *Service) TestEdit(ctx context.Context, owner, wsID, id string, edit Input) (*TestResult, error) {
	c, err := s.config(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	return s.Test(ctx, owner, merge(s.inputOf(ctx, c), edit))
}

// ChatClient returns a ready client for the workspace's chosen configuration
// (the default one when id is empty), plus its saved defaults. The key lives
// only in the returned client's memory and is registered with the scrubber so
// it can never appear in logs or errors (PRD F17, 18.2).
func (s *Service) ChatClient(ctx context.Context, owner, wsID, id string) (*modelprovider.Client, *domain.AgentConfig, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, nil, err
	}
	list, err := s.st.ListAgentConfigs(ctx, wsID)
	if err != nil {
		return nil, nil, err
	}
	var cfg *domain.AgentConfig
	for _, c := range list {
		if (id != "" && c.ID == id) || (id == "" && c.IsDefault) {
			cfg = c
			break
		}
	}
	if cfg == nil && id == "" && len(list) == 1 {
		cfg = list[0]
	}
	if cfg == nil {
		return nil, nil, verr("agentConfig", "no_agent_config", "Choose a code agent in Workspace Settings first.")
	}
	r, err := s.resolve(s.inputOf(ctx, cfg))
	if err != nil {
		return nil, nil, err
	}
	p := s.params(r)
	cl, merr := modelprovider.NewClient(p.Config, p.Guard, p.Scrub)
	if merr != nil {
		return nil, nil, verr("agentConfig", string(merr.Kind), merr.Msg)
	}
	return cl, cfg, nil
}
