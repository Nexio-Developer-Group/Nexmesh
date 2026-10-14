package probe

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The External API card (PRD F16) is tested with one bounded, read-only GET
// request. Every connection goes through the same SSRF guard as the other
// probes, redirects are not followed, no proxy is used, and the response body
// is read only up to a small limit and then discarded.

const maxBody = 16 << 10

var forbiddenHeaders = map[string]string{
	"host": "the Host header is set from the Base URL", "content-length": "it is set automatically", "transfer-encoding": "it is set automatically",
	"connection": "it is managed automatically", "authorization": "use the Authentication field (bearer or basic) so the secret is stored safely",
	"cookie": "cookies are not supported", "proxy-authorization": "proxies are not supported",
}

// externalTarget validates the External API fields and fills in the target.
func externalTarget(p Params, t *Target) *StageError {
	raw := str(p.Fields, "baseUrl")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return stageErr("Base URL must start with http:// or https:// and include a host name.")
	}
	if u.User != nil {
		return stageErr("Remove the user name and password from the Base URL and use the authentication fields instead.")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return stageErr("Base URL should not contain a query string or fragment.")
	}
	t.Host = u.Hostname()
	t.UseTLS = u.Scheme == "https"
	t.Port = 443
	if !t.UseTLS {
		t.Port = 80
	}
	if ps := u.Port(); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return stageErr("Port must be between 1 and 65535.")
		}
		t.Port = n
	}
	switch at := authType(p); at {
	case "none":
	case "apiKey":
		if str(p.Fields, "apiKey") == "" {
			return stageErr("API key is required when Authentication is apiKey.")
		}
		if h := apiKeyHeader(p); !validHeaderName(h) {
			return stageErr("API key header is not a valid header name.")
		}
	case "bearer":
		if str(p.Fields, "token") == "" {
			return stageErr("Bearer token is required when Authentication is bearer.")
		}
	case "basic":
		if str(p.Fields, "username") == "" || str(p.Fields, "password") == "" {
			return stageErr("Username and password are required when Authentication is basic.")
		}
	default:
		return stageErr("Authentication must be one of: none, apiKey, bearer, basic.")
	}
	if _, e := customHeaders(p); e != nil {
		return e
	}
	if hp := healthPath(p); !strings.HasPrefix(hp, "/") {
		return stageErr("Health check path must start with /.")
	}
	if n := intField(p.Fields, "expectStatus", 200); n < 100 || n > 599 {
		return stageErr("Expected status must be an HTTP status code between 100 and 599.")
	}
	return nil
}

func authType(p Params) string {
	if v := str(p.Fields, "authType"); v != "" {
		return v
	}
	return "none"
}

func apiKeyHeader(p Params) string {
	if v := str(p.Fields, "apiKeyHeader"); v != "" {
		return v
	}
	return "X-API-Key"
}

func healthPath(p Params) string {
	if v := str(p.Fields, "healthPath"); v != "" {
		return v
	}
	return "/"
}

func validHeaderName(n string) bool {
	if n == "" || len(n) > 100 {
		return false
	}
	for _, c := range n {
		if !(c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// customHeaders parses "Name: value" lines.
func customHeaders(p Params) (http.Header, *StageError) {
	h := http.Header{}
	for i, line := range strings.Split(str(p.Fields, "headers"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, val, ok := strings.Cut(line, ":")
		name, val = strings.TrimSpace(name), strings.TrimSpace(val)
		if !ok || !validHeaderName(name) {
			return nil, stageErr("Extra headers, line %d: write each header as Name: value.", i+1)
		}
		if why, bad := forbiddenHeaders[strings.ToLower(name)]; bad {
			return nil, stageErr("Extra headers, line %d: %s cannot be set here (%s).", i+1, name, why)
		}
		if strings.ContainsAny(val, "\r\n\x00") || len(val) > 1024 {
			return nil, stageErr("Extra headers, line %d: the value is not valid.", i+1)
		}
		h.Add(name, val)
	}
	if len(h) > 20 {
		return nil, stageErr("At most 20 extra headers are allowed.")
	}
	return h, nil
}

type httpSession struct {
	p       Params
	client  *http.Client
	req     *http.Request
	status  int
	server  string
	latency time.Duration
	done    bool
	err     *StageError
}

func openHTTP(t *Target, p Params) (session, *StageError) {
	base := str(p.Fields, "baseUrl")
	u, _ := url.Parse(base)
	u.Path = strings.TrimRight(u.Path, "/") + healthPath(p)
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, stageErr("Base URL and health check path do not make a valid address.")
	}
	req.Header.Set("User-Agent", "Nexmesh-connection-test")
	req.Header.Set("Accept", "*/*")
	extra, _ := customHeaders(p)
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	switch authType(p) {
	case "apiKey":
		req.Header.Set(apiKeyHeader(p), str(p.Fields, "apiKey"))
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+str(p.Fields, "token"))
	case "basic":
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(str(p.Fields, "username")+":"+str(p.Fields, "password"))))
	}
	tr := &http.Transport{
		Proxy: nil, // never through an environment proxy: the guard must see every connection
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return p.Guard.DialContext(ctx, network, addr)
		},
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	}
	if t.TLS != nil {
		tr.TLSClientConfig = t.TLS.Clone()
	} else {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cl := &http.Client{Transport: tr, Timeout: 12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &httpSession{p: p, client: cl, req: req}, nil
}

func (s *httpSession) Close() { s.client.CloseIdleConnections() }

// do sends the single request once; the three stages read its outcome.
func (s *httpSession) do(ctx context.Context) {
	if s.done {
		return
	}
	s.done = true
	start := time.Now()
	resp, err := s.client.Do(s.req.WithContext(ctx))
	s.latency = time.Since(start)
	if err != nil {
		var ne net.Error
		msg := s.p.Scrub(firstLine(err))
		switch {
		case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
			s.err = stageErr("The API did not answer in time.")
		default:
			s.err = stageErr("The request failed: %s", msg)
		}
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	s.status = resp.StatusCode
	s.server = resp.Header.Get("Server")
}

func (s *httpSession) Authenticate(ctx context.Context) *StageError {
	s.do(ctx)
	if s.err != nil {
		return s.err
	}
	if s.status == http.StatusUnauthorized {
		if authType(s.p) == "none" {
			return stageErr("The API answered 401 Unauthorized. It needs credentials: choose an Authentication type.")
		}
		return stageErr("The API rejected the credentials (401 Unauthorized). Check the key, token or password.")
	}
	return nil
}

func (s *httpSession) Authorize(ctx context.Context) *StageError {
	s.do(ctx)
	if s.status == http.StatusForbidden {
		return stageErr("The credentials were accepted but are not allowed to read the health check path (403 Forbidden). Use a path the key can read, or grant it access.")
	}
	return nil
}

func (s *httpSession) Details(ctx context.Context) (Details, *StageError) {
	want := intField(s.p.Fields, "expectStatus", 200)
	if s.status != want {
		if s.status >= 300 && s.status < 400 {
			return Details{}, stageErr("The API answered %d (a redirect). Redirects are not followed: set Base URL or the health check path to the final address.", s.status)
		}
		if s.status == http.StatusNotFound {
			return Details{}, stageErr("The API answered 404 Not Found for the health check path. Set Health check path to a URL that exists.")
		}
		return Details{}, stageErr("The API answered %d but %d was expected. Change Expected status if that is normal.", s.status, want)
	}
	info := fmt.Sprintf("HTTP %d", s.status)
	if s.server != "" {
		info += " · " + s.p.Scrub(s.server)
	}
	return Details{Info: info, Latency: s.latency}, nil
}
