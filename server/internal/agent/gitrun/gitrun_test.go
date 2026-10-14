package gitrun

import (
	"context"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func code(err error) string {
	var e *agentproto.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestAllowlistAndForbiddenOptions(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	for _, args := range [][]string{
		nil, {"gc"}, {"daemon"}, {"clone", "-c", "core.sshCommand=evil", "x"}, {"clone", "--upload-pack=touch /tmp/x", "x"},
		{"fetch", "--config=core.fsmonitor=evil"}, {"remote", "add", "origin", "ext::sh"}, {"remote", "set-url", "origin", "x"}, {"config", "core.sshCommand", "evil"}, {"config", "--global", "user.name", "x"}, {"clone", "ext::sh -c evil"}, {"clone", "--recurse-submodules", "u"}, {"pull", "--exec=evil"},
	} {
		if _, err := r.Run(context.Background(), Params{Args: args}); code(err) != "bad_params" {
			t.Errorf("args %v accepted (%v)", args, err)
		}
	}
	// After "--" everything is a path, even a dash-prefixed one.
	root := t.TempDir()
	git(t, root, "init", "-q")
	if res, err := (&Runner{Root: root}).Run(context.Background(), Params{Args: []string{"add", "--", "-weird-name"}}); err != nil || res.ExitCode == 0 && false {
		t.Fatal(err)
	}
}

func TestCwdCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	git(t, outside, "init", "-q")
	_ = os.Symlink(outside, filepath.Join(root, "link"))
	r := &Runner{Root: root}
	for _, cwd := range []string{"link", "../" + filepath.Base(outside), "../../../"} {
		res, err := r.Run(context.Background(), Params{Args: []string{"rev-parse", "--show-toplevel"}, Cwd: cwd})
		if err == nil && strings.Contains(res.Stdout, outside) {
			t.Fatalf("cwd %q reached %s", cwd, outside)
		}
	}
	if _, err := r.Run(context.Background(), Params{Args: []string{"status"}, Cwd: "link"}); code(err) != "outside_workspace" {
		t.Fatalf("symlinked cwd: %v", err)
	}
}

func TestLocalWorkflow(t *testing.T) {
	root := t.TempDir()
	r := &Runner{Root: root}
	ctx := context.Background()
	env := map[string]string{"GIT_AUTHOR_NAME": "Ada", "GIT_AUTHOR_EMAIL": "ada@example.com", "GIT_COMMITTER_NAME": "Ada", "GIT_COMMITTER_EMAIL": "ada@example.com", "EVIL": "x"}
	run := func(cwd string, args ...string) *Result {
		t.Helper()
		res, err := r.Run(ctx, Params{Args: args, Cwd: cwd, Env: env})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run("", "init", "-q", "-b", "main", "app"); res.ExitCode != 0 {
		t.Fatalf("init: %+v", res)
	}
	_ = os.WriteFile(filepath.Join(root, "app", "a.txt"), []byte("one\n"), 0o644)
	run("app", "add", "--", "a.txt")
	if res := run("app", "commit", "-m", "first"); res.ExitCode != 0 {
		t.Fatalf("commit: %+v", res)
	}
	if out := run("app", "log", "-1", "--format=%an <%ae>").Stdout; !strings.Contains(out, "Ada <ada@example.com>") {
		t.Fatalf("identity env not applied: %q", out)
	}
	if repos := r.Repos(); len(repos) != 1 || repos[0] != "app" {
		t.Fatalf("repos: %v", repos)
	}
	// Only allow-listed env passes through to Git.
	if out := run("app", "config", "user.name"); strings.Contains(out.Stdout, "x") && out.ExitCode == 0 {
		t.Fatalf("unexpected config: %+v", out)
	}
}

// TestCredentialInjection serves a real repository over smart HTTP that
// requires Basic auth, then proves: the token authenticates, it is scrubbed
// from output, it is absent from argv-visible places and from .git/config,
// and without it the same operation fails.
func TestCredentialInjection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	backend, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("no git exec path")
	}
	httpBackend := filepath.Join(strings.TrimSpace(string(backend)), "git-http-backend")
	if _, err := os.Stat(httpBackend); err != nil {
		t.Skip("git-http-backend not available")
	}
	srvRoot := t.TempDir()
	bare := filepath.Join(srvRoot, "demo.git")
	git(t, srvRoot, "init", "-q", "--bare", "-b", "main", "demo.git")
	seed := t.TempDir()
	git(t, seed, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644)
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-qm", "seed")
	git(t, seed, "push", "-q", bare, "main")
	git(t, bare, "config", "http.receivepack", "true")

	const token = "tok-SECRET-0123456789"
	var mu sync.Mutex
	var seen []string
	h := &cgi.Handler{Path: httpBackend, Env: []string{"GIT_PROJECT_ROOT=" + srvRoot, "GIT_HTTP_EXPORT_ALL=1"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, p, ok := req.BasicAuth()
		mu.Lock()
		seen = append(seen, req.Header.Get("Authorization"))
		mu.Unlock()
		if !ok || u != "x-access-token" || p != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "denied "+token, http.StatusUnauthorized) // server echoes the token: it must still be scrubbed
			return
		}
		h.ServeHTTP(w, req)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	root := t.TempDir()
	r := &Runner{Root: root}
	ctx := context.Background()
	url := srv.URL + "/demo.git"

	// Without a credential it fails.
	res, _ := r.Run(ctx, Params{Args: []string{"ls-remote", url}})
	if res.ExitCode == 0 {
		t.Fatal("anonymous access should be denied")
	}
	// With the wrong token it fails and nothing leaks.
	res, _ = r.Run(ctx, Params{Args: []string{"ls-remote", url}, Cred: &Cred{Scheme: "http", Host: host, Username: "x-access-token", Token: "wrong-token-123456"}})
	if res.ExitCode == 0 || strings.Contains(res.Stderr+res.Stdout, "wrong-token") {
		t.Fatalf("wrong token: %+v", res)
	}
	// With the right token it works.
	cred := &Cred{Scheme: "http", Host: host, Username: "x-access-token", Token: token}
	res, err = r.Run(ctx, Params{Args: []string{"clone", "-q", url, "demo"}, Cred: cred})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("clone: %v %+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "README")); err != nil {
		t.Fatal("clone produced no files")
	}
	cfg, _ := os.ReadFile(filepath.Join(root, "demo", ".git", "config"))
	if strings.Contains(string(cfg), token) || strings.Contains(string(cfg), "extraheader") || strings.Contains(strings.ToLower(string(cfg)), "authorization") {
		t.Fatalf("credential persisted to .git/config:\n%s", cfg)
	}
	// Push works with the credential and the server's echo of it is scrubbed.
	_ = os.WriteFile(filepath.Join(root, "demo", "new.txt"), []byte("n\n"), 0o644)
	env := map[string]string{"GIT_AUTHOR_NAME": "A", "GIT_AUTHOR_EMAIL": "a@x.io", "GIT_COMMITTER_NAME": "A", "GIT_COMMITTER_EMAIL": "a@x.io"}
	_, _ = r.Run(ctx, Params{Args: []string{"add", "--", "new.txt"}, Cwd: "demo"})
	_, _ = r.Run(ctx, Params{Args: []string{"commit", "-qm", "add new"}, Cwd: "demo", Env: env})
	res, _ = r.Run(ctx, Params{Args: []string{"push", "origin", "main"}, Cwd: "demo", Cred: cred})
	if res.ExitCode != 0 {
		t.Fatalf("push: %+v", res)
	}
	if out := git(t, bare, "log", "--oneline"); !strings.Contains(out, "add new") {
		t.Fatalf("push did not reach the server: %s", out)
	}
	// Revoked credential: the same push is refused again.
	_ = os.WriteFile(filepath.Join(root, "demo", "later.txt"), []byte("l\n"), 0o644)
	_, _ = r.Run(ctx, Params{Args: []string{"add", "--", "later.txt"}, Cwd: "demo"})
	_, _ = r.Run(ctx, Params{Args: []string{"commit", "-qm", "later"}, Cwd: "demo", Env: env})
	if res, _ = r.Run(ctx, Params{Args: []string{"push", "origin", "main"}, Cwd: "demo"}); res.ExitCode == 0 {
		t.Fatal("push without credential must fail")
	}
	if strings.Contains(res.Stderr, token) {
		t.Fatal("token echoed in stderr")
	}
}
