package modelprovider

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
)

// Stages of Test Configuration (PRD 12.3 step 4), in order.
const (
	StageInput   = "input_validation"
	StageNetwork = "network"
	StageAuth    = "authentication"
	StageModel   = "model"
	StageRequest = "request"
)

var StageOrder = []string{StageInput, StageNetwork, StageAuth, StageModel, StageRequest}

var StageLabels = map[string]string{
	StageInput: "Required fields", StageNetwork: "Endpoint reachable", StageAuth: "Key accepted", StageModel: "Model available", StageRequest: "Test request",
}

var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// Hosted reports whether the protocol points at a first-party hosted API (an
// API key is mandatory there; custom endpoints may need none).
func hosted(cfg Config, provider string) bool { return provider != "custom" }

// Params for Run.
type Params struct {
	Provider string // anthropic | openai | google | custom
	Config   Config
	Guard    *netguard.Guard
	Scrub    func(string) string
}

// Validate checks required fields and returns a user-safe error.
func Validate(p Params) *Error {
	c := p.Config
	if _, ok := ForProtocol(c.Protocol); !ok {
		return errf(KindInvalidInput, "Choose how to call this model (OpenAI-compatible, Anthropic-compatible or Ollama).")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errf(KindInvalidInput, "The endpoint must be a full http:// or https:// URL.")
	}
	if u.User != nil {
		return errf(KindInvalidInput, "Do not put credentials in the endpoint URL. Use the key field.")
	}
	if strings.TrimSpace(c.ModelID) == "" {
		return errf(KindInvalidInput, "Model is required.")
	}
	if hosted(c, p.Provider) && strings.TrimSpace(c.APIKey) == "" {
		return errf(KindInvalidInput, "API key is required for this provider.")
	}
	if c.AuthStyle == "header" && !headerNameRe.MatchString(c.HeaderName) {
		return errf(KindInvalidInput, "Enter a valid header name for the key.")
	}
	for k := range c.CustomHeaders {
		if !headerNameRe.MatchString(k) {
			return errf(KindInvalidInput, "A custom header name is not valid.")
		}
	}
	return nil
}

func hostWarnings(c Config, provider string) []string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil
	}
	var w []string
	h := u.Hostname()
	private := h == "localhost"
	if a, err := netip.ParseAddr(h); err == nil {
		private = a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
	}
	if private {
		w = append(w, "This address is on a loopback or private network. It is reachable only if the control plane (and the workspace) can reach it; a model running on your own machine is not reachable from a remote workspace.")
	}
	if u.Scheme == "http" && !private {
		w = append(w, "This endpoint uses http://. Prompts and your key travel unencrypted.")
	}
	if provider != "custom" {
		w = append(w, "Prompts and project context are sent to this provider. Usage and rate limits belong to your provider account.")
	}
	return w
}

func normalizeModel(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
	return strings.TrimSuffix(id, ":latest")
}

// Run executes Test Configuration. It returns the result and, when the
// endpoint offers a model list, the models available to this key.
func Run(ctx context.Context, p Params, report func(domain.StageResult)) (*domain.TestResult, []Model) {
	if p.Scrub == nil {
		p.Scrub = func(s string) string { return s }
	}
	res := &domain.TestResult{At: time.Now().UTC(), Warnings: hostWarnings(p.Config, p.Provider)}
	for _, s := range StageOrder {
		res.Stages = append(res.Stages, domain.StageResult{Stage: s, Status: "pending"})
	}
	set := func(stage, status, msg string) {
		for i := range res.Stages {
			if res.Stages[i].Stage == stage {
				res.Stages[i].Status, res.Stages[i].Message = status, p.Scrub(msg)
				if report != nil {
					report(res.Stages[i])
				}
			}
		}
	}
	fail := func(stage string, e *Error) (*domain.TestResult, []Model) {
		set(stage, "failed", e.Msg)
		res.FailedStage, res.Message = stage, p.Scrub(e.Msg)
		after := false
		for _, s := range StageOrder {
			if s == stage {
				after = true
				continue
			}
			if after {
				set(s, "skipped", "Not run because an earlier check failed.")
			}
		}
		return res, nil
	}

	set(StageInput, "running", "")
	if e := Validate(p); e != nil {
		return fail(StageInput, e)
	}
	set(StageInput, "passed", "All required fields present.")

	set(StageNetwork, "running", "")
	u, _ := url.Parse(p.Config.BaseURL)
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	conn, err := p.Guard.DialContext(dctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	cancel()
	if err != nil {
		cl := &Client{scrub: p.Scrub}
		return fail(StageNetwork, cl.classifyTransport(err))
	}
	_ = conn.Close()
	set(StageNetwork, "passed", fmt.Sprintf("Connected to %s:%s.", u.Hostname(), port))

	cl, e := NewClient(p.Config, p.Guard, p.Scrub)
	if e != nil {
		return fail(StageInput, e)
	}
	ad, _ := ForProtocol(p.Config.Protocol)

	set(StageAuth, "running", "")
	var models []Model
	listed := false
	ms, lerr := ad.ListModels(ctx, cl)
	switch {
	case lerr == nil:
		models, listed = ms, true
		set(StageAuth, "passed", "The key was accepted.")
	case lerr.Kind == KindModel || lerr.Kind == KindProtocol:
		// Some compatible servers have no model list. The test request below checks the key.
		set(StageAuth, "passed", "This endpoint has no model list; the key is checked by the test request.")
	default:
		return fail(StageAuth, lerr)
	}

	set(StageModel, "running", "")
	if listed {
		want := normalizeModel(p.Config.ModelID)
		found := false
		for _, m := range models {
			if normalizeModel(m.ID) == want {
				found = true
			}
		}
		if !found {
			msg := fmt.Sprintf("Model %q is not available to this key.", p.Config.ModelID)
			if len(models) > 0 {
				var some []string
				for i, m := range models {
					if i >= 5 {
						break
					}
					some = append(some, m.ID)
				}
				msg += " Available models include: " + strings.Join(some, ", ") + "."
			}
			e := errf(KindModel, "%s", msg)
			r, _ := fail(StageModel, e)
			return r, models
		}
		set(StageModel, "passed", fmt.Sprintf("%s is available.", p.Config.ModelID))
	} else {
		set(StageModel, "passed", "Not verified from a list; the test request confirms it.")
	}

	set(StageRequest, "running", "")
	start := time.Now()
	if e := ad.Ping(ctx, cl); e != nil {
		r, _ := fail(StageRequest, e)
		return r, models
	}
	res.LatencyMS = time.Since(start).Milliseconds()
	set(StageRequest, "passed", fmt.Sprintf("A minimal request succeeded in %d ms.", res.LatencyMS))
	res.Passed = true
	return res, models
}

// ListModels fetches the model list for the dropdown after the credentials
// validate. It returns an empty list (not an error) when the endpoint has none.
func ListModels(ctx context.Context, p Params) ([]Model, *Error) {
	c := p.Config
	c.ModelID = "list"
	p.Config = c
	if e := Validate(p); e != nil {
		return nil, e
	}
	cl, e := NewClient(p.Config, p.Guard, p.Scrub)
	if e != nil {
		return nil, e
	}
	ad, _ := ForProtocol(p.Config.Protocol)
	ms, lerr := ad.ListModels(ctx, cl)
	if lerr != nil && (lerr.Kind == KindModel || lerr.Kind == KindProtocol) {
		return []Model{}, nil
	}
	return ms, lerr
}
