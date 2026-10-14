// Package gitrun runs Git inside the workspace on behalf of the control
// plane. The control plane builds every command from a structured request and
// may attach a credential for the duration of one call. The credential is
// delivered through environment-based Git config (never argv, never
// .git/config, never a credential helper) and scrubbed from all output.
package gitrun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

const (
	maxOutput      = 2 << 20
	defaultTimeout = 2 * time.Minute
	maxTimeout     = 15 * time.Minute
)

// subcommands the control plane may request.
var allowed = map[string]bool{
	"clone": true, "init": true, "status": true, "branch": true, "switch": true, "checkout": true, "add": true, "restore": true,
	"commit": true, "pull": true, "push": true, "fetch": true, "diff": true, "log": true, "ls-remote": true, "remote": true,
	"rev-parse": true, "config": true, "clean": true,
}

// options that can execute code or redirect Git; never passed through.
var forbiddenPrefix = []string{"--config", "--upload-pack", "--receive-pack", "--exec", "--git-dir", "--work-tree", "--template", "--ext", "--ssh-command", "--recurse-submodules"}

var envAllowed = map[string]bool{"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true}

type Cred struct {
	// Scheme is "https" unless a test points Git at a local HTTP server; the
	// control plane only ever builds https remotes.
	Scheme   string `json:"scheme,omitempty"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

type Params struct {
	Args       []string          `json:"args"`
	Cwd        string            `json:"cwd"` // root-relative
	Cred       *Cred             `json:"cred,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	TimeoutSec int               `json:"timeoutSec"`
}

type Result struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timedOut"`
	Truncated bool   `json:"truncated"`
}

type Runner struct {
	Root string
}

// safeDir resolves a root-relative directory and verifies, after resolving
// symlinks, that it is still inside the root.
func (r *Runner) safeDir(rel string) (string, error) {
	rel = filepath.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	root, err := filepath.EvalSymlinks(r.Root)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", agentproto.Errf("not_found", "That folder does not exist in the workspace.")
	}
	if dir != root && !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		return "", agentproto.Errf("outside_workspace", "That folder resolves outside the workspace.")
	}
	return dir, nil
}

type capped struct {
	buf     bytes.Buffer
	dropped bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := maxOutput - c.buf.Len()
	if room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	if len(p) > room {
		c.dropped = true
	}
	return len(p), nil
}

func validate(args []string) error {
	if len(args) == 0 || !allowed[args[0]] {
		return agentproto.Errf("bad_params", "That Git command is not allowed.")
	}
	switch args[0] {
	case "remote": // read-only: the control plane only asks for the origin URL
		if len(args) != 3 || args[1] != "get-url" || args[2] != "origin" {
			return agentproto.Errf("bad_params", "That Git command is not allowed.")
		}
	case "config": // read-only identity lookup
		if len(args) != 2 || (args[1] != "user.email" && args[1] != "user.name") {
			return agentproto.Errf("bad_params", "That Git command is not allowed.")
		}
	}
	for _, a := range args[1:] {
		if a == "--" {
			break // everything after is a path
		}
		if a == "-c" && args[0] == "clone" { // clone -c key=value writes arbitrary config into the new repository
			return agentproto.Errf("bad_params", "Option %q is not allowed.", a)
		}
		for _, f := range forbiddenPrefix {
			if a == f || strings.HasPrefix(a, f+"=") || (len(f) > 2 && strings.HasPrefix(a, f)) {
				return agentproto.Errf("bad_params", "Option %q is not allowed.", a)
			}
		}
		if strings.Contains(a, "ext::") || strings.HasPrefix(a, "-") && strings.Contains(a, "$(") {
			return agentproto.Errf("bad_params", "Argument is not allowed.")
		}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, p Params) (*Result, error) {
	if err := validate(p.Args); err != nil {
		return nil, err
	}
	dir, err := r.safeDir(p.Cwd)
	if err != nil {
		return nil, err
	}
	to := defaultTimeout
	if p.TimeoutSec > 0 {
		to = min(time.Duration(p.TimeoutSec)*time.Second, maxTimeout)
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never", "LC_ALL=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	cfg := [][2]string{
		{"credential.helper", ""},       // never persist or consult helpers
		{"protocol.ext.allow", "never"}, // no ext:: transports
		{"protocol.file.allow", "user"}, // local clones only on explicit user action, not via URLs we build
		{"core.hooksPath", "/dev/null"}, // hooks in untrusted clones must not run with injected credentials
		{"advice.detachedHead", "false"},
	}
	var secrets []string
	if p.Cred != nil && p.Cred.Token != "" {
		scheme := p.Cred.Scheme
		if scheme != "http" {
			scheme = "https"
		}
		basic := base64.StdEncoding.EncodeToString([]byte(p.Cred.Username + ":" + p.Cred.Token))
		cfg = append(cfg,
			[2]string{"http." + scheme + "://" + p.Cred.Host + "/.extraHeader", "Authorization: Basic " + basic},
			// Hooks in an untrusted clone must not run while a credential is in the environment.
			[2]string{"core.hooksPath", "/dev/null"})
		secrets = []string{p.Cred.Token, basic}
	}
	env = append(env, "GIT_CONFIG_COUNT="+itoa(len(cfg)))
	for i, kv := range cfg {
		env = append(env, "GIT_CONFIG_KEY_"+itoa(i)+"="+kv[0], "GIT_CONFIG_VALUE_"+itoa(i)+"="+kv[1])
	}
	keys := make([]string, 0, len(p.Env))
	for k := range p.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if envAllowed[k] {
			env = append(env, k+"="+p.Env[k])
		}
	}

	cmd := exec.Command("git", p.Args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, errb := &capped{}, &capped{}
	cmd.Stdout, cmd.Stderr = out, errb
	if err := cmd.Start(); err != nil {
		return nil, agentproto.Errf("start_failed", "Git is not available in this workspace.")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	res := &Result{}
	var werr error
	select {
	case werr = <-done:
	case <-cctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		werr = <-done
		res.TimedOut = ctx.Err() == nil
	}
	if ee, ok := werr.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	}
	res.Stdout, res.Stderr = scrub(out.buf.String(), secrets), scrub(errb.buf.String(), secrets)
	res.Truncated = out.dropped || errb.dropped
	return res, nil
}

func scrub(s string, secrets []string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
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

// Repos finds Git working copies up to three levels below the root.
func (r *Runner) Repos() []string {
	var out []string
	root, err := filepath.EvalSymlinks(r.Root)
	if err != nil {
		return out
	}
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		if fi, err := os.Lstat(filepath.Join(root, rel, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			out = append(out, rel)
			return // do not descend into a repository
		}
		if depth >= 3 {
			return
		}
		ents, _ := os.ReadDir(filepath.Join(root, rel))
		for _, e := range ents {
			if e.IsDir() && e.Type()&os.ModeSymlink == 0 && !strings.HasPrefix(e.Name(), ".") && e.Name() != "node_modules" {
				walk(filepath.Join(rel, e.Name()), depth+1)
			}
		}
	}
	walk("", 0)
	sort.Strings(out)
	return out
}

type installer interface {
	Handle(method string, h agentproto.Handler)
}

func (r *Runner) Install(a installer) {
	a.Handle("git.run", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p Params
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, agentproto.Errf("bad_params", "Invalid request parameters.")
		}
		return r.Run(ctx, p)
	})
	a.Handle("git.repos", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"repos": r.Repos()}, nil
	})
}
