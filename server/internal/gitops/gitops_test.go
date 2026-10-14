package gitops_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent/gitrun"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitops"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// directAgent runs git.run/git.repos in-process against a temp root, recording the params it saw.
type directAgent struct {
	r    *gitrun.Runner
	mu   sync.Mutex
	seen []gitrun.Params
}

func (a *directAgent) Call(ctx context.Context, _ string, method string, params, out any) error {
	b, _ := json.Marshal(params)
	switch method {
	case "git.run":
		var p gitrun.Params
		_ = json.Unmarshal(b, &p)
		a.mu.Lock()
		a.seen = append(a.seen, p)
		a.mu.Unlock()
		res, err := a.r.Run(ctx, p)
		if err != nil {
			return err
		}
		rb, _ := json.Marshal(res)
		return json.Unmarshal(rb, out)
	case "git.repos":
		rb, _ := json.Marshal(map[string]any{"repos": a.r.Repos()})
		return json.Unmarshal(rb, out)
	}
	return errors.New("unexpected method " + method)
}

type rig struct {
	svc   *gitops.Service
	creds *gitcred.Service
	agent *directAgent
	root  string
	ctx   context.Context
	st    store.Store
}

func newRig(t *testing.T) *rig {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // no ambient git identity
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	ctx := context.Background()
	st := store.NewMemory()
	be, _ := secrets.NewStoreBackend(st, make([]byte, 32))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	aud := audit.New(st, log)
	creds := gitcred.NewService(st, secrets.New(be), aud)
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "n", Status: domain.WSReady})
	root := t.TempDir()
	ag := &directAgent{r: &gitrun.Runner{Root: root}}
	return &rig{svc: gitops.NewService(ag, creds, aud), creds: creds, agent: ag, root: root, ctx: ctx, st: st}
}

