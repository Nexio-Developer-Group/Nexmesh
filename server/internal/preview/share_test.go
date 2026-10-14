package preview_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/preview"
)

type shareResp struct {
	Link struct {
		ID     string
		Active bool
	}
	URL string
}

func (r *rig) api(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, r.ts.URL+path, strings.NewReader(body))
	req.Host = "localhost:8080"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (r *rig) createShare(t *testing.T, ttlMin int) (shareResp, *url.URL) {
	t.Helper()
	c, b := r.api(t, "POST", "/api/workspaces/ws_abc123/shares", `{"port":`+strconv.Itoa(r.port)+`,"ttlMinutes":`+strconv.Itoa(ttlMin)+`,"label":"demo"}`)
	if c != 201 {
		t.Fatalf("create share: %d %s", c, b)
	}
	var out shareResp
	_ = json.Unmarshal([]byte(b), &out)
	u, _ := url.Parse(out.URL)
	return out, u
}

func cookieNamed(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSharingIsOffByDefault(t *testing.T) {
	r := newRig(t, "")
	if c, b := r.api(t, "POST", "/api/workspaces/ws_abc123/shares", `{"port":3000,"ttlMinutes":60}`); c != 403 || !strings.Contains(b, "sharing_disabled") {
		t.Fatalf("create while disabled: %d %s", c, b)
	}
	if c, b := r.api(t, "GET", "/api/workspaces/ws_abc123/shares", ""); c != 200 || !strings.Contains(b, `"enabled":false`) {
		t.Fatalf("list: %d %s", c, b)
	}
	if res, _ := r.get(t, "/share/ws_abc123/3000/?nx_share=whatever.secret", nil); res.StatusCode != 403 {
		t.Fatalf("public route while disabled: %d", res.StatusCode)
	}
}

func TestPathModeShareLinkLifecycle(t *testing.T) {
	r := newRig(t, "")
	r.pv.EnableSharing(true, r.aud)
	now := time.Now()
	r.pv.SetClock(func() time.Time { return now })

	if c, _ := r.api(t, "POST", "/api/workspaces/ws_abc123/shares", `{"port":3000,"ttlMinutes":1}`); c != 422 {
		t.Fatalf("ttl below minimum: %d", c)
	}
	if c, _ := r.api(t, "POST", "/api/workspaces/ws_abc123/shares", `{"port":3000,"ttlMinutes":99999}`); c != 422 {
		t.Fatalf("ttl above maximum: %d", c)
	}
	link, u := r.createShare(t, 60)
	if !strings.HasPrefix(u.Path, "/share/ws_abc123/"+strconv.Itoa(r.port)+"/") || u.Query().Get("nx_share") == "" || !link.Link.Active {
		t.Fatalf("share URL: %s %+v", link.URL, link)
	}
	secret := u.Query().Get("nx_share")

	// The listing never reveals the secret.
	if c, b := r.api(t, "GET", "/api/workspaces/ws_abc123/shares", ""); c != 200 || strings.Contains(b, secret) || strings.Contains(b, "secretSha") || !strings.Contains(b, `"label":"demo"`) {
		t.Fatalf("list: %d %s", c, b)
	}

	// Anonymous visitor (no Nexmesh session): wrong secrets fail, the real one sets a path-scoped cookie.
	for name, q := range map[string]string{"none": "", "wrong": "?nx_share=" + link.Link.ID + ".AAAA", "other-id": "?nx_share=shr_nope.AAAA"} {
		if res, _ := r.get(t, u.Path+q, nil); res.StatusCode != 403 {
			t.Fatalf("%s: %d", name, res.StatusCode)
		}
	}
	res, _ := r.get(t, u.Path+"hello?nx_share="+url.QueryEscape(secret), nil)
	ck := cookieNamed(res, preview.CookieName)
	if res.StatusCode != 302 || ck == nil || !ck.HttpOnly || ck.Path != strings.TrimSuffix(u.Path, "/") || res.Header.Get("Location") != u.Path+"hello" {
		t.Fatalf("bootstrap: %d %+v loc=%q", res.StatusCode, ck, res.Header.Get("Location"))
	}
	res, body := r.get(t, u.Path+"hello", nil, ck)
	if res.StatusCode != 200 || !strings.Contains(body, `"path":"/hello"`) || !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("shared content: %d %s", res.StatusCode, body)
	}
	// The grant does not open other ports, and does not reach the authenticated owner API.
	if res, _ = r.get(t, "/share/ws_abc123/1/hello", nil, ck); res.StatusCode != 403 {
		t.Fatalf("other port: %d", res.StatusCode)
	}

	// Revocation takes effect on the very next request.
	if c, _ := r.api(t, "DELETE", "/api/workspaces/ws_abc123/shares/"+link.Link.ID, ""); c != 204 {
		t.Fatalf("revoke: %d", c)
	}
	if res, _ = r.get(t, u.Path+"hello", nil, ck); res.StatusCode != 403 {
		t.Fatalf("revoked link still works: %d", res.StatusCode)
	}
	if res, _ = r.get(t, u.Path+"?nx_share="+url.QueryEscape(secret), nil); res.StatusCode != 403 {
		t.Fatalf("revoked secret re-bootstraps: %d", res.StatusCode)
	}
	if c, _ := r.api(t, "DELETE", "/api/workspaces/ws_abc123/shares/"+link.Link.ID, ""); c != 204 {
		t.Fatalf("revoke is idempotent: %d", c)
	}
	if c, _ := r.api(t, "DELETE", "/api/workspaces/ws_abc123/shares/shr_missing", ""); c != 404 {
		t.Fatalf("unknown link: %d", c)
	}

	// Audit has creation and revocation, never the secret.
	evs, _ := r.st.ListAudit(context.Background(), "ws_abc123", 50)
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Action] = true
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), strings.Split(secret, ".")[1]) {
			t.Fatalf("secret in audit: %s", raw)
		}
	}
	if !seen["share.created"] || !seen["share.revoked"] {
		t.Fatalf("audit: %v", seen)
	}
}

