package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/auth"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func authServer(t *testing.T, signup bool) *httptest.Server {
	t.Helper()
	s := httpapi.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.RegisterAuth(auth.NewService(store.NewMemory(), signup), true, "")
	s.Handle("GET /api/whoami", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, 200, map[string]string{"owner": httpapi.Owner(r)})
	})
	s.Handle("POST /api/thing", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, c *http.Client, method, url, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func jar(t *testing.T) *http.Client {
	t.Helper()
	j, _ := cookiejarNew()
	return &http.Client{Jar: j}
}

func TestAuthFlow(t *testing.T) {
	ts := authServer(t, false)
	c := jar(t)

	if res, _ := do(t, c, "GET", ts.URL+"/api/whoami", "", map[string]string{"X-Nexmesh-User": "evil"}); res.StatusCode != 401 {
		t.Fatalf("unauthenticated whoami = %d, want 401 (header must not grant access)", res.StatusCode)
	}
	if res, _ := do(t, c, "GET", ts.URL+"/healthz", "", nil); res.StatusCode != 200 {
		t.Fatal("healthz must stay public")
	}
	if _, body := do(t, c, "GET", ts.URL+"/api/auth/status", "", nil); !strings.Contains(body, `"signupOpen":true`) {
		t.Fatalf("first user may sign up: %s", body)
	}

	res, body := do(t, c, "POST", ts.URL+"/api/auth/register", `{"email":"Owner@Example.com","password":"short"}`, nil)
	if res.StatusCode != 422 || !strings.Contains(body, `"field":"password"`) {
		t.Fatalf("short password: %d %s", res.StatusCode, body)
	}
	res, body = do(t, c, "POST", ts.URL+"/api/auth/register", `{"email":"Owner@Example.com","password":"correct horse battery"}`, nil)
	if res.StatusCode != 200 || !strings.Contains(body, `"role":"owner"`) || strings.Contains(body, "argon2") {
		t.Fatalf("register: %d %s", res.StatusCode, body)
	}
	var cookie *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == "nexmesh_session" {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie flags wrong: %+v", cookie)
	}

	_, body = do(t, c, "GET", ts.URL+"/api/whoami", "", nil)
	if !strings.Contains(body, `"owner":"usr_`) {
		t.Fatalf("whoami with session: %s", body)
	}
	if _, body = do(t, c, "GET", ts.URL+"/api/auth/status", "", nil); !strings.Contains(body, `"signupOpen":false`) {
		t.Fatalf("signup must close after first user: %s", body)
	}
	if res, _ = do(t, jar(t), "POST", ts.URL+"/api/auth/register", `{"email":"b@example.com","password":"another long one"}`, nil); res.StatusCode != 403 {
		t.Fatalf("second signup = %d, want 403", res.StatusCode)
	}

	// CSRF: cross-site unsafe request is rejected even with a valid cookie.
	if res, _ = do(t, c, "POST", ts.URL+"/api/thing", "{}", map[string]string{"Origin": "https://evil.example"}); res.StatusCode != 403 {
		t.Fatalf("cross-origin POST = %d, want 403", res.StatusCode)
	}
	if res, _ = do(t, c, "POST", ts.URL+"/api/thing", "{}", map[string]string{"Origin": ts.URL}); res.StatusCode != 204 {
		t.Fatalf("same-origin POST = %d, want 204", res.StatusCode)
	}

	do(t, c, "POST", ts.URL+"/api/auth/logout", "", nil)
	if res, _ = do(t, c, "GET", ts.URL+"/api/whoami", "", nil); res.StatusCode != 401 {
		t.Fatalf("after logout = %d, want 401", res.StatusCode)
	}
	// The old cookie value is dead server-side too.
	if res, _ = do(t, jar(t), "GET", ts.URL+"/api/whoami", "", map[string]string{"Cookie": "nexmesh_session=" + cookie.Value}); res.StatusCode != 401 {
		t.Fatalf("revoked token = %d, want 401", res.StatusCode)
	}
}

func TestLoginAndRateLimit(t *testing.T) {
	ts := authServer(t, true)
	c := jar(t)
	do(t, c, "POST", ts.URL+"/api/auth/register", `{"email":"a@example.com","password":"correct horse battery"}`, nil)
	do(t, c, "POST", ts.URL+"/api/auth/logout", "", nil)

	if res, _ := do(t, c, "POST", ts.URL+"/api/auth/register", `{"email":"A@example.com","password":"correct horse battery"}`, nil); res.StatusCode != 409 {
		t.Fatalf("duplicate email = %d, want 409", res.StatusCode)
	}
	if res, _ := do(t, c, "POST", ts.URL+"/api/auth/login", `{"email":"a@example.com","password":"correct horse battery"}`, nil); res.StatusCode != 200 {
		t.Fatalf("login = %d", res.StatusCode)
	}
	bad := jar(t)
	var last int
	for i := 0; i < 9; i++ {
		res, _ := do(t, bad, "POST", ts.URL+"/api/auth/login", `{"email":"a@example.com","password":"nope-nope-nope"}`, nil)
		last = res.StatusCode
		if i < 8 && last != 401 {
			t.Fatalf("attempt %d = %d, want 401", i, last)
		}
	}
	if last != 429 {
		t.Fatalf("9th failure = %d, want 429", last)
	}
}

func TestPasswordHashing(t *testing.T) {
	h, _ := auth.HashPassword("s3cret-pass-phrase")
	if !auth.VerifyPassword("s3cret-pass-phrase", h) || auth.VerifyPassword("wrong", h) || auth.VerifyPassword("x", "garbage") {
		t.Fatal("hash/verify mismatch")
	}
	h2, _ := auth.HashPassword("s3cret-pass-phrase")
	if h == h2 {
		t.Fatal("salts must differ")
	}
}
