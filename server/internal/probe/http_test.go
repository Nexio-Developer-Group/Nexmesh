package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
)

func apiParams(t *testing.T, base string, extra map[string]any) Params {
	f := map[string]any{"baseUrl": base, "authType": "none"}
	for k, v := range extra {
		f[k] = v
	}
	return Params{Def: loadDef(t, "externalapi"), Fields: f, Guard: netguard.New(netguard.Policy{AllowLoopback: true}), Timeout: 8 * time.Second}
}

func failedStage(t *testing.T, base string, extra map[string]any) (string, string) {
	t.Helper()
	res := Run(context.Background(), apiParams(t, base, extra), nil)
	if res.Passed {
		return "", ""
	}
	return res.FailedStage, res.Message
}

func TestExternalAPIAuthTypesAndStatuses(t *testing.T) {
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Clone())
		if r.Method != http.MethodGet {
			t.Errorf("a probe must only read, got %s", r.Method)
		}
		switch {
		case r.URL.Path == "/v1/health" && r.Header.Get("X-API-Key") == "good-key":
			w.Header().Set("Server", "fake-api/1")
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/v1/health" && r.Header.Get("Authorization") == "Bearer good-token":
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/v1/health":
			if u, p, ok := r.BasicAuth(); ok && u == "svc" && p == "good-pass" {
				_, _ = w.Write([]byte("ok"))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v1/admin":
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path == "/v1/moved":
			http.Redirect(w, r, "/v1/health", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	base := srv.URL + "/v1"

	for name, tc := range map[string]map[string]any{
		"api key": {"authType": "apiKey", "apiKey": "good-key", "healthPath": "/health"},
		"bearer":  {"authType": "bearer", "token": "good-token", "healthPath": "/health"},
		"basic":   {"authType": "basic", "username": "svc", "password": "good-pass", "healthPath": "/health"},
	} {
		res := Run(context.Background(), apiParams(t, base, tc), nil)
		if !res.Passed {
			t.Fatalf("%s: %s %s", name, res.FailedStage, res.Message)
		}
		if len(res.Stages) != 7 || res.Stages[1].Status != "passed" {
			t.Fatalf("%s: stages %+v", name, res.Stages)
		}
	}
	if h := seen.Load().(http.Header); h.Get("User-Agent") == "" {
		t.Error("the probe should identify itself")
	}

	for name, c := range map[string]struct {
		extra map[string]any
		stage string
		text  string
	}{
		"wrong key":    {map[string]any{"authType": "apiKey", "apiKey": "nope", "healthPath": "/health"}, StageAuth, "rejected the credentials"},
		"needs auth":   {map[string]any{"healthPath": "/health"}, StageAuth, "needs credentials"},
		"forbidden":    {map[string]any{"authType": "bearer", "token": "x", "healthPath": "/admin"}, StageAuthz, "403"},
		"missing path": {map[string]any{"healthPath": "/nothing"}, StageServer, "404"},
		"redirect":     {map[string]any{"healthPath": "/moved"}, StageServer, "Redirects are not followed"},
		"wrong expect": {map[string]any{"authType": "bearer", "token": "good-token", "healthPath": "/health", "expectStatus": 204}, StageServer, "204 was expected"},
	} {
		stage, msg := failedStage(t, base, c.extra)
		if stage != c.stage || !strings.Contains(msg, c.text) {
			t.Errorf("%s: failed at %q with %q, want %q containing %q", name, stage, msg, c.stage, c.text)
		}
	}
	// An expected status other than 200 is honoured.
	res := Run(context.Background(), apiParams(t, base, map[string]any{"healthPath": "/nothing", "expectStatus": 404}), nil)
	if !res.Passed {
		t.Fatalf("expected 404 should pass: %s", res.Message)
	}
}

func TestExternalAPIValidation(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"ok", map[string]any{"baseUrl": "https://api.example.com/v1"}, ""},
		{"no scheme", map[string]any{"baseUrl": "api.example.com"}, "http:// or https://"},
		{"ftp", map[string]any{"baseUrl": "ftp://x"}, "http:// or https://"},
		{"credentials in url", map[string]any{"baseUrl": "https://u:p@api.example.com"}, "authentication fields"},
		{"query", map[string]any{"baseUrl": "https://a.example/?key=1"}, "query string"},
		{"api key missing", map[string]any{"baseUrl": "https://a.example", "authType": "apiKey"}, "API key is required"},
		{"bearer missing", map[string]any{"baseUrl": "https://a.example", "authType": "bearer"}, "Bearer token is required"},
		{"basic missing", map[string]any{"baseUrl": "https://a.example", "authType": "basic", "username": "u"}, "Username and password"},
		{"unknown auth", map[string]any{"baseUrl": "https://a.example", "authType": "magic"}, "must be one of"},
		{"bad header name", map[string]any{"baseUrl": "https://a.example", "authType": "apiKey", "apiKey": "k", "apiKeyHeader": "X Key"}, "valid header name"},
		{"authorization header", map[string]any{"baseUrl": "https://a.example", "headers": "Authorization: Bearer x"}, "cannot be set here"},
		{"header syntax", map[string]any{"baseUrl": "https://a.example", "headers": "no colon here"}, "Name: value"},
		{"header injection", map[string]any{"baseUrl": "https://a.example", "headers": "X-A: b\x00c"}, "not valid"},
		{"bad path", map[string]any{"baseUrl": "https://a.example", "healthPath": "health"}, "must start with /"},
		{"bad status", map[string]any{"baseUrl": "https://a.example", "expectStatus": 99}, "between 100 and 599"},
	}
	for _, c := range cases {
		f := map[string]any{"authType": "none"}
		for k, v := range c.fields {
			f[k] = v
		}
		_, e := validate(Params{Def: loadDef(t, "externalapi"), Fields: f})
		switch {
		case c.want == "" && e != nil:
			t.Errorf("%s: unexpected error %v", c.name, e)
		case c.want != "" && (e == nil || !strings.Contains(e.Msg, c.want)):
			t.Errorf("%s: got %v, want %q", c.name, e, c.want)
		}
	}
}

