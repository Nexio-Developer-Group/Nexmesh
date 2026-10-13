// Package agent is the workspace agent: a small process inside every
// workspace container that dials out to the control plane (PRD 20.3.4).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
)

const Version = "0.1.0"

type Config struct {
	ControlURL  string
	WorkspaceID string
	Token       string
}

func ConfigFromEnv() (Config, error) {
	c := Config{ControlURL: os.Getenv("NEXMESH_CONTROL_URL"), WorkspaceID: os.Getenv("NEXMESH_WORKSPACE_ID"), Token: os.Getenv("NEXMESH_AGENT_TOKEN")}
	if c.ControlURL == "" || c.WorkspaceID == "" || c.Token == "" {
		return c, fmt.Errorf("NEXMESH_CONTROL_URL, NEXMESH_WORKSPACE_ID and NEXMESH_AGENT_TOKEN must be set")
	}
	return c, nil
}

type Agent struct {
	cfg Config
	log *slog.Logger
	hc  *http.Client
}

func New(cfg Config, log *slog.Logger) *Agent {
	return &Agent{cfg: cfg, log: log, hc: &http.Client{Timeout: 15 * time.Second}}
}

func (a *Agent) post(ctx context.Context, path string, in, out any) error {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.ControlURL+"/agent/v1/workspaces/"+a.cfg.WorkspaceID+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("control plane returned %s", res.Status)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

// Run connects with exponential backoff, then heartbeats until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	host, _ := os.Hostname()
	backoff := time.Second
	var hello agentapi.HelloResponse
	for {
		err := a.post(ctx, "/hello", agentapi.Hello{Hostname: host, AgentVersion: Version, OS: "ubuntu-24.04"}, &hello)
		if err == nil {
			break
		}
		a.log.Warn("control plane unreachable, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	a.log.Info("connected to control plane", "workspace", hello.WorkspaceID)
	every := time.Duration(hello.HeartbeatEvery) * time.Second
	if every <= 0 {
		every = agentapi.HeartbeatEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := a.post(ctx, "/heartbeat", agentapi.Heartbeat{}, nil); err != nil {
				a.log.Warn("heartbeat failed", "err", err)
			}
		}
	}
}
