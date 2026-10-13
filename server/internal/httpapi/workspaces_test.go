package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

func testServer(t *testing.T) *httptest.Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200})
	eng := ops.NewEngine(st, bus, log, 2)
	svc := workspace.NewService(st, drv, eng, bus, audit.New(st, log), log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eng.Start(ctx)
	api := New(log)
	api.RegisterWorkspaces(WorkspaceDeps{Svc: svc, Driver: drv, Store: st, Bus: bus})
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, body string) (*http.Response, map[string]any) {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res, m
}

func TestWorkspaceAPIFlow(t *testing.T) {
	ts := testServer(t)

	res, host := do(t, "GET", ts.URL+"/api/host", "")
	if res.StatusCode != 200 || host["maxCpu"].(float64) != 12 {
		t.Fatalf("host: %v", host)
	}

	res, body := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"big","cpu":13,"ramGb":8,"storageGb":40}`)
	e := body["error"].(map[string]any)
	if res.StatusCode != 422 || e["code"] != "cpu_out_of_range" || e["field"] != "cpu" {
		t.Fatalf("limit error: %d %v", res.StatusCode, body)
	}

	res, ws := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"orders-api","description":"d","cpu":4,"ramGb":8,"storageGb":40}`)
	if res.StatusCode != 201 || ws["os"] != "ubuntu-24.04" {
		t.Fatalf("create: %d %v", res.StatusCode, ws)
	}
	id := ws["id"].(string)

	res, _ = do(t, "POST", ts.URL+"/api/workspaces", `{"name":"orders-api","cpu":1,"ramGb":1,"storageGb":5}`)
	if res.StatusCode != 409 {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}
	res, av := do(t, "GET", ts.URL+"/api/workspaces/name-available?name=orders-api", "")
	if av["available"] != false {
		t.Fatalf("name-available: %v", av)
	}

	var status string
	for i := 0; i < 200 && status != "ready"; i++ {
		_, got := do(t, "GET", ts.URL+"/api/workspaces/"+id, "")
		status, _ = got["status"].(string)
		time.Sleep(10 * time.Millisecond)
	}
	if status != "ready" {
		t.Fatalf("never became ready: %s", status)
	}

	res, _ = do(t, "DELETE", ts.URL+"/api/workspaces/"+id+"?confirmName=nope", "")
	if res.StatusCode != 422 {
		t.Fatalf("delete w/o name: %d", res.StatusCode)
	}
	res, _ = do(t, "GET", ts.URL+"/api/workspaces/"+id, "")
	req, _ := http.NewRequest("GET", ts.URL+"/api/workspaces/"+id, nil)
	req.Header.Set("X-Nexmesh-User", "someone-else")
	other, _ := http.DefaultClient.Do(req)
	if other.StatusCode != 404 {
		t.Fatalf("cross-user read: %d", other.StatusCode)
	}
	res, _ = do(t, "DELETE", ts.URL+"/api/workspaces/"+id+"?confirmName=orders-api", "")
	if res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	ts := testServer(t)
	_, ws := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"sse","cpu":1,"ramGb":1,"storageGb":5}`)
	id := ws["id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/workspaces/"+id+"/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	sc := bufio.NewScanner(res.Body)
	sawReady := false
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") && strings.Contains(sc.Text(), `"status":"ready"`) {
			sawReady = true
			break
		}
	}
	if !sawReady {
		t.Fatal("did not receive ready event over SSE")
	}
}

func TestComponentsAPIAndSummary(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200})
	eng := ops.NewEngine(st, bus, log, 2)
	aud := audit.New(st, log)
	svc := workspace.NewService(st, drv, eng, bus, aud, log)
	cat, _ := catalog.Load("../../../catalog")
	comp := component.NewService(st, cat, bus, aud)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eng.Start(ctx)
	api := New(log)
	api.RegisterWorkspaces(WorkspaceDeps{Svc: svc, Driver: drv, Store: st, Bus: bus, Summary: comp})
	api.RegisterComponents(comp)
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)

	_, ws := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"cards","cpu":1,"ramGb":1,"storageGb":5}`)
	id := ws["id"].(string)
	for i := 0; i < 200; i++ {
		_, g := do(t, "GET", ts.URL+"/api/workspaces/"+id, "")
		if g["status"] == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	res, c := do(t, "POST", ts.URL+"/api/workspaces/"+id+"/components", `{"catalogId":"mongodb","x":40,"y":60}`)
	if res.StatusCode != 201 || c["observedState"] != "draft" {
		t.Fatalf("add: %d %v", res.StatusCode, c)
	}
	res, bad := do(t, "POST", ts.URL+"/api/workspaces/"+id+"/components", `{"catalogId":"cobol"}`)
	if res.StatusCode != 422 {
		t.Fatalf("unknown: %d %v", res.StatusCode, bad)
	}
	_, list := do(t, "GET", ts.URL+"/api/workspaces/"+id+"/components", "")
	if len(list["components"].([]any)) != 1 {
		t.Fatalf("list: %v", list)
	}
	_, g := do(t, "GET", ts.URL+"/api/workspaces/"+id, "")
	if g["summary"] != "1 component" {
		t.Fatalf("summary: %v", g["summary"])
	}
	res, _ = do(t, "DELETE", ts.URL+"/api/workspaces/"+id+"/components/"+c["id"].(string), "")
	if res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
}

