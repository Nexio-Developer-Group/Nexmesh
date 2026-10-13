// Package modelprovider talks to AI model providers for Test Configuration
// and model discovery (PRD 12.3, 12.4). Phase 1 only needs configuration and
// testing; the Phase 3 agent runtime reuses these adapters. Requests run from
// the control plane through the SSRF guard, so endpoints need no CORS support.
package modelprovider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
)

// Protocols the adapters speak.
const (
	ProtoAnthropic = "anthropic"
	ProtoOpenAI    = "openai"
	ProtoGemini    = "gemini"
	ProtoOllama    = "ollama"
)

// Config is everything needed to call one model. APIKey and header values are
// secrets: they live only in memory for the duration of a call.
type Config struct {
	Protocol      string
	BaseURL       string
	APIKey        string
	ModelID       string
	AuthStyle     string // "bearer" (default) or "header"
	HeaderName    string // used when AuthStyle == "header"
	CustomHeaders map[string]string
	Organization  string // OpenAI
	Project       string // OpenAI
	TLSInsecure   bool
	CACertificate string
	Timeout       time.Duration
}

type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
}

// Kind classifies a failure so the UI can say something specific.
type Kind string

const (
	KindInvalidInput Kind = "invalid_input"
	KindBlocked      Kind = "blocked"
	KindUnreachable  Kind = "unreachable"
	KindTLS          Kind = "tls"
	KindTimeout      Kind = "timeout"
	KindUnauthorized Kind = "unauthorized"
	KindForbidden    Kind = "forbidden"
	KindModel        Kind = "model_unavailable"
	KindQuota        Kind = "quota"
	KindRateLimit    Kind = "rate_limited"
	KindProtocol     Kind = "protocol"
	KindServer       Kind = "server"
	KindUnsupported  Kind = "unsupported"
)

// Error carries a user-safe message. It never contains the API key or any
// request content.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(k Kind, format string, a ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, a...)}
}

// Client executes provider HTTP calls through the guard.
type Client struct {
	cfg   Config
	http  *http.Client
	scrub func(string) string
}

func NewClient(cfg Config, g *netguard.Guard, scrub func(string) string) (*Client, *Error) {
	if scrub == nil {
		scrub = func(s string) string { return s }
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var tc *tls.Config
	if cfg.TLSInsecure || cfg.CACertificate != "" {
		tc = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecure} //nolint:gosec // explicit, visible user opt-out (PRD 12.4)
		if cfg.CACertificate != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(cfg.CACertificate)) {
				return nil, errf(KindInvalidInput, "The CA certificate is not valid PEM.")
			}
			tc.RootCAs = pool
		}
	}
	return &Client{cfg: cfg, http: g.HTTPClientTLS(timeout, tc), scrub: scrub}, nil
}

func (c *Client) endpoint(path string) string {
	return strings.TrimRight(c.cfg.BaseURL, "/") + path
}

// do sends a request. Success returns the (size-limited) body; failures are
// classified. Error bodies are parsed only for the provider's own message.
func (c *Client) do(ctx context.Context, method, path string, body any, setAuth func(*http.Request)) ([]byte, *Error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), rd)
	if err != nil {
		return nil, errf(KindInvalidInput, "The endpoint URL is not valid.")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if setAuth != nil {
		setAuth(req)
	}
	for k, v := range c.cfg.CustomHeaders {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, c.classifyTransport(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return data, nil
	}
	return nil, c.classifyStatus(res.StatusCode, data)
}

func (c *Client) classifyTransport(err error) *Error {
	var be *netguard.BlockedError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ne net.Error
	var ue *url.Error
	switch {
	case errors.As(err, &be):
		return errf(KindBlocked, "Connections to %s are not allowed: %s.", be.Addr, be.Reason)
	case errors.As(err, &ua):
		return errf(KindTLS, "The endpoint's certificate is not trusted. Add its CA certificate under TLS settings.")
	case errors.As(err, &he):
		return errf(KindTLS, "The endpoint's certificate is not valid for its host name.")
	case strings.Contains(err.Error(), "certificate"), strings.Contains(err.Error(), "tls:"):
		return errf(KindTLS, "The TLS handshake with the endpoint failed.")
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) || (errors.As(err, &ue) && ue.Timeout()):
		return errf(KindTimeout, "The endpoint did not respond in time.")
	case strings.Contains(err.Error(), "refused"):
		return errf(KindUnreachable, "Connection refused. Check the endpoint address and port, and that the server is running.")
	case strings.Contains(err.Error(), "no such host"):
		return errf(KindUnreachable, "The endpoint host could not be found.")
	}
	return errf(KindUnreachable, "The endpoint could not be reached.")
}

// providerMessage extracts error.message / error / message from common error bodies.
func providerMessage(body []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	if len(v.Error) > 0 {
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(v.Error, &o) == nil && o.Message != "" {
			return o.Message
		}
		var s string
		if json.Unmarshal(v.Error, &s) == nil {
			return s
		}
	}
	return v.Message
}

func (c *Client) classifyStatus(code int, body []byte) *Error {
	msg := c.scrub(strings.TrimSpace(providerMessage(body)))
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	low := strings.ToLower(msg)
	switch {
	case code == 401:
		return errf(KindUnauthorized, "The key was rejected (invalid or revoked).")
	case code == 403:
		return errf(KindForbidden, "The key is valid but not permitted to do this (check its permissions and organization).")
	case code == 404:
		return errf(KindModel, "Not found. The model or endpoint path is not available to this key.")
	case code == 429 && (strings.Contains(low, "quota") || strings.Contains(low, "billing") || strings.Contains(low, "credit")):
		return errf(KindQuota, "Quota or credit exceeded on the provider account.")
	case code == 429:
		return errf(KindRateLimit, "The provider is rate limiting this key. Try again shortly.")
	case code >= 500:
		return errf(KindServer, "The provider returned a server error (%d). Try again later.", code)
	}
	if msg != "" {
		return errf(KindProtocol, "The provider rejected the request: %s", msg)
	}
	return errf(KindProtocol, "The provider rejected the request (HTTP %d).", code)
}

// Adapter is one wire protocol.
type Adapter interface {
	ListModels(ctx context.Context, c *Client) ([]Model, *Error)
	// Ping sends the smallest possible request to the configured model.
	Ping(ctx context.Context, c *Client) *Error
}

// ForProtocol returns the adapter for a protocol name.
func ForProtocol(p string) (Adapter, bool) {
	switch p {
	case ProtoAnthropic:
		return anthropicAdapter{}, true
	case ProtoOpenAI:
		return openaiAdapter{}, true
	case ProtoGemini:
		return geminiAdapter{}, true
	case ProtoOllama:
		return ollamaAdapter{}, true
	}
	return nil, false
}

// DefaultBaseURL for hosted providers.
func DefaultBaseURL(protocol string) string {
	switch protocol {
	case ProtoAnthropic:
		return "https://api.anthropic.com"
	case ProtoOpenAI:
		return "https://api.openai.com/v1"
	case ProtoGemini:
		return "https://generativelanguage.googleapis.com"
	}
	return ""
}
