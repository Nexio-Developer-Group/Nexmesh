// Package gitops is the control-plane side of Git integration (PRD F10): it
// turns structured requests into Git commands, runs them in the workspace
// through the agent with a credential attached only for that call, and
// turns Git's output into structured results and friendly errors.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent/gitrun"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
)

// Agent is the link to the workspace agent.
type Agent interface {
	Call(ctx context.Context, wsID, method string, params, out any) error
}

// AgentMarks tells the Git panel which files the Code Agent wrote (PRD F18),
// so commits that include them can say so.
type AgentMarks interface {
	AgentFiles(ctx context.Context, wsID string) map[string]string
	ConsumeAgentFiles(ctx context.Context, wsID string, paths []string)
}

type Service struct {
	agent Agent
	creds *gitcred.Service
	aud   *audit.Recorder
	marks AgentMarks
}

// WithAgentMarks enables the Assisted-by trailer on commits with agent-written files.
func (s *Service) WithAgentMarks(m AgentMarks) *Service { s.marks = m; return s }

// AgentTrailer is added to the message of a commit that includes agent-written files.
const AgentTrailer = "Assisted-by: Nexmesh Code Agent"

func NewService(a Agent, creds *gitcred.Service, aud *audit.Recorder) *Service {
	return &Service{agent: a, creds: creds, aud: aud}
}

// Error is a Git failure with a stable code and a user-presentable message.
type Error struct {
	Code    string
	Message string
	Field   string
	Detail  string // scrubbed stderr tail, for the details disclosure
}

func (e *Error) Error() string { return e.Message }

func bad(field, format string, a ...any) *Error {
	return &Error{Code: "invalid", Field: field, Message: fmt.Sprintf(format, a...)}
}

