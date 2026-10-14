package gitops

import (
	"context"
	"net/url"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
)

// Author identifies the user making commits made through the UI.
type Author struct{ Name, Email string }

func (a Author) env() map[string]string {
	if a.Name == "" || a.Email == "" {
		return nil
	}
	return map[string]string{"GIT_AUTHOR_NAME": a.Name, "GIT_AUTHOR_EMAIL": a.Email, "GIT_COMMITTER_NAME": a.Name, "GIT_COMMITTER_EMAIL": a.Email}
}

func (s *Service) Repos(ctx context.Context, ws string) ([]string, error) {
	var out struct {
		Repos []string `json:"repos"`
	}
	if err := s.agent.Call(ctx, ws, "git.repos", struct{}{}, &out); err != nil {
		return nil, err
	}
	if out.Repos == nil {
		out.Repos = []string{}
	}
	return out.Repos, nil
}

// credFor finds the credential for the host of a repository's origin.
func (s *Service) credFor(ctx context.Context, ws, repo string) (*gitcred.Secret, string, error) {
	res, err := s.git(ctx, ws, repo, nil, nil, "remote", "get-url", "origin")
	if err != nil {
		return nil, "", err
	}
	if res.ExitCode != 0 {
		return nil, "", &Error{Code: "no_remote", Message: "This repository has no remote named origin."}
	}
	remote := strings.TrimSpace(res.Stdout)
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return nil, remote, nil
	}
	c, err := s.creds.Resolve(ctx, ws, u.Host, "")
	return c, remote, err
}

func (s *Service) Clone(ctx context.Context, ws, actor, rawURL, dir, branch string) (string, error) {
	u, err := ParseRemoteURL(rawURL)
	if err != nil {
		return "", err
	}
	if dir == "" {
		dir = DefaultDir(u)
	}
	if !dirRE.MatchString(dir) || strings.Contains(dir, "..") {
		return "", bad("dir", "Use a simple folder name (letters, digits, - _ . and spaces).")
	}
	args := []string{"clone", "--progress"}
	if branch != "" {
		if err := ValidateBranch(branch); err != nil {
			return "", err
		}
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", u.String(), dir)
	cred, err := s.creds.Resolve(ctx, ws, u.Host, "")
	if err != nil {
		return "", err
	}
	if _, err := s.ok(ctx, ws, "", cred, nil, args...); err != nil {
		return "", err
	}
	s.aud.User(ctx, actor, ws, "git.clone", u.Host+u.Path, map[string]any{"dir": dir, "authenticated": cred != nil})
	return dir, nil
}

// CheckAccess verifies that the stored credential (or none) can read a repository.
func (s *Service) CheckAccess(ctx context.Context, ws, rawURL string) (authenticated bool, err error) {
	u, err := ParseRemoteURL(rawURL)
	if err != nil {
		return false, err
	}
	cred, err := s.creds.Resolve(ctx, ws, u.Host, "")
	if err != nil {
		return false, err
	}
	_, err = s.ok(ctx, ws, "", cred, nil, "ls-remote", "--heads", "--", u.String())
	return cred != nil, err
}

func (s *Service) Status(ctx context.Context, ws, repo string) (*Status, error) {
	repo, err := cleanRepo(repo)
	if err != nil {
		return nil, err
	}
	res, err := s.ok(ctx, ws, repo, nil, nil, "status", "--porcelain=v1", "-b", "-z", "-uall")
	if err != nil {
		return nil, err
	}
	return ParseStatus(repo, res.Stdout), nil
}

func (s *Service) Branches(ctx context.Context, ws, repo string) ([]Branch, error) {
	repo, err := cleanRepo(repo)
	if err != nil {
		return nil, err
	}
	res, err := s.ok(ctx, ws, repo, nil, nil, "branch", "-a", "--format=%(HEAD)|%(refname:short)|%(upstream:short)")
	if err != nil {
		return nil, err
	}
	return ParseBranches(res.Stdout), nil
}

func (s *Service) Switch(ctx context.Context, ws, actor, repo, branch string, create bool) error {
	repo, err := cleanRepo(repo)
	if err != nil {
		return err
	}
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	args := []string{"switch"}
	if create {
		args = append(args, "-c")
	}
	// Remote-only branch names ("origin/x") switch to a tracking branch of the same short name.
	args = append(args, strings.TrimPrefix(branch, "origin/"))
	if _, err := s.ok(ctx, ws, repo, nil, nil, args...); err != nil {
		return err
	}
	s.aud.User(ctx, actor, ws, "git.branch_switched", repo, map[string]any{"branch": branch, "created": create})
	return nil
}

func (s *Service) Stage(ctx context.Context, ws, repo string, paths []string, unstage bool) error {
	repo, err := cleanRepo(repo)
	if err != nil {
		return err
	}
	ps, err := cleanPaths(paths)
	if err != nil {
		return err
	}
	args := []string{"add", "--"}
	if unstage {
		args = []string{"restore", "--staged", "--"}
	}
	_, err = s.ok(ctx, ws, repo, nil, nil, append(args, ps...)...)
	return err
}

// Discard throws away working-tree changes to tracked files and deletes untracked ones.
func (s *Service) Discard(ctx context.Context, ws, actor, repo string, paths []string) error {
	repo, err := cleanRepo(repo)
	if err != nil {
		return err
	}
	ps, err := cleanPaths(paths)
	if err != nil {
		return err
	}
	st, err := s.Status(ctx, ws, repo)
	if err != nil {
		return err
	}
	untracked := map[string]bool{}
	for _, f := range st.Files {
		if f.Untracked {
			untracked[f.Path] = true
		}
	}
	var tracked, fresh []string
	for _, p := range ps {
		if untracked[p] {
			fresh = append(fresh, p)
		} else {
			tracked = append(tracked, p)
		}
	}
	if len(tracked) > 0 {
		if _, err := s.ok(ctx, ws, repo, nil, nil, append([]string{"restore", "--"}, tracked...)...); err != nil {
			return err
		}
	}
	if len(fresh) > 0 {
		if _, err := s.ok(ctx, ws, repo, nil, nil, append([]string{"clean", "-f", "--"}, fresh...)...); err != nil {
			return err
		}
	}
	s.aud.User(ctx, actor, ws, "git.discarded", repo, map[string]any{"files": len(ps)})
	return nil
}

func (s *Service) Commit(ctx context.Context, ws, actor, repo, message string, author Author) error {
	repo, err := cleanRepo(repo)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return bad("message", "Write a commit message.")
	}
	if len(message) > 10000 {
		return bad("message", "The commit message is too long.")
	}
	env := author.env()
	// Respect an identity already configured in the repository or globally.
	if res, err := s.git(ctx, ws, repo, nil, nil, "config", "user.email"); err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != "" {
		env = nil
	}
	if _, err := s.ok(ctx, ws, repo, nil, env, "commit", "-m", message); err != nil {
		return err
	}
	s.aud.User(ctx, actor, ws, "git.commit", repo, map[string]any{"messageLength": len(message)})
	return nil
}

