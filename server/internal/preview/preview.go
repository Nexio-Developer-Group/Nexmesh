// Package preview serves web servers running inside a workspace to the
// browser, through the agent tunnel (no inbound ports, loopback-only servers
// work, WebSockets for hot reload work).
//
// A preview runs untrusted code (the user's app, or a cloned repository), so
// it must never share an origin with the Nexmesh UI:
//
//   - Subdomain mode (NEXMESH_PREVIEW_DOMAIN set): each port gets its own
//     origin, p<port>-ws-<id>.<domain>. Full browser fidelity. Access is a
//     host-only signed cookie, bootstrapped by a short-lived token.
//   - Path mode (default): served under the Nexmesh origin but with a
//     Content-Security-Policy sandbox (no allow-same-origin), which gives the
//     page an opaque origin that cannot use Nexmesh cookies or storage.
//     Apps that need cookies/localStorage should use subdomain mode.
package preview

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/tunnel"
)

const (
	CookieName      = "nx_pv"
	tokenTTL        = 5 * time.Minute
	cookieTTL       = 8 * time.Hour
	sandboxCSP      = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads allow-pointer-lock"
	maxPort         = 65535
	userCookieNames = "nexmesh_session"
)

var ErrDenied = errors.New("not allowed")

type Config struct {
	// Domain enables subdomain mode, e.g. "preview.example.com" or "localhost".
	Domain string
	// Key signs tokens and cookies.
	Key []byte
}

type Service struct {
	cfg  Config
	st   store.Store
	dial *tunnel.Dialer
	now  func() time.Time

	shareOn bool
	aud     *audit.Recorder
}

func NewService(cfg Config, st store.Store, d *tunnel.Dialer) *Service {
	cfg.Domain = strings.ToLower(strings.Trim(cfg.Domain, ". "))
	return &Service{cfg: cfg, st: st, dial: d, now: time.Now}
}

// SubdomainMode reports whether previews get their own origin.
func (s *Service) SubdomainMode() bool { return s.cfg.Domain != "" }

// --- signed tokens ---

type claims struct {
	WS   string `json:"w"`
	Port int    `json:"p"`
	Kind string `json:"k"`           // "user" | "share"
	Ref  string `json:"r,omitempty"` // share link id
	Exp  int64  `json:"e"`
}

func (s *Service) sign(c claims) string {
	b, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, s.cfg.Key)
	m.Write([]byte("preview|" + body))
	return body + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Service) verify(tok string) (*claims, bool) {
	body, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return nil, false
	}
	m := hmac.New(sha256.New, s.cfg.Key)
	m.Write([]byte("preview|" + body))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, false
	}
	var c claims
	if json.Unmarshal(raw, &c) != nil || s.now().Unix() > c.Exp {
		return nil, false
	}
	return &c, true
}

// --- URLs ---

func hostLabel(wsID string, port int) string {
	return "p" + strconv.Itoa(port) + "-" + strings.ReplaceAll(wsID, "_", "-")
}

var hostRE = regexp.MustCompile(`^p([0-9]{1,5})-ws-([a-f0-9]+)$`)

// MatchHost parses a preview host name; ok is false for any other host.
func (s *Service) MatchHost(host string) (wsID string, port int, ok bool) {
	if s.cfg.Domain == "" {
		return "", 0, false
	}
	h := strings.ToLower(host)
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	label, rest, found := strings.Cut(h, ".")
	if !found || rest != s.cfg.Domain {
		return "", 0, false
	}
	m := hostRE.FindStringSubmatch(label)
	if m == nil {
		return "", 0, false
	}
	p, _ := strconv.Atoi(m[1])
	if p < 1 || p > maxPort {
		return "", 0, false
	}
	return "ws_" + m[2], p, true
}

// origin builds the preview origin from how the browser reached the control
// plane (scheme and port are reused; only the host changes).
func (s *Service) origin(r *http.Request, wsID string, port int) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	hostport := ""
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		hostport = ":" + p
	}
	return scheme + "://" + hostLabel(wsID, port) + "." + s.cfg.Domain + hostport
}

// OwnerURL returns a URL the signed-in owner can open: a one-time-token URL in
// subdomain mode, the sandboxed path URL otherwise.
func (s *Service) OwnerURL(r *http.Request, wsID string, port int) (string, time.Time) {
	exp := s.now().Add(tokenTTL)
	if s.SubdomainMode() {
		tok := s.sign(claims{WS: wsID, Port: port, Kind: "user", Exp: exp.Unix()})
		return s.origin(r, wsID, port) + "/?nx_token=" + url.QueryEscape(tok), exp
	}
	return fmt.Sprintf("/api/preview/%s/%d/", wsID, port), exp
}

// --- proxy ---

func (s *Service) proxy(wsID string, port int, prefix string, sandbox bool) *httputil.ReverseProxy {
	upstream := "localhost:" + strconv.Itoa(port)
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = upstream
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, prefix)
			pr.Out.URL.RawPath = ""
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
			pr.Out.Host = upstream
			if o := pr.In.Header.Get("Origin"); o != "" {
				pr.Out.Header.Set("Origin", "http://"+upstream)
			}
			stripCookies(pr.Out)
			pr.Out.Header.Del("X-Forwarded-For")
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
		},
		Transport: &http.Transport{
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 60 * time.Second,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return s.dial.DialContext(ctx, wsID, port)
			},
		},
		ModifyResponse: func(res *http.Response) error {
			if sandbox {
				res.Header.Set("Content-Security-Policy", sandboxCSP)
				if loc := res.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
					res.Header.Set("Location", strings.TrimSuffix(prefix, "/")+loc)
				}
			}
			res.Header.Set("Referrer-Policy", "no-referrer")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeUnavailable(w, port, err)
		},
	}
}

