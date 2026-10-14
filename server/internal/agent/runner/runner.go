// Package runner executes one-off commands in the workspace with a timeout,
// bounded output and process-group cleanup.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"syscall"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

const (
	DefaultTimeout = 60 * time.Second
	MaxTimeout     = 10 * time.Minute
	MaxOutput      = 1 << 20
)

type EnvFunc func(ctx context.Context) (map[string]string, error)

type Runner struct {
	Root string
	Env  EnvFunc
	// Begin, when set, brackets each command so the agent can report it as running work.
	Begin func() (end func())
}

type Params struct {
	Command    string `json:"command"` // run through sh -c
	TimeoutSec int    `json:"timeoutSec"`
	Stdin      string `json:"stdin"`
}

type Result struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timedOut"`
	Truncated bool   `json:"truncated"`
	Millis    int64  `json:"durationMs"`
}

// capped keeps the first max bytes and counts the rest as dropped.
type capped struct {
	buf     bytes.Buffer
	max     int
	dropped bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	if len(p) > room {
		c.dropped = true
	}
	return len(p), nil
}

func (r *Runner) Run(ctx context.Context, p Params) (*Result, error) {
	if p.Command == "" {
		return nil, agentproto.Errf("bad_params", "Enter a command to run.")
	}
	to := DefaultTimeout
	if p.TimeoutSec > 0 {
		to = min(time.Duration(p.TimeoutSec)*time.Second, MaxTimeout)
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	cmd := exec.Command("/bin/sh", "-c", p.Command)
	cmd.Dir = r.Root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if r.Env != nil {
		extra, err := r.Env(ctx)
		if err != nil {
			return nil, agentproto.Errf("env_failed", "Could not load environment variables: %v", err)
		}
		cmd.Env = append(cmd.Environ(), flatten(extra)...)
	}
	out, errb := &capped{max: MaxOutput}, &capped{max: MaxOutput}
	cmd.Stdout, cmd.Stderr = out, errb
	if p.Stdin != "" {
		cmd.Stdin = bytes.NewBufferString(p.Stdin)
	}
	if r.Begin != nil {
		defer r.Begin()()
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, agentproto.Errf("start_failed", "Could not start the command: %v", err)
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
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		res.ExitCode = ee.ExitCode()
	}
	res.Stdout, res.Stderr = out.buf.String(), errb.buf.String()
	res.Truncated = out.dropped || errb.dropped
	res.Millis = time.Since(start).Milliseconds()
	return res, nil
}

func flatten(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

type installer interface {
	Handle(method string, h agentproto.Handler)
}

func (r *Runner) Install(a installer) {
	a.Handle("exec.run", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p Params
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, agentproto.Errf("bad_params", "Invalid request parameters.")
		}
		return r.Run(ctx, p)
	})
}