func TestParseConnectionStringEndpoint(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	cat, _ := catalog.Load("../../../catalog")
	comp := component.NewService(st, cat, bus, audit.New(st, log))
	api := New(log)
	api.RegisterConnections(comp)
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	res, body := do(t, "POST", ts.URL+"/api/connections/parse", `{"catalogId":"mongodb","uri":"mongodb+srv://orders_app:p%40ss@orders.k3x9p.mongodb.net/orders"}`)
	f := body["fields"].(map[string]any)
	if res.StatusCode != 200 || f["host"] != "orders.k3x9p.mongodb.net" || f["password"] != "p@ss" || f["srv"] != true {
		t.Fatalf("%d %v", res.StatusCode, body)
	}
	res, body = do(t, "POST", ts.URL+"/api/connections/parse", `{"catalogId":"mongodb","uri":"mysql://u:SuperSecret1@h/d"}`)
	if res.StatusCode != 422 {
		t.Fatalf("%d", res.StatusCode)
	}
	if msg, _ := body["error"].(map[string]any)["message"].(string); strings.Contains(msg, "SuperSecret1") {
		t.Fatalf("parse error leaks the password: %s", msg)
	}
}

func TestSettingsAPILocksOperatingSystem(t *testing.T) {
	ts := testServer(t)
	_, ws := do(t, "POST", ts.URL+"/api/workspaces", `{"name":"locked","cpu":2,"ramGb":4,"storageGb":20}`)
	id := ws["id"].(string)
	for i := 0; i < 200; i++ {
		_, g := do(t, "GET", ts.URL+"/api/workspaces/"+id, "")
		if g["status"] == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, method := range []string{"PATCH", "POST"} {
		res, body := do(t, method, ts.URL+"/api/workspaces/"+id+"/settings", `{"os":"debian-12","cpu":3}`)
		e, _ := body["error"].(map[string]any)
		if res.StatusCode != 422 || e["code"] != "os_locked" || e["field"] != "os" {
			t.Fatalf("%s: %d %v", method, res.StatusCode, body)
		}
	}
	_, g := do(t, "GET", ts.URL+"/api/workspaces/"+id, "")
	if g["os"] != "ubuntu-24.04" || g["cpu"].(float64) != 2 {
		t.Fatalf("workspace changed by a rejected request: %v", g)
	}
	// A valid change works and the response keeps the OS.
	res, body := do(t, "PATCH", ts.URL+"/api/workspaces/"+id+"/settings", `{"cpu":3,"description":"hi"}`)
	if res.StatusCode != 200 || body["cpu"].(float64) != 3 || body["os"] != "ubuntu-24.04" {
		t.Fatalf("%d %v", res.StatusCode, body)
	}
	res, body = do(t, "POST", ts.URL+"/api/workspaces/"+id+"/settings", `{"storageGb":50}`)
	if res.StatusCode != 200 || body["restartRequired"] != true {
		t.Fatalf("%d %v", res.StatusCode, body)
	}
}
