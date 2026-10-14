// Package agent is the workspace agent: a small process inside every
// workspace container that dials out to the control plane (PRD 20.3.4) and
// then serves requests from it over one WebSocket.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

const Version = "0.2.0"

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

// Installer lets feature modules (filesystem, terminals, ...) register their
// request handlers and receive an emitter for events.
type Installer func(a *Agent)

type Agent struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	handlers map[string]agentproto.Handler
	peer     *agentproto.Peer
	onConn   []func()
}

func New(cfg Config, log *slog.Logger) *Agent {
	return &Agent{cfg: cfg, log: log, handlers: map[string]agentproto.Handler{}}
}

// Handle registers a request handler (before Run).
func (a *Agent) Handle(method string, h agentproto.Handler) { a.handlers[method] = h }

// OnConnect registers a callback run after every (re)connection, e.g. to
// re-announce listening ports.
func (a *Agent) OnConnect(fn func()) { a.onConn = append(a.onConn, fn) }

// Emit sends an event to the control plane; it is dropped while disconnected.
func (a *Agent) Emit(method string, params any) {
	a.mu.Lock()
	p := a.peer
	a.mu.Unlock()
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.Emit(ctx, method, params)
}

func wsURL(controlURL, id string) (string, error) {
	u, err := url.Parse(controlURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/agent/v1/workspaces/" + id + "/ws"
	return u.String(), nil
}

// Run keeps one link to the control plane open, reconnecting with
// exponential backoff, until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	target, err := wsURL(a.cfg.ControlURL, a.cfg.WorkspaceID)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	backoff := time.Second
	for {
		start := time.Now()
		err := a.session(ctx, target, host)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // a long-lived link resets the backoff
		}
		a.log.Warn("control plane link down, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) session(ctx context.Context, target, host string) error {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, target, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + a.cfg.Token}}})
	if err != nil {
		return err
	}
	peer := agentproto.New(c)
	for m, h := range a.handlers {
		peer.Handle(m, h)
	}
	peer.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return map[string]string{"version": Version}, nil })
	a.mu.Lock()
	a.peer = peer
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.peer = nil; a.mu.Unlock() }()

	_ = peer.Emit(ctx, "hello", agentapi.Hello{Hostname: host, AgentVersion: Version, OS: "ubuntu-24.04"})
	a.log.Info("connected to control plane", "workspace", a.cfg.WorkspaceID)
	for _, f := range a.onConn {
		go f()
	}
	return peer.Serve(ctx)
}