// Sync runs fetch, pull (fast-forward only) or push for the repository's origin.
func (s *Service) Sync(ctx context.Context, ws, actor, repo, op string) (string, error) {
	repo, err := cleanRepo(repo)
	if err != nil {
		return "", err
	}
	var args []string
	switch op {
	case "fetch":
		args = []string{"fetch", "--prune", "origin"}
	case "pull":
		args = []string{"pull", "--ff-only", "origin"}
	case "push":
		st, err := s.Status(ctx, ws, repo)
		if err != nil {
			return "", err
		}
		if st.Detached || st.Branch == "" {
			return "", &Error{Code: "detached", Message: "Switch to a branch before pushing."}
		}
		args = []string{"push", "origin", "HEAD"}
		if st.Upstream == "" {
			args = []string{"push", "--set-upstream", "origin", st.Branch}
		}
	default:
		return "", bad("op", "Unknown operation.")
	}
	cred, remote, err := s.credFor(ctx, ws, repo)
	if err != nil {
		return "", err
	}
	res, err := s.ok(ctx, ws, repo, cred, nil, args...)
	if err != nil {
		return "", err
	}
	host := ""
	if u, perr := url.Parse(remote); perr == nil {
		host = u.Host
	}
	s.aud.User(ctx, actor, ws, "git."+op, repo, map[string]any{"host": host, "authenticated": cred != nil})
	return strings.TrimSpace(res.Stderr + res.Stdout), nil
}

const maxDiff = 1 << 20

func (s *Service) Diff(ctx context.Context, ws, repo, path string, staged bool) (string, bool, error) {
	repo, err := cleanRepo(repo)
	if err != nil {
		return "", false, err
	}
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if staged {
		args = append(args, "--cached")
	}
	if path != "" {
		ps, err := cleanPaths([]string{path})
		if err != nil {
			return "", false, err
		}
		args = append(args, "--")
		args = append(args, ps...)
	}
	res, err := s.ok(ctx, ws, repo, nil, nil, args...)
	if err != nil {
		return "", false, err
	}
	out, trunc := res.Stdout, res.Truncated
	if len(out) > maxDiff {
		out, trunc = out[:maxDiff], true
	}
	return out, trunc, nil
}

func (s *Service) Log(ctx context.Context, ws, repo string, n int) ([]Commit, error) {
	repo, err := cleanRepo(repo)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > 100 {
		n = 30
	}
	res, err := s.git(ctx, ws, repo, nil, nil, "log", "-n", itoa(n), "--format=%H%x1f%an%x1f%aI%x1f%s%x1e")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(res.Stderr, "does not have any commits") {
			return []Commit{}, nil
		}
		return nil, translate(res.Stderr)
	}
	return ParseLog(res.Stdout), nil
}

func (s *Service) PullRequestURL(ctx context.Context, ws, repo string) (string, error) {
	repo, err := cleanRepo(repo)
	if err != nil {
		return "", err
	}
	res, err := s.ok(ctx, ws, repo, nil, nil, "remote", "get-url", "origin")
	if err != nil {
		return "", &Error{Code: "no_remote", Message: "This repository has no remote named origin."}
	}
	st, err := s.Status(ctx, ws, repo)
	if err != nil {
		return "", err
	}
	u, perr := PullRequestURL(res.Stdout, st.Branch)
	if perr != nil {
		return "", &Error{Code: "no_pr_url", Message: perr.Error()}
	}
	return u, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
