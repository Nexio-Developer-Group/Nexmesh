package preview_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/preview"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/tunnel"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

type rig struct {
	ts     *httptest.Server
	pv     *preview.Service
	st     store.Store
	ctx    context.Context
	port   int // upstream dev server inside the "workspace"
	domain string
	aud    *audit.Recorder
}

// newRig wires the real HTTP layer, agent registry, tunnel and a real agent
// serving a loopback-only dev server.
func newRig(t *testing.T, domainName string) *rig {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := store.NewMemory()
	bus := events.NewBus()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws_abc123", OwnerID: "local", Name: "n", Status: domain.WSReady})
	aud := audit.New(st, log)
	reg := agentapi.NewRegistry(st, bus)
	wsSvc := workspace.NewService(st, runtime.NewFake(runtime.Capabilities{MaxCPU: 4, MaxRAMGB: 8, MaxStorageGB: 50}), ops.NewEngine(st, bus, log, 1), bus, aud, log)
	pv := preview.NewService(preview.Config{Domain: domainName, Key: []byte("0123456789abcdef0123456789abcdef")}, st, tunnel.NewDialer(reg))
	api := httpapi.New(log)
	reg.Register(api)
	api.RegisterPorts(httpapi.FilesDeps{Svc: wsSvc, Agent: reg, Audit: aud}, st, pv)
	api.RegisterShares(httpapi.FilesDeps{Svc: wsSvc, Agent: reg, Audit: aud}, pv)
	h := http.Handler(api.Handler())
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pv.ServeHost(w, r) {
			return
		}
		api.Handler().ServeHTTP(w, r)
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	tok, _ := reg.IssueToken(ctx, "ws_abc123")
	ag := agent.New(agent.Config{ControlURL: ts.URL, WorkspaceID: "ws_abc123", Token: tok}, log)
	stop, err := ag.Serve(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	go func() { _ = ag.Run(ctx) }()
	for i := 0; i < 200 && !reg.Online("ws_abc123"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !reg.Online("ws_abc123") {
		t.Fatal("agent offline")
	}

	// The "dev server": loopback only, echoes what it saw, redirects, and speaks WebSocket.
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		c, _ := json.Marshal(map[string]any{"path": r.URL.Path, "host": r.Host, "cookie": r.Header.Get("Cookie"), "origin": r.Header.Get("Origin"), "xfh": r.Header.Get("X-Forwarded-Host")})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(c)
	})
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/hello", http.StatusFound) })
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			_ = c.Write(r.Context(), typ, append([]byte("echo:"), b...))
		}
	})
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return &rig{ts: ts, pv: pv, st: st, ctx: ctx, port: l.Addr().(*net.TCPAddr).Port, domain: domainName, aud: aud}
}

func (r *rig) host() string { return "p" + strconv.Itoa(r.port) + "-ws-abc123." + r.domain }

func (r *rig) get(t *testing.T, path string, hdr map[string]string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", r.ts.URL+path, nil)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func (r *rig) ownerTokenURL(t *testing.T) *url.URL {
	t.Helper()
	req, _ := http.NewRequest("POST", r.ts.URL+"/api/workspaces/ws_abc123/ports/"+strconv.Itoa(r.port)+"/preview", nil)
	req.Host = "localhost:8080"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct{ URL, Mode string }
	_ = json.NewDecoder(res.Body).Decode(&out)
	u, _ := url.Parse(out.URL)
	return u
}

