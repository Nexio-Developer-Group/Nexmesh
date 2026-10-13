package httpapi

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

	"github.com/nexio-developer-group/nexmesh/server/internal/agentcfg"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

const apiKey = "sk-test-API-KEY-0123456789"

// A fake OpenAI-compatible endpoint that requires apiKey.
func fakeModelServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return false
		}
		return true
	}
	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1"},{"id":"qwen"}]}`))
		}
	})
	mux.HandleFunc("POST /chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func agentStack(t *testing.T) *httptest.Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200})
	eng := ops.NewEngine(st, bus, log, 2)
	aud := audit.New(st, log)
	svc := workspace.NewService(st, drv, eng, bus, aud, log)
	key := []byte("0123456789abcdef0123456789abcdef")
	be, _ := secrets.NewStoreBackend(st, key)
	provs, err := catalog.LoadProviders("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	ag := agentcfg.NewService(st, secrets.New(be), netguard.New(netguard.Policy{AllowLoopback: true}), aud, provs, key)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eng.Start(ctx)
	api := New(log)
	api.RegisterWorkspaces(WorkspaceDeps{Svc: svc, Driver: drv, Store: st, Bus: bus, Agent: ag})
	api.RegisterAgent(ag)
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestProvidersEndpointIsCatalogDriven(t *testing.T) {
	ts := agentStack(t)
	res, body := do(t, "GET", ts.URL+"/api/agent/providers", "")
	provs := body["providers"].([]any)
	if res.StatusCode != 200 || len(provs) != 4 || provs[0].(map[string]any)["id"] != "anthropic" || provs[3].(map[string]any)["id"] != "custom" {
		t.Fatalf("%d %v", res.StatusCode, body)
	}
}

func TestAdvancedSetupGatesWorkspaceCreation(t *testing.T) {
	ms := fakeModelServer(t)
	ts := agentStack(t)
	agent := `{"name":"Local","provider":"custom","protocol":"openai","endpoint":"` + ms.URL + `","modelId":"gpt-4.1","apiKey":"` + apiKey + `"}`

	// No agent section: never blocks.
	res, _ := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"plain","cpu":1,"ramGb":1,"storageGb":5}`)
	if res.StatusCode != 201 {
		t.Fatalf("%d", res.StatusCode)
	}
	// A started but untested section blocks, and nothing is created.
	res, body := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"gated","cpu":1,"ramGb":1,"storageGb":5,"agent":`+agent+`}`)
	e, _ := body["error"].(map[string]any)
	if res.StatusCode != 422 || e["code"] != "test_required" {
		t.Fatalf("%d %v", res.StatusCode, body)
	}
	_, list := do(t, "GET", ts.URL+"/api/workspaces", "")
	if len(list["workspaces"].([]any)) != 1 {
		t.Fatal("a blocked creation must not leave a workspace behind")
	}
	// Test it, then create: the configuration is saved with the workspace.
	res, tr := do(t, "POST", ts.URL+"/api/agent/test", agent)
	if res.StatusCode != 200 || tr["passed"] != true || len(tr["models"].([]any)) != 2 {
		t.Fatalf("%d %v", res.StatusCode, tr)
	}
	time.Sleep(1100 * time.Millisecond) // test rate limit is irrelevant to create
	res, ws := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"with-agent","cpu":1,"ramGb":1,"storageGb":5,"agent":`+agent+`}`)
	if res.StatusCode != 201 || ws["agentError"] != nil {
		t.Fatalf("%d %v", res.StatusCode, ws)
	}
	id := ws["id"].(string)
	res, cfgs := do(t, "GET", ts.URL+"/api/workspaces/"+id+"/agent-configs", "")
	configs := cfgs["configs"].([]any)
	if res.StatusCode != 200 || len(configs) != 1 {
		t.Fatalf("%d %v", res.StatusCode, cfgs)
	}
	c := configs[0].(map[string]any)
	if c["hasKey"] != true || c["isDefault"] != true || c["modelId"] != "gpt-4.1" {
		t.Fatalf("%v", c)
	}
	// The key never appears in any response.
	raw, _ := json.Marshal(cfgs)
	if strings.Contains(string(raw), apiKey) {
		t.Fatalf("API response leaks the key: %s", raw)
	}
}

func TestAgentConfigCRUDOverHTTP(t *testing.T) {
	ms := fakeModelServer(t)
	ts := agentStack(t)
	_, ws := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"cfg","cpu":1,"ramGb":1,"storageGb":5}`)
	id := ws["id"].(string)
	in := func(name string) string {
		return `{"name":"` + name + `","provider":"custom","protocol":"openai","endpoint":"` + ms.URL + `","modelId":"gpt-4.1","apiKey":"` + apiKey + `"}`
	}
	// Saving without a passing test is rejected.
	res, body := do(t, "POST", ts.URL+"/api/workspaces/"+id+"/agent-configs", in("A"))
	if res.StatusCode != 422 {
		t.Fatalf("%d %v", res.StatusCode, body)
	}
	do(t, "POST", ts.URL+"/api/agent/test", in("A"))
	res, a := do(t, "POST", ts.URL+"/api/workspaces/"+id+"/agent-configs", in("A"))
	if res.StatusCode != 201 {
		t.Fatalf("%d %v", res.StatusCode, a)
	}
	time.Sleep(1100 * time.Millisecond)
	do(t, "POST", ts.URL+"/api/agent/test", in("B"))
	res, b := do(t, "POST", ts.URL+"/api/workspaces/"+id+"/agent-configs", in("B"))
	if res.StatusCode != 201 || b["isDefault"] != false {
		t.Fatalf("%d %v", res.StatusCode, b)
	}
	if res, _ = do(t, "POST", ts.URL+"/api/workspaces/"+id+"/agent-configs/"+b["id"].(string)+"/default", ""); res.StatusCode != 204 {
		t.Fatalf("default: %d", res.StatusCode)
	}
	time.Sleep(1100 * time.Millisecond)
	res, rt := do(t, "POST", ts.URL+"/api/workspaces/"+id+"/agent-configs/"+a["id"].(string)+"/retest", "")
	if res.StatusCode != 200 || rt["passed"] != true {
		t.Fatalf("retest: %d %v", res.StatusCode, rt)
	}
	// A bad replacement key is rejected with the failing result and keeps the old key.
	time.Sleep(1100 * time.Millisecond)
	res, bad := do(t, "PUT", ts.URL+"/api/workspaces/"+id+"/agent-configs/"+a["id"].(string)+"/key", `{"apiKey":"wrong-key-wrong-key"}`)
	if res.StatusCode != 422 || bad["result"] == nil {
		t.Fatalf("%d %v", res.StatusCode, bad)
	}
	if res, _ = do(t, "DELETE", ts.URL+"/api/workspaces/"+id+"/agent-configs/"+a["id"].(string), ""); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	_, cfgs := do(t, "GET", ts.URL+"/api/workspaces/"+id+"/agent-configs", "")
	if n := len(cfgs["configs"].([]any)); n != 1 {
		t.Fatalf("configs: %d", n)
	}
}