func (r *rig) sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Join(r.root, dir)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.io", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.io")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *rig) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(r.root, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gerr(err error) *gitops.Error {
	var ge *gitops.Error
	if errors.As(err, &ge) {
		return ge
	}
	return nil
}

func TestLocalCommitFlow(t *testing.T) {
	r := newRig(t)
	r.sh(t, "", "init", "-q", "-b", "main", "app")
	r.write(t, "app/a.txt", "one\n")
	r.write(t, "app/b.txt", "bee\n")

	st, err := r.svc.Status(r.ctx, "ws1", "app")
	if err != nil || st.Branch != "main" || len(st.Files) != 2 || !st.Files[0].Untracked {
		t.Fatalf("status: %v %+v", err, st)
	}
	if _, err := r.svc.Sync(r.ctx, "ws1", "u1", "app", "fetch"); gerr(err) == nil || gerr(err).Code != "no_remote" {
		t.Fatalf("fetch without origin: %v", err)
	}
	if err := r.svc.Stage(r.ctx, "ws1", "app", []string{"a.txt"}, false); err != nil {
		t.Fatal(err)
	}
	st, _ = r.svc.Status(r.ctx, "ws1", "app")
	staged := 0
	for _, f := range st.Files {
		if f.Staged {
			staged++
		}
	}
	if staged != 1 {
		t.Fatalf("staged = %d: %+v", staged, st.Files)
	}
	if err := r.svc.Commit(r.ctx, "ws1", "u1", "app", "  ", gitops.Author{}); gerr(err) == nil || gerr(err).Field != "message" {
		t.Fatalf("empty message: %v", err)
	}
	if err := r.svc.Commit(r.ctx, "ws1", "u1", "app", "add a", gitops.Author{Name: "Ada", Email: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	if out := r.sh(t, "app", "log", "-1", "--format=%an <%ae> %s"); !strings.Contains(out, "Ada <ada@example.com> add a") {
		t.Fatalf("author: %s", out)
	}
	if err := r.svc.Commit(r.ctx, "ws1", "u1", "app", "again", gitops.Author{Name: "Ada", Email: "ada@example.com"}); gerr(err) == nil || gerr(err).Code != "nothing_to_commit" {
		t.Fatalf("empty commit: %v", err)
	}

	// Diff of an unstaged change, then stage+unstage round trip.
	r.write(t, "app/a.txt", "one\ntwo\n")
	d, _, err := r.svc.Diff(r.ctx, "ws1", "app", "a.txt", false)
	if err != nil || !strings.Contains(d, "+two") {
		t.Fatalf("diff: %v %q", err, d)
	}
	_ = r.svc.Stage(r.ctx, "ws1", "app", []string{"a.txt"}, false)
	if d, _, _ = r.svc.Diff(r.ctx, "ws1", "app", "a.txt", true); !strings.Contains(d, "+two") {
		t.Fatalf("staged diff: %q", d)
	}
	_ = r.svc.Stage(r.ctx, "ws1", "app", []string{"a.txt"}, true)
	if d, _, _ = r.svc.Diff(r.ctx, "ws1", "app", "a.txt", true); d != "" {
		t.Fatalf("unstage failed: %q", d)
	}

	// Discard restores tracked files and removes untracked ones.
	if err := r.svc.Discard(r.ctx, "ws1", "u1", "app", []string{"a.txt", "b.txt"}); err != nil {
		t.Fatal(err)
	}
	if st, _ = r.svc.Status(r.ctx, "ws1", "app"); !st.Clean {
		t.Fatalf("not clean after discard: %+v", st.Files)
	}
	if b, _ := os.ReadFile(filepath.Join(r.root, "app/a.txt")); string(b) != "one\n" {
		t.Fatalf("a.txt = %q", b)
	}

	// Branches.
	if err := r.svc.Switch(r.ctx, "ws1", "u1", "app", "feature/login", true); err != nil {
		t.Fatal(err)
	}
	bs, _ := r.svc.Branches(r.ctx, "ws1", "app")
	cur := ""
	for _, b := range bs {
		if b.Current {
			cur = b.Name
		}
	}
	if cur != "feature/login" || len(bs) != 2 {
		t.Fatalf("branches: %+v", bs)
	}
	if err := r.svc.Switch(r.ctx, "ws1", "u1", "app", "--detach", false); gerr(err) == nil || gerr(err).Field != "branch" {
		t.Fatalf("option-like branch accepted: %v", err)
	}
	log, _ := r.svc.Log(r.ctx, "ws1", "app", 10)
	if len(log) != 1 || log[0].Subject != "add a" || log[0].Author != "Ada" {
		t.Fatalf("log: %+v", log)
	}
	if repos, _ := r.svc.Repos(r.ctx, "ws1"); len(repos) != 1 || repos[0] != "app" {
		t.Fatalf("repos: %v", repos)
	}
}

func TestCredentialIsAttachedOnlyForMatchingHost(t *testing.T) {
	r := newRig(t)
	r.sh(t, "", "init", "-q", "-b", "main", "app")
	r.sh(t, "app", "remote", "add", "origin", "https://127.0.0.1:1/acme/app.git") // unreachable: fails fast
	const token = "ghp_0123456789abcdefTOKEN"
	_, _ = r.creds.Add(r.ctx, "u1", "ws1", gitcred.AddInput{Provider: "custom", Host: "127.0.0.1:1", Username: "bot", Token: token})
	_, _ = r.creds.Add(r.ctx, "u1", "ws1", gitcred.AddInput{Provider: "github", Token: "ghp_OTHERTOKEN_0123456789"})

	_, err := r.svc.Sync(r.ctx, "ws1", "u1", "app", "fetch")
	if ge := gerr(err); ge == nil || ge.Code != "network" {
		t.Fatalf("unreachable remote: %v", err)
	}
	var fetch *gitrun.Params
	for i := range r.agent.seen {
		if len(r.agent.seen[i].Args) > 0 && r.agent.seen[i].Args[0] == "fetch" {
			fetch = &r.agent.seen[i]
		}
	}
	if fetch == nil || fetch.Cred == nil || fetch.Cred.Token != token || fetch.Cred.Host != "127.0.0.1:1" || fetch.Cred.Username != "bot" {
		t.Fatalf("matching credential not attached: %+v", fetch)
	}
	// The unrelated GitHub token is never sent to this host, and local ops carry no credential.
	for _, p := range r.agent.seen {
		if p.Cred != nil && strings.Contains(p.Cred.Token, "OTHERTOKEN") {
			t.Fatal("credential for another host was attached")
		}
		if p.Cred != nil && p.Args[0] != "fetch" {
			t.Fatalf("credential attached to a local command: %v", p.Args)
		}
	}
	// The token appears in no audit record or error.
	evs, _ := r.st.ListAudit(r.ctx, "ws1", 50)
	for _, e := range evs {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "TOKEN") {
			t.Fatalf("token in audit: %s", b)
		}
	}
	if strings.Contains(err.Error(), token) || strings.Contains(gerr(err).Detail, token) {
		t.Fatal("token in error")
	}
	// After revoke, the next call has no credential.
	l, _ := r.creds.List(r.ctx, "u1", "ws1")
	for _, c := range l {
		if c.Host == "127.0.0.1:1" {
			_ = r.creds.Revoke(r.ctx, "u1", "ws1", c.ID)
		}
	}
	r.agent.seen = nil
	_, _ = r.svc.Sync(r.ctx, "ws1", "u1", "app", "fetch")
	for _, p := range r.agent.seen {
		if p.Cred != nil {
			t.Fatal("revoked credential still attached")
		}
	}
}

func TestValidation(t *testing.T) {
	r := newRig(t)
	for _, u := range []string{"git@github.com:a/b.git", "http://github.com/a/b.git", "https://user:pw@github.com/a/b.git", "ext::sh -c evil", "file:///etc", "https://github.com/a/b.git?x=1", "https://exa mple.com/a"} {
		if _, err := r.svc.Clone(r.ctx, "ws1", "u1", u, "", ""); gerr(err) == nil || gerr(err).Field != "url" {
			t.Errorf("url %q accepted: %v", u, err)
		}
	}
	for _, d := range []string{"../x", "-rf", "a/b", ".hidden"} {
		if _, err := r.svc.Clone(r.ctx, "ws1", "u1", "https://example.com/a/b.git", d, ""); gerr(err) == nil || gerr(err).Field != "dir" {
			t.Errorf("dir %q accepted: %v", d, err)
		}
	}
	for _, repo := range []string{"../x", "-c", "a/../b"} {
		if _, err := r.svc.Status(r.ctx, "ws1", repo); gerr(err) == nil || gerr(err).Field != "repo" {
			t.Errorf("repo %q accepted: %v", repo, err)
		}
	}
	for _, b := range []string{"-x", "a..b", "a b", "x.lock", "a//b", "@{u}", "feature/"} {
		if gitops.ValidateBranch(b) == nil {
			t.Errorf("branch %q accepted", b)
		}
	}
	if gitops.ValidateBranch("feature/ABC-123_x.y") != nil {
		t.Error("good branch rejected")
	}
	if err := r.svc.Stage(r.ctx, "ws1", "", []string{"../../etc/passwd"}, false); gerr(err) == nil {
		t.Error("path traversal accepted")
	}
}

func TestParsersAndHelpers(t *testing.T) {
	st := gitops.ParseStatus("app", "## main...origin/main [ahead 2, behind 1]\x00 M src/a.go\x00A  new.go\x00R  b.go\x00old.go\x00?? t.txt\x00UU conflict.go\x00")
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 2 || st.Behind != 1 || len(st.Files) != 5 {
		t.Fatalf("%+v", st)
	}
	f := st.Files
	if !f[0].Unstaged || f[0].Staged || !f[1].Staged || f[2].OrigPath != "old.go" || !f[3].Untracked || !f[4].Conflicted {
		t.Fatalf("files: %+v", f)
	}
	if s := gitops.ParseStatus("", "## No commits yet on trunk\x00"); s.Branch != "trunk" || !s.Clean {
		t.Fatalf("%+v", s)
	}
	if s := gitops.ParseStatus("", "## HEAD (no branch)\x00"); !s.Detached {
		t.Fatalf("%+v", s)
	}
	if s := gitops.ParseStatus("", "## dev...origin/dev [behind 3]\x00"); s.Behind != 3 || s.Ahead != 0 {
		t.Fatalf("%+v", s)
	}
	for in, want := range map[string]string{
		"https://github.com/acme/app.git":      "https://github.com/acme/app/compare/feat%2Fx?expand=1",
		"https://gitlab.com/acme/app":          "https://gitlab.com/acme/app/-/merge_requests/new?merge_request%5Bsource_branch%5D=feat%2Fx",
		"https://bitbucket.org/acme/app.git":   "https://bitbucket.org/acme/app/pull-requests/new?source=feat%2Fx",
		"https://git.example.com/acme/app.git": "https://git.example.com/acme/app/compare/feat%2Fx",
	} {
		if got, err := gitops.PullRequestURL(in, "feat/x"); err != nil || got != want {
			t.Errorf("PR url %s = %q (%v), want %q", in, got, err, want)
		}
	}
	if _, err := gitops.PullRequestURL("git@github.com:a/b.git", "x"); err == nil {
		t.Error("ssh remote must not produce a PR link")
	}
}