func stripCookies(r *http.Request) {
	cs := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cs {
		if c.Name == CookieName || c.Name == userCookieNames {
			continue
		}
		r.AddCookie(c)
	}
}

func writeUnavailable(w http.ResponseWriter, port int, err error) {
	msg := fmt.Sprintf("Nothing is answering on port %d inside the workspace.", port)
	if err != nil && !strings.Contains(err.Error(), "Nothing is listening") && !strings.Contains(err.Error(), "not connected") {
		msg = "The workspace did not answer this request."
	}
	if err != nil && strings.Contains(err.Error(), "not connected") {
		msg = "The workspace is not running or its agent is not connected."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", sandboxCSP)
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>Preview unavailable</title><body style="font:16px system-ui;margin:3rem auto;max-width:34rem;color:#0b1530"><h1 style="font-size:1.2rem">Preview unavailable</h1><p>%s</p><p>Start your server (listening on this port) and reload this page.</p></body>`, html.EscapeString(msg))
}

func validPort(p int) bool { return p >= 1 && p <= maxPort }

// ServePath handles /api/preview/{ws}/{port}/... for a signed-in owner whose
// ownership of the workspace the caller has already verified.
func (s *Service) ServePath(w http.ResponseWriter, r *http.Request, wsID string, port int) {
	if !validPort(port) {
		http.NotFound(w, r)
		return
	}
	prefix := fmt.Sprintf("/api/preview/%s/%d", wsID, port)
	s.proxy(wsID, port, prefix, true).ServeHTTP(w, r)
}

// ServeHost handles a request whose Host is a preview origin. It returns
// false when the host is not a preview host so the caller can route normally.
func (s *Service) ServeHost(w http.ResponseWriter, r *http.Request) bool {
	wsID, port, ok := s.MatchHost(r.Host)
	if !ok {
		return false
	}
	// Bootstrap: exchange a one-time token (or share secret) for a host-only cookie, then drop it from the URL.
	if tok := r.URL.Query().Get("nx_token"); tok != "" {
		c, ok := s.verify(tok)
		if !ok || c.Kind != "user" || c.WS != wsID || c.Port != port {
			s.deny(w)
			return true
		}
		s.setCookie(w, r, claims{WS: wsID, Port: port, Kind: "user", Exp: s.now().Add(cookieTTL).Unix()})
		s.cleanRedirect(w, r, "nx_token")
		return true
	}
	if secret := r.URL.Query().Get("nx_share"); secret != "" {
		link, ok := s.shareFromSecret(r.Context(), wsID, port, secret)
		if !ok {
			s.deny(w)
			return true
		}
		s.setCookie(w, r, claims{WS: wsID, Port: port, Kind: "share", Ref: link.ID, Exp: minUnix(s.now().Add(cookieTTL), link.ExpiresAt)})
		s.noteShareHit(r.Context(), wsID, link.ID)
		s.cleanRedirect(w, r, "nx_share")
		return true
	}
	ck, err := r.Cookie(CookieName)
	if err != nil {
		s.deny(w)
		return true
	}
	c, ok := s.verify(ck.Value)
	if !ok || c.WS != wsID || c.Port != port || !s.stillValid(r.Context(), c) {
		s.deny(w)
		return true
	}
	s.proxy(wsID, port, "", false).ServeHTTP(w, r)
	return true
}

func minUnix(t time.Time, u time.Time) int64 {
	if u.Before(t) {
		return u.Unix()
	}
	return t.Unix()
}

// stillValid re-checks revocable grants on every request.
func (s *Service) stillValid(ctx context.Context, c *claims) bool {
	switch c.Kind {
	case "user":
		return true
	case "share":
		return s.shareActive(ctx, c.WS, c.Ref, c.Port)
	}
	return false
}

func (s *Service) setCookie(w http.ResponseWriter, r *http.Request, c claims) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.sign(c), Path: "/", Expires: time.Unix(c.Exp, 0), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
}

func (s *Service) cleanRedirect(w http.ResponseWriter, r *http.Request, drop string) {
	q := r.URL.Query()
	q.Del(drop)
	u := *r.URL
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.RequestURI(), http.StatusFound)
}

func (s *Service) deny(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", sandboxCSP)
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><title>Access denied</title><body style="font:16px system-ui;margin:3rem auto;max-width:34rem;color:#0b1530"><h1 style="font-size:1.2rem">This preview link is not valid</h1><p>It may have expired or been revoked. Open the preview again from Nexmesh, or ask the owner for a new link.</p></body>`))
}

// --- share-link hooks (see share.go) ---

func (s *Service) workspace(ctx context.Context, wsID string) (*domain.Workspace, error) {
	return s.st.GetWorkspace(ctx, wsID)
}

// SetClock replaces the time source (tests).
func (s *Service) SetClock(f func() time.Time) { s.now = f }
