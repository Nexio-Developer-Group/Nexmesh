package agentapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type rig struct {
	reg  *agentapi.Registry
	st   store.Store
	ts   *httptest.Server
	tok  string
	ctx  context.Context
	stop context.CancelFunc
	ag   *agent.Agent
}

func newRig(t *testing.T) *rig {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := store.NewMemory()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u", Name: "n", Status: domain.WSReady})
	reg := agentapi.NewRegistry(st, events.NewBus())
	api := httpapi.New(log)
	reg.Register(api)
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	tok, _ := reg.IssueToken(ctx, "ws1")
	ag := agent.New(agent.Config{ControlURL: ts.URL, WorkspaceID: "ws1", Token: tok}, log)
	return &rig{reg: reg, st: st, ts: ts, tok: tok, ctx: ctx, stop: cancel, ag: ag}
}

func (r *rig) waitOnline(t *testing.T, want bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if r.reg.Online("ws1") == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("online never became %v", want)
}

func TestLinkRequestResponseAndEvents(t *testing.T) {
	r := newRig(t)
	r.ag.Handle("echo", func(_ context.Context, p json.RawMessage) (any, error) {
		var in struct{ Msg string }
		_ = json.Unmarshal(p, &in)
		return map[string]string{"msg": strings.ToUpper(in.Msg)}, nil
	})
	r.ag.Handle("deny", func(context.Context, json.RawMessage) (any, error) {
		return nil, agentproto.Errf("forbidden", "Not allowed here.")
	})
	r.ag.Handle("boom", func(context.Context, json.RawMessage) (any, error) { panic("x") })
	var got atomic.Value
	cancel := r.reg.Subscribe("ws1", func(m string, p json.RawMessage) {
		if m == "ping.event" {
			got.Store(string(p))
		}
	})
	defer cancel()
	go func() { _ = r.ag.Run(r.ctx) }()
	r.waitOnline(t, true)

	w, _ := r.st.GetWorkspace(r.ctx, "ws1")
	if !w.AgentConnected {
		t.Fatal("presence not recorded")
	}

	var out struct{ Msg string }
	if err := r.reg.Call(r.ctx, "ws1", "echo", map[string]string{"msg": "hi"}, &out); err != nil || out.Msg != "HI" {
		t.Fatalf("echo: %v %+v", err, out)
	}
	var re *agentproto.Error
	if err := r.reg.Call(r.ctx, "ws1", "deny", nil, nil); !errors.As(err, &re) || re.Code != "forbidden" {
		t.Fatalf("typed error lost: %v", err)
	}
	if err := r.reg.Call(r.ctx, "ws1", "boom", nil, nil); !errors.As(err, &re) || re.Code != "internal" {
		t.Fatalf("panic must become an internal error: %v", err)
	}
	if err := r.reg.Call(r.ctx, "ws1", "nope", nil, nil); !errors.As(err, &re) || re.Code != "unknown_method" {
		t.Fatalf("unknown method: %v", err)
	}
	r.ag.Emit("ping.event", map[string]int{"n": 1})
	for i := 0; i < 100 && got.Load() == nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if got.Load() != `{"n":1}` {
		t.Fatalf("event not delivered: %v", got.Load())
	}
}

func TestLinkRejectsBadToken(t *testing.T) {
	r := newRig(t)
	req, _ := http.NewRequest("GET", r.ts.URL+"/agent/v1/workspaces/ws1/ws", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 401 {
		t.Fatalf("bad token: %v %v", res, err)
	}
	if r.reg.Online("ws1") {
		t.Fatal("must not be online")
	}
	if err := r.reg.Call(r.ctx, "ws1", "echo", nil, nil); !errors.Is(err, agentapi.ErrOffline) {
		t.Fatalf("want ErrOffline, got %v", err)
	}
}

func TestLinkReconnects(t *testing.T) {
	r := newRig(t)
	go func() { _ = r.ag.Run(r.ctx) }()
	r.waitOnline(t, true)
	// Drop the server side of the link; the agent must come back on its own.
	r.reg.Drop("ws1")
	r.waitOnline(t, false)
	w, _ := r.st.GetWorkspace(r.ctx, "ws1")
	if w.AgentConnected {
		t.Fatal("disconnect not reflected in workspace")
	}
	r.waitOnline(t, true)
	var out map[string]string
	if err := r.reg.Call(r.ctx, "ws1", "ping", nil, &out); err != nil || out["version"] == "" {
		t.Fatalf("ping after reconnect: %v %v", err, out)
	}
}
