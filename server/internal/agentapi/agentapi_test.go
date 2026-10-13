package agentapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func TestAgentHandshake(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u", Name: "n", Status: domain.WSReady})
	reg := agentapi.NewRegistry(st, events.NewBus())
	api := httpapi.New(log)
	reg.Register(api)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	tok, err := reg.IssueToken(ctx, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	// The plaintext token is never stored.
	rec, _ := st.GetSecret(ctx, "agenttoken_ws1")
	if strings.Contains(string(rec.Ciphertext), tok) {
		t.Fatal("token stored in plaintext")
	}

	// Wrong token is rejected.
	res, _ := http.Post(ts.URL+"/agent/v1/workspaces/ws1/hello", "application/json", strings.NewReader(`{}`))
	if res.StatusCode != 401 {
		t.Fatalf("no token: %d", res.StatusCode)
	}

	a := agent.New(agent.Config{ControlURL: ts.URL, WorkspaceID: "ws1", Token: tok}, log)
	runCtx, stop := context.WithCancel(ctx)
	go func() { _ = a.Run(runCtx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w, _ := st.GetWorkspace(ctx, "ws1")
		if w.AgentConnected {
			stop()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent never registered")
}

func TestStaleAgentMarkedDisconnected(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	old := time.Now().Add(-2 * time.Minute)
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u", Name: "n", AgentConnected: true, AgentLastSeen: &old})
	agentapi.NewRegistry(st, events.NewBus()).MarkStale(ctx)
	if w, _ := st.GetWorkspace(ctx, "ws1"); w.AgentConnected {
		t.Fatal("stale agent should be disconnected")
	}
}