func TestExternalAPIUsesTheEgressGuardAndNeverFollowsRedirects(t *testing.T) {
	// Loopback is refused by default, like every other probe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	p := apiParams(t, srv.URL, nil)
	p.Guard = netguard.New(netguard.Policy{})
	res := Run(context.Background(), p, nil)
	if res.Passed || res.FailedStage != StageNetwork || !strings.Contains(res.Message, "not allowed") {
		t.Fatalf("loopback must be blocked: %+v", res)
	}
	// The cloud metadata address is blocked even when private ranges are allowed.
	p = apiParams(t, "http://169.254.169.254/latest", nil)
	p.Guard = netguard.New(netguard.Policy{AllowPrivate: true, AllowLoopback: true})
	if res := Run(context.Background(), p, nil); res.Passed || !strings.Contains(res.Message, "not allowed") {
		t.Fatalf("metadata address must be blocked: %+v", res)
	}
	// A redirect to the metadata service is returned as a status, never followed.
	var hits atomic.Int32
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer redir.Close()
	res = Run(context.Background(), apiParams(t, redir.URL, nil), nil)
	if res.Passed || !strings.Contains(res.Message, "redirect") || hits.Load() != 1 {
		t.Fatalf("redirect handling: %+v (hits %d)", res, hits.Load())
	}
}

func TestExternalAPISecretsNeverAppearInResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, _ = strconv.Atoi(u.Port())
	secret := "sk_live_VERYSECRETKEY"
	p := apiParams(t, srv.URL, map[string]any{"authType": "apiKey", "apiKey": secret})
	p.Scrub = func(s string) string { return strings.ReplaceAll(s, secret, "••••") }
	res := Run(context.Background(), p, nil)
	for _, st := range res.Stages {
		if strings.Contains(st.Message, secret) {
			t.Fatalf("secret in stage message: %+v", st)
		}
	}
	if strings.Contains(res.Message, secret) {
		t.Fatal("secret in result message")
	}
}