func TestSubdomainModeAuthAndProxy(t *testing.T) {
	r := newRig(t, "preview.test")
	// No grant: denied, with a page that does not reveal anything about the upstream.
	if res, body := r.get(t, "/hello", map[string]string{"Host": r.host()}); res.StatusCode != 403 || strings.Contains(body, "path") {
		t.Fatalf("no cookie: %d %s", res.StatusCode, body)
	}

	u := r.ownerTokenURL(t)
	if u.Host != r.host()+":8080" || u.Query().Get("nx_token") == "" {
		t.Fatalf("owner URL: %s", u)
	}
	tok := u.Query().Get("nx_token")

	// Wrong port / tampered / garbage tokens are refused.
	for name, bad := range map[string]string{"tampered": tok[:len(tok)-3] + "AAA", "garbage": "xx.yy", "empty-sig": strings.Split(tok, ".")[0] + "."} {
		if res, _ := r.get(t, "/?nx_token="+url.QueryEscape(bad), map[string]string{"Host": r.host()}); res.StatusCode != 403 {
			t.Fatalf("%s token accepted: %d", name, res.StatusCode)
		}
	}
	if res, _ := r.get(t, "/?nx_token="+url.QueryEscape(tok), map[string]string{"Host": "p1-ws-abc123." + r.domain}); res.StatusCode != 404 && res.StatusCode != 403 && res.StatusCode != 502 {
		t.Fatalf("token for another port accepted: %d", res.StatusCode)
	}

	// Valid token: cookie issued, token removed from the URL.
	res, _ := r.get(t, "/hello?x=1&nx_token="+url.QueryEscape(tok), map[string]string{"Host": r.host()})
	if res.StatusCode != 302 || res.Header.Get("Location") != "/hello?x=1" {
		t.Fatalf("bootstrap: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	var ck *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == preview.CookieName {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.Domain != "" {
		t.Fatalf("cookie must be host-only and HttpOnly: %+v", ck)
	}

	// Proxied with the cookie: Host is rewritten, Nexmesh cookies are stripped, the app's own survive.
	res, body := r.get(t, "/hello", map[string]string{"Host": r.host(), "Origin": "http://" + r.host(), "Cookie": "nexmesh_session=SECRET; theme=dark"}, ck)
	if res.StatusCode != 200 {
		t.Fatalf("proxied: %d %s", res.StatusCode, body)
	}
	var seen map[string]string
	_ = json.Unmarshal([]byte(body), &seen)
	if seen["host"] != "localhost:"+strconv.Itoa(r.port) || strings.Contains(seen["cookie"], "SECRET") || strings.Contains(seen["cookie"], "nx_pv") || !strings.Contains(seen["cookie"], "theme=dark") ||
		seen["origin"] != "http://localhost:"+strconv.Itoa(r.port) || seen["xfh"] != r.host() {
		t.Fatalf("upstream saw: %v", seen)
	}
	if res.Header.Get("Content-Security-Policy") != "" {
		t.Fatal("subdomain mode must not sandbox the app")
	}
	// Redirects pass through untouched in subdomain mode.
	if res, _ := r.get(t, "/redir", map[string]string{"Host": r.host()}, ck); res.StatusCode != 302 || res.Header.Get("Location") != "/hello" {
		t.Fatalf("redirect: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	// The cookie is only good for its own port.
	if res, _ := r.get(t, "/hello", map[string]string{"Host": "p1-ws-abc123." + r.domain}, ck); res.StatusCode != 403 {
		t.Fatalf("cookie reused on another port: %d", res.StatusCode)
	}
	// Non-preview hosts still reach the API.
	if res, _ := r.get(t, "/healthz", map[string]string{"Host": "localhost"}); res.StatusCode != 200 && res.StatusCode != 404 {
		t.Fatalf("api host: %d", res.StatusCode)
	}

	// WebSockets (hot reload) pass through the tunnel.
	wsURL := "ws" + strings.TrimPrefix(r.ts.URL, "http") + "/ws"
	c, _, err := websocket.Dial(r.ctx, wsURL, &websocket.DialOptions{Host: r.host(), HTTPHeader: http.Header{"Cookie": {ck.String()}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_ = c.Write(r.ctx, websocket.MessageText, []byte("hmr"))
	_, b, err := c.Read(r.ctx)
	if err != nil || string(b) != "echo:hmr" {
		t.Fatalf("websocket: %q %v", b, err)
	}
	// A websocket without the cookie is refused.
	if _, resp, err := websocket.Dial(r.ctx, wsURL, &websocket.DialOptions{Host: r.host()}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("unauthenticated websocket: %v %v", err, resp)
	}
}

func TestPathModeIsSandboxed(t *testing.T) {
	r := newRig(t, "")
	u := r.ownerTokenURL(t)
	if u.Path != "/api/preview/ws_abc123/"+strconv.Itoa(r.port)+"/" {
		t.Fatalf("path URL: %s", u)
	}
	res, body := r.get(t, u.Path+"hello", nil)
	if res.StatusCode != 200 || !strings.Contains(body, `"path":"/hello"`) {
		t.Fatalf("path mode: %d %s", res.StatusCode, body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || strings.Contains(csp, "allow-same-origin") {
		t.Fatalf("path mode must sandbox without allow-same-origin: %q", csp)
	}
	// Absolute redirects stay inside the preview prefix.
	res, _ = r.get(t, u.Path+"redir", nil)
	if res.Header.Get("Location") != strings.TrimSuffix(u.Path, "/")+"/hello" {
		t.Fatalf("location: %q", res.Header.Get("Location"))
	}
}

func TestClosedPortShowsFriendlyPage(t *testing.T) {
	r := newRig(t, "")
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().(*net.TCPAddr).Port
	l.Close()
	res, body := r.get(t, "/api/preview/ws_abc123/"+strconv.Itoa(closed)+"/", nil)
	if res.StatusCode != 502 || !strings.Contains(body, "Nothing is answering on port") {
		t.Fatalf("closed port: %d %s", res.StatusCode, body)
	}
	if res, _ := r.get(t, "/api/preview/ws_abc123/99999/", nil); res.StatusCode != 422 {
		t.Fatalf("bad port: %d", res.StatusCode)
	}
	if res, _ := r.get(t, "/api/preview/ws_other/1234/", nil); res.StatusCode != 404 {
		t.Fatalf("unknown workspace: %d", res.StatusCode)
	}
}

func TestPortsAPIMergesDeclaredAndDetected(t *testing.T) {
	r := newRig(t, "")
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, r.ts.URL+path, strings.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if c, b := do("PUT", "/api/workspaces/ws_abc123/ports/5173", `{"label":"web"}`); c != 204 {
		t.Fatalf("declare: %d %s", c, b)
	}
	if c, _ := do("PUT", "/api/workspaces/ws_abc123/ports/70000", `{"label":"x"}`); c != 422 {
		t.Fatalf("bad port: %d", c)
	}
	c, b := do("GET", "/api/workspaces/ws_abc123/ports", "")
	var out struct {
		Ports []struct {
			Port               int
			Label              string
			Declared, Detected bool
			Local              bool
		}
		Online      bool
		PreviewMode string
	}
	_ = json.Unmarshal([]byte(b), &out)
	var declared, detected bool
	for _, p := range out.Ports {
		if p.Port == 5173 && p.Declared && !p.Detected && p.Label == "web" {
			declared = true
		}
		if p.Port == r.port && p.Detected && p.Local && !p.Declared {
			detected = true
		}
	}
	if c != 200 || !declared || !detected || !out.Online || out.PreviewMode != "path" {
		t.Fatalf("ports: %d %s", c, b)
	}
	if c, _ := do("DELETE", "/api/workspaces/ws_abc123/ports/5173", ""); c != 204 {
		t.Fatalf("undeclare: %d", c)
	}
	if _, b = do("GET", "/api/workspaces/ws_abc123/ports", ""); strings.Contains(b, `"label":"web"`) {
		t.Fatalf("still declared: %s", b)
	}
}
