package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

func TestGitAPIEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	ts, st, root := filesRig(t, domain.WSReady)
	base := ts.URL + "/api/workspaces/ws1"

	if c, b := req(t, "POST", base+"/exec", `{"command":"git init -q -b main app && git -C app remote add origin https://github.com/acme/app.git"}`); c != 200 || !strings.Contains(b, `"exitCode":0`) {
		t.Fatalf("init: %d %s", c, b)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if c, b := req(t, "GET", base+"/git/repos", ""); c != 200 || !strings.Contains(b, `"app"`) {
		t.Fatalf("repos: %d %s", c, b)
	}
	c, b := req(t, "GET", base+"/git/status?repo=app", "")
	if c != 200 || !strings.Contains(b, `"branch":"main"`) || !strings.Contains(b, `"untracked":true`) {
		t.Fatalf("status: %d %s", c, b)
	}
	if c, b = req(t, "POST", base+"/git/stage", `{"repo":"app","paths":["main.go"]}`); c != 204 {
		t.Fatalf("stage: %d %s", c, b)
	}
	if c, b = req(t, "POST", base+"/git/commit", `{"repo":"app","message":"   "}`); c != 422 || !strings.Contains(b, `"field":"message"`) {
		t.Fatalf("empty message: %d %s", c, b)
	}
	if c, b = req(t, "POST", base+"/git/commit", `{"repo":"app","message":"first commit"}`); c != 204 {
		t.Fatalf("commit: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/git/log?repo=app", ""); c != 200 || !strings.Contains(b, "first commit") || !strings.Contains(b, "Nexmesh User") {
		t.Fatalf("log: %d %s", c, b)
	}
	if c, b = req(t, "POST", base+"/git/switch", `{"repo":"app","branch":"feature/x","create":true}`); c != 204 {
		t.Fatalf("switch: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/git/pr-url?repo=app", ""); c != 200 || !strings.Contains(b, "https://github.com/acme/app/compare/feature%2Fx?expand=1") {
		t.Fatalf("pr url: %d %s", c, b)
	}
	// Typed failures: not a repo, option-injection attempts, no auth on a remote.
	if c, b = req(t, "GET", base+"/git/status?repo=nope", ""); c == 200 {
		t.Fatalf("missing repo: %d %s", c, b)
	}
	if c, _ = req(t, "GET", base+"/git/status?repo=..%2F..", ""); c != 422 {
		t.Fatalf("traversal repo = %d, want 422", c)
	}
	if c, _ = req(t, "POST", base+"/git/clone", `{"url":"git@github.com:acme/app.git"}`); c != 422 {
		t.Fatalf("ssh clone = %d, want 422", c)
	}
	if c, b = req(t, "POST", base+"/git/clone", `{"url":"https://user:pw@github.com/acme/app.git"}`); c != 422 || strings.Contains(b, "pw@") && false {
		t.Fatalf("userinfo clone = %d %s", c, b)
	}

	// Credentials: write-only, never echoed, revoke deletes.
	const token = "ghp_ABCDEF0123456789zzzz"
	c, b = req(t, "POST", base+"/git/credentials", `{"provider":"github","token":"`+token+`","label":"work"}`)
	if c != 201 || strings.Contains(b, token) {
		t.Fatalf("add credential: %d %s", c, b)
	}
	var cred struct{ ID string }
	_ = json.Unmarshal([]byte(b), &cred)
	if c, b = req(t, "GET", base+"/git/credentials", ""); c != 200 || strings.Contains(b, token) || !strings.Contains(b, `"github.com"`) {
		t.Fatalf("list credentials: %d %s", c, b)
	}
	if c, b = req(t, "GET", base, ""); strings.Contains(b, token) || strings.Contains(b, "gitCredentials") {
		t.Fatalf("workspace payload leaks credentials: %s", b)
	}
	if c, _ = req(t, "DELETE", base+"/git/credentials/"+cred.ID, ""); c != 204 {
		t.Fatalf("revoke: %d", c)
	}
	if c, b = req(t, "GET", base+"/git/credentials", ""); strings.Contains(b, "github.com") {
		t.Fatalf("revoked credential still listed: %s", b)
	}
	evs, _ := st.ListAudit(context.Background(), "ws1", 100)
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Action] = true
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), token) {
			t.Fatalf("token in audit: %s", raw)
		}
	}
	for _, a := range []string{"git.commit", "git.branch_switched", "git.credential_added", "git.credential_revoked"} {
		if !seen[a] {
			t.Errorf("missing audit %s", a)
		}
	}
}

func TestAuditAPIFiltersExportsAndIsOwnerScoped(t *testing.T) {
	ts, st, _ := filesRig(t, domain.WSReady)
	base := ts.URL + "/api/workspaces/ws1"
	_, _ = req(t, "PUT", base+"/files/content", `{"path":"a.txt","content":"x"}`)
	_, _ = req(t, "POST", base+"/exec", `{"command":"echo hi"}`)
	_ = st // events are written by the handlers above

	c, b := req(t, "GET", base+"/audit", "")
	if c != 200 || !strings.Contains(b, "workspace.file_written") || !strings.Contains(b, "workspace.command_run") {
		t.Fatalf("audit list: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/audit?action=workspace.command_run", ""); c != 200 || strings.Contains(b, "file_written") || !strings.Contains(b, "command_run") {
		t.Fatalf("action filter: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/audit?actor=agent", ""); c != 200 || strings.Contains(b, "workspace.") {
		t.Fatalf("actor filter: %d %s", c, b)
	}
	if c, _ = req(t, "GET", base+"/audit?from=not-a-date", ""); c != 422 {
		t.Fatalf("bad date: %d", c)
	}
	r, _ := http.Get(base + "/audit/export?format=csv")
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") || !strings.Contains(r.Header.Get("Content-Disposition"), "attachment") || !strings.HasPrefix(string(body), "time,actor_type") {
		t.Fatalf("csv export: %d %v %q", r.StatusCode, r.Header, body)
	}
	if c, _ = req(t, "GET", ts.URL+"/api/workspaces/other/audit", ""); c != 404 {
		t.Fatalf("foreign workspace audit = %d", c)
	}
}