var (
	branchRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]{0,199}$`)
	dirRE    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._ -]{0,99}$`)
	hostRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:[0-9]{1,5})?$`)
)

// ValidateBranch applies a conservative subset of git check-ref-format.
func ValidateBranch(b string) error {
	if !branchRE.MatchString(b) || strings.Contains(b, "..") || strings.Contains(b, "//") || strings.HasSuffix(b, "/") ||
		strings.HasSuffix(b, ".lock") || strings.HasSuffix(b, ".") || strings.Contains(b, "/.") || strings.Contains(b, "@{") {
		return bad("branch", "Use letters, digits and - _ . / in the branch name (no spaces, no \"..\").")
	}
	return nil
}

// cleanRepo validates a root-relative repository directory ("" is the root).
func cleanRepo(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" {
		return "", nil
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, "-") || strings.ContainsRune(seg, 0) {
			return "", bad("repo", "That repository path is not valid.")
		}
	}
	return p, nil
}

func cleanPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, bad("paths", "Choose at least one file.")
	}
	if len(paths) > 500 {
		return nil, bad("paths", "Too many files in one request.")
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./")
		if p == "" || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") {
			return nil, bad("paths", "A file path is not valid.")
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return nil, bad("paths", "A file path is not valid.")
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// ParseRemoteURL accepts https remotes only, without embedded credentials.
func ParseRemoteURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, bad("url", "Enter an https:// repository URL. SSH and other protocols are not supported yet.")
	}
	if u.User != nil {
		return nil, bad("url", "Remove the username and password from the URL. Add a token under Git credentials instead.")
	}
	if !hostRE.MatchString(strings.ToLower(u.Host)) || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, " ") {
		return nil, bad("url", "That repository URL is not valid.")
	}
	return u, nil
}

// DefaultDir names the clone directory after the repository.
func DefaultDir(u *url.URL) string {
	b := u.Path[strings.LastIndex(u.Path, "/")+1:]
	b = strings.TrimSuffix(b, ".git")
	if !dirRE.MatchString(b) {
		return "repo"
	}
	return b
}

type runResult struct {
	gitrun.Result
}

// git runs one command; cred may be nil (local operations, public repos).
func (s *Service) git(ctx context.Context, ws, repo string, cred *gitcred.Secret, env map[string]string, args ...string) (*runResult, error) {
	p := gitrun.Params{Args: args, Cwd: repo, Env: env}
	if cred != nil {
		p.Cred = &gitrun.Cred{Host: cred.Host, Username: cred.Username, Token: cred.Token}
	}
	var res runResult
	if err := s.agent.Call(ctx, ws, "git.run", p, &res.Result); err != nil {
		return nil, err
	}
	return &res, nil
}

// ok runs a command and converts a non-zero exit into a friendly *Error.
func (s *Service) ok(ctx context.Context, ws, repo string, cred *gitcred.Secret, env map[string]string, args ...string) (*runResult, error) {
	res, err := s.git(ctx, ws, repo, cred, env, args...)
	if err != nil {
		return nil, err
	}
	if res.TimedOut {
		return nil, &Error{Code: "timeout", Message: "Git took too long and was stopped. Check the network and try again."}
	}
	if res.ExitCode != 0 {
		return nil, translate(res.Stderr + "\n" + res.Stdout)
	}
	return res, nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > 1200 {
		out = out[len(out)-1200:]
	}
	return out
}

// translate maps Git's stderr to a stable code and a helpful message.
func translate(out string) *Error {
	l := strings.ToLower(out)
	e := &Error{Detail: tail(out)}
	switch {
	case strings.Contains(l, "authentication failed"), strings.Contains(l, "could not read username"), strings.Contains(l, "terminal prompts disabled"),
		strings.Contains(l, "invalid username or password"), strings.Contains(l, "returned error: 401"), strings.Contains(l, "returned error: 403"), strings.Contains(l, "http basic: access denied"):
		e.Code, e.Message = "auth_failed", "Git rejected the credentials. Add or replace the access token under Git credentials."
	case strings.Contains(l, "repository not found"), strings.Contains(l, "does not appear to be a git repository"), strings.Contains(l, "returned error: 404"):
		e.Code, e.Message = "repo_not_found", "Repository not found, or the token has no access to it."
	case strings.Contains(l, "not a git repository"):
		e.Code, e.Message = "not_a_repo", "That folder is not a Git repository."
	case strings.Contains(l, "non-fast-forward"), strings.Contains(l, "fetch first"), strings.Contains(l, "[rejected]"), strings.Contains(l, "failed to push some refs"):
		e.Code, e.Message = "push_rejected", "The remote has commits you don't have. Pull first, then push again."
	case strings.Contains(l, "conflict"), strings.Contains(l, "not possible to fast-forward"), strings.Contains(l, "divergent branches"), strings.Contains(l, "need to specify how to reconcile"):
		e.Code, e.Message = "pull_conflict", "Your branch and the remote have diverged. Resolve this in the terminal (merge or rebase), then try again."
	case strings.Contains(l, "author identity unknown"), strings.Contains(l, "please tell me who you are"), strings.Contains(l, "unable to auto-detect email"):
		e.Code, e.Message = "identity_unknown", "Git doesn't know who you are. Set user.name and user.email in the terminal, or commit from the Git panel while signed in."
	case strings.Contains(l, "nothing to commit"), strings.Contains(l, "no changes added to commit"), strings.Contains(l, "nothing added to commit"):
		e.Code, e.Message = "nothing_to_commit", "There is nothing staged to commit."
	case strings.Contains(l, "already exists and is not an empty directory"):
		e.Code, e.Message = "dir_exists", "That folder already exists and is not empty. Choose another folder name."
	case strings.Contains(l, "no upstream"), strings.Contains(l, "has no upstream branch"):
		e.Code, e.Message = "no_upstream", "This branch has no remote yet. Push it once to publish it."
	case strings.Contains(l, "would be overwritten"), strings.Contains(l, "your local changes"):
		e.Code, e.Message = "dirty_tree", "You have uncommitted changes that would be overwritten. Commit or discard them first."
	case strings.Contains(l, "could not resolve host"), strings.Contains(l, "failed to connect"), strings.Contains(l, "connection timed out"), strings.Contains(l, "network is unreachable"):
		e.Code, e.Message = "network", "Could not reach the Git host from the workspace. Check network access."
	default:
		e.Code, e.Message = "git_failed", "Git could not complete that. "+firstLine(out)
	}
	return e
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}

// --- results ---

type FileChange struct {
	Path       string `json:"path"`
	OrigPath   string `json:"origPath,omitempty"`
	Index      string `json:"index"`    // X column
	Worktree   string `json:"worktree"` // Y column
	Staged     bool   `json:"staged"`
	Unstaged   bool   `json:"unstaged"`
	Untracked  bool   `json:"untracked"`
	Conflicted bool   `json:"conflicted"`
}

type Status struct {
	Repo     string       `json:"repo"`
	Branch   string       `json:"branch"`
	Detached bool         `json:"detached"`
	Upstream string       `json:"upstream,omitempty"`
	Ahead    int          `json:"ahead"`
	Behind   int          `json:"behind"`
	Files    []FileChange `json:"files"`
	Clean    bool         `json:"clean"`
	// AgentFiles are changed files that were written by accepted Code Agent changes.
	AgentFiles []string `json:"agentFiles"`
}

// ParseStatus reads `git status --porcelain=v1 -b -z` output.
func ParseStatus(repo, out string) *Status {
	st := &Status{Repo: repo, Files: []FileChange{}, AgentFiles: []string{}}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "## ") {
			parseBranchLine(st, e[3:])
			continue
		}
		if len(e) < 4 {
			continue
		}
		x, y, path := string(e[0]), string(e[1]), e[3:]
		fc := FileChange{Path: path, Index: x, Worktree: y}
		if x == "R" || x == "C" {
			if i+1 < len(entries) {
				fc.OrigPath = entries[i+1]
				i++
			}
		}
		switch {
		case x == "?" && y == "?":
			fc.Untracked = true
		case x == "U" || y == "U" || (x == "A" && y == "A") || (x == "D" && y == "D"):
			fc.Conflicted = true
		default:
			fc.Staged = x != " " && x != "?"
			fc.Unstaged = y != " " && y != "?"
		}
		st.Files = append(st.Files, fc)
	}
	st.Clean = len(st.Files) == 0
	return st
}

var aheadBehindRE = regexp.MustCompile(`\[(?:ahead (\d+))?(?:, )?(?:behind (\d+))?\]`)

func parseBranchLine(st *Status, l string) {
	switch {
	case strings.HasPrefix(l, "No commits yet on "):
		st.Branch = strings.TrimPrefix(l, "No commits yet on ")
		return
	case strings.HasPrefix(l, "HEAD (no branch)"):
		st.Detached, st.Branch = true, "HEAD"
		return
	}
	if m := aheadBehindRE.FindStringSubmatch(l); m != nil {
		st.Ahead, _ = strconv.Atoi(m[1])
		st.Behind, _ = strconv.Atoi(m[2])
		l = strings.TrimSpace(l[:strings.Index(l, " [")])
	}
	if b, up, ok := strings.Cut(l, "..."); ok {
		st.Branch, st.Upstream = b, up
	} else {
		st.Branch = l
	}
}

type Branch struct {
	Name     string `json:"name"`
	Current  bool   `json:"current"`
	Remote   bool   `json:"remote"`
	Upstream string `json:"upstream,omitempty"`
}

// ParseBranches reads `git branch -a --format=%(HEAD)|%(refname:short)|%(upstream:short)`.
func ParseBranches(out string) []Branch {
	bs := []Branch{}
	for _, l := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimRight(l, "\r"), "|", 3)
		if len(parts) < 2 || parts[1] == "" || strings.HasSuffix(parts[1], "/HEAD") || strings.HasPrefix(parts[1], "(") {
			continue
		}
		b := Branch{Name: parts[1], Current: parts[0] == "*"}
		if len(parts) == 3 {
			b.Upstream = parts[2]
		}
		b.Remote = strings.HasPrefix(b.Name, "origin/")
		bs = append(bs, b)
	}
	return bs
}

type Commit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

// ParseLog reads `git log --format=%H%x1f%an%x1f%aI%x1f%s%x1e`.
func ParseLog(out string) []Commit {
	cs := []Commit{}
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		f := strings.Split(rec, "\x1f")
		if len(f) < 4 {
			continue
		}
		short := f[0]
		if len(short) > 7 {
			short = short[:7]
		}
		cs = append(cs, Commit{Hash: f[0], Short: short, Author: f[1], Date: f[2], Subject: f[3]})
	}
	return cs
}

// PullRequestURL builds the "open a pull request" link for a remote and branch.
func PullRequestURL(remote, branch string) (string, error) {
	u, err := ParseRemoteURL(strings.TrimSuffix(strings.TrimSpace(remote), "/"))
	if err != nil {
		return "", errors.New("The origin remote is not an https URL, so a pull request link can't be built.")
	}
	if err := ValidateBranch(branch); err != nil {
		return "", errors.New("Switch to a named branch first.")
	}
	path := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	base := "https://" + u.Host + path
	q := url.QueryEscape(branch)
	switch {
	case u.Host == "github.com" || strings.Contains(u.Host, "github"):
		return base + "/compare/" + url.PathEscape(branch) + "?expand=1", nil
	case u.Host == "gitlab.com" || strings.Contains(u.Host, "gitlab"):
		return base + "/-/merge_requests/new?merge_request%5Bsource_branch%5D=" + q, nil
	case u.Host == "bitbucket.org":
		return base + "/pull-requests/new?source=" + q, nil
	}
	return base + "/compare/" + url.PathEscape(branch), nil
}