func TestShareLinksExpire(t *testing.T) {
	r := newRig(t, "")
	r.pv.EnableSharing(true, r.aud)
	now := time.Now()
	r.pv.SetClock(func() time.Time { return now })
	_, u := r.createShare(t, 10)
	secret := u.Query().Get("nx_share")
	res, _ := r.get(t, u.Path+"hello?nx_share="+url.QueryEscape(secret), nil)
	ck := cookieNamed(res, preview.CookieName)
	if ck == nil {
		t.Fatal("no cookie")
	}
	if ck.Expires.After(now.Add(11 * time.Minute)) {
		t.Fatalf("cookie outlives the link: %v", ck.Expires)
	}
	if res, _ = r.get(t, u.Path+"hello", nil, ck); res.StatusCode != 200 {
		t.Fatalf("before expiry: %d", res.StatusCode)
	}
	now = now.Add(11 * time.Minute)
	if res, _ = r.get(t, u.Path+"hello", nil, ck); res.StatusCode != 403 {
		t.Fatalf("after expiry: %d", res.StatusCode)
	}
	if res, _ = r.get(t, u.Path+"?nx_share="+url.QueryEscape(secret), nil); res.StatusCode != 403 {
		t.Fatalf("expired secret: %d", res.StatusCode)
	}
	// Turning sharing off kills links that are still within their lifetime.
	now = now.Add(-11 * time.Minute)
	if res, _ = r.get(t, u.Path+"hello", nil, ck); res.StatusCode != 200 {
		t.Fatalf("clock rewind: %d", res.StatusCode)
	}
	r.pv.EnableSharing(false, r.aud)
	if res, _ = r.get(t, u.Path+"hello", nil, ck); res.StatusCode != 403 {
		t.Fatalf("sharing disabled: %d", res.StatusCode)
	}
}

func TestSubdomainShareLink(t *testing.T) {
	r := newRig(t, "preview.test")
	r.pv.EnableSharing(true, r.aud)
	link, u := r.createShare(t, 60)
	if u.Host != r.host()+":8080" || u.Query().Get("nx_share") == "" {
		t.Fatalf("subdomain share URL: %s", link.URL)
	}
	res, _ := r.get(t, "/?nx_share="+url.QueryEscape(u.Query().Get("nx_share")), map[string]string{"Host": r.host()})
	ck := cookieNamed(res, preview.CookieName)
	if res.StatusCode != 302 || ck == nil || ck.Domain != "" {
		t.Fatalf("bootstrap: %d %+v", res.StatusCode, ck)
	}
	if res, body := r.get(t, "/hello", map[string]string{"Host": r.host()}, ck); res.StatusCode != 200 || !strings.Contains(body, `"path":"/hello"`) {
		t.Fatalf("shared subdomain: %d %s", res.StatusCode, body)
	}
	_, _ = r.api(t, "DELETE", "/api/workspaces/ws_abc123/shares/"+link.Link.ID, "")
	if res, _ := r.get(t, "/hello", map[string]string{"Host": r.host()}, ck); res.StatusCode != 403 {
		t.Fatalf("revoked subdomain share: %d", res.StatusCode)
	}
}
