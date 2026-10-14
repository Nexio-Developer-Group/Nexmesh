package agentapi_test

import (
	"context"
	"encoding/json"
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

type fakeEnv struct{}

func (fakeEnv) Environment(_ context.Context, ws string) (map[string]string, error) {
	return map[string]string{"PROD_DB_URI": "mongodb://u:s3cret@h/db", "WS": ws}, nil
}

func TestAgentEnvRequiresTokenAndIsNotCached(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u", Name: "n"})
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws2", OwnerID: "u", Name: "m"})
	reg := agentapi.NewRegistry(st, events.NewBus()).WithEnv(fakeEnv{})
	api := httpapi.New(log)
	reg.Register(api)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()
	tok1, _ := reg.IssueToken(ctx, "ws1")
	tok2, _ := reg.IssueToken(ctx, "ws2")

	get := func(ws, token string) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+"/agent/v1/workspaces/"+ws+"/env", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, _ := http.DefaultClient.Do(req)
		return res
	}
	if res := get("ws1", ""); res.StatusCode != 401 {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	if res := get("ws1", tok2); res.StatusCode != 401 {
		t.Fatalf("another workspace's token must not work: %d", res.StatusCode)
	}
	res := get("ws1", tok1)
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	var body struct{ Env map[string]string }
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.Env["WS"] != "ws1" {
		t.Fatalf("%v", body)
	}
}
