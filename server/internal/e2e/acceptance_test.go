// Package e2e runs the whole control plane in-process and walks the Phase 1
// acceptance criteria (PRD Section 15) through the real HTTP API. Provisioning
// uses the fake runtime driver; connection probes and model calls use fakes
// that accept one known secret. Every response body, event and audit record is
// scanned at the end for secret values.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/app"
	"github.com/nexio-developer-group/nexmesh/server/internal/config"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/modelprovider"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const (
	dbPassword  = "Tr0ub4dor&3-mongo"
	dbPassword2 = "c0rrect-h0rse-staple-2"
	apiKeyGood  = "sk-ant-ACCEPTANCE-0123456789"
)

type harness struct {
	t      *testing.T
	ts     *httptest.Server
	app    *app.App
	drv    *runtime.Fake
	mu     sync.Mutex
	log    strings.Builder // every API response body except the parse endpoint
	goodPW string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, goodPW: dbPassword}
	cfg := config.Config{AuthDisabled: true, Store: "memory", Driver: "fake", CatalogDir: "../../../catalog", MasterKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200, PublicURL: "http://localhost"}
	h.drv = runtime.NewFake(runtime.Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200, StorageShrink: false, GPUReason: "this execution backend has no GPU drivers installed."})
	h.drv.Delay = 40 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := app.New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), app.Options{
		Driver: h.drv,
		Probe: func(_ context.Context, p probe.Params, report probe.Report) *domain.TestResult {
			r := &domain.TestResult{At: time.Now().UTC(), LatencyMS: 41, ServerVersion: "7.0.14"}
			for _, s := range probe.StageOrder {
				r.Stages = append(r.Stages, domain.StageResult{Stage: s, Status: "passed"})
			}
			if p.Fields["password"] != h.goodPW {
				r.FailedStage, r.Message = probe.StageAuth, "Host reachable but authentication failed. Check the username and password."
				for i := range r.Stages {
					if r.Stages[i].Stage == probe.StageAuth {
						r.Stages[i].Status = "failed"
					}
				}
				return r
			}
			r.Passed = true
			return r
		},
		ModelRun: func(_ context.Context, p modelprovider.Params, _ func(domain.StageResult)) (*domain.TestResult, []modelprovider.Model) {
			r := &domain.TestResult{At: time.Now().UTC(), LatencyMS: 640}
			if p.Config.APIKey != apiKeyGood {
				r.FailedStage, r.Message = modelprovider.StageAuth, "The key was rejected (invalid or revoked)."
				return r, nil
			}
			r.Passed = true
			return r, []modelprovider.Model{{ID: "claude-sonnet-4-5"}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.app = a
	a.Start(ctx)
	h.ts = httptest.NewServer(a.Handler)
	t.Cleanup(h.ts.Close)
	return h
}

// call performs a request and records the body (unless it is the parse
// endpoint, which returns the pasted password to the caller by design).
func (h *harness) call(method, path, body string) (int, map[string]any) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if !strings.Contains(path, "/connections/parse") {
		h.mu.Lock()
		h.log.WriteString(method + " " + path + " " + string(raw) + "\n")
		h.mu.Unlock()
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m
}

func (h *harness) must(code int, method, path, body string) map[string]any {
	h.t.Helper()
	got, m := h.call(method, path, body)
	if got != code {
		h.t.Fatalf("%s %s: status %d, want %d: %v", method, path, got, code, m)
	}
	return m
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (h *harness) eventually(what string, cond func() bool) {
	h.t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) workspace(id string) map[string]any {
	return h.must(200, "GET", "/api/workspaces/"+id, "")
}

func (h *harness) card(ws, id string) map[string]any {
	return h.must(200, "GET", "/api/workspaces/"+ws+"/components/"+id, "")
}

func (h *harness) waitCard(ws, id, state string) map[string]any {
	h.t.Helper()
	var c map[string]any
	h.eventually(fmt.Sprintf("card %s to be %s", id, state), func() bool { c = h.card(ws, id); return c["observedState"] == state })
	return c
}

func TestPhase1Acceptance(t *testing.T) {
	h := newHarness(t)

	// §15: "The sidebar shows the defined technology categories and catalog entries."
	cat := h.must(200, "GET", "/api/catalog", "")
	// Six categories and 18 entries are the PRD Section 8 catalog; Phase 3 adds the
	// External services category with one connect-only entry (F16).
	if got := len(cat["categories"].([]any)); got != 7 {
		t.Fatalf("categories: %d", got)
	}
	comps := cat["components"].([]any)
	if len(comps) != 19 {
		t.Fatalf("catalog entries: %d", len(comps))
	}
	connectable := map[string]bool{}
	for _, c := range comps {
		cm := c.(map[string]any)
		for _, m := range cm["supportedModes"].([]any) {
			if m == "connect" {
				connectable[cm["id"].(string)] = true
			}
		}
	}
	for _, id := range []string{"mongodb", "mysql", "redis", "kafka", "rabbitmq"} {
		if !connectable[id] {
			t.Errorf("%s must support Connect", id)
		}
	}
	if !connectable["externalapi"] {
		t.Errorf("the External API card (F16) supports Connect")
	}
	if len(connectable) != 6 {
		t.Errorf("only service-type components support Connect: %v", connectable)
	}

	// §15: "The system rejects unsupported or unavailable resource combinations with a clear error."
	code, m := h.call("POST", "/api/workspaces", `{"name":"toobig","cpu":99,"ramGb":8,"storageGb":40}`)
	if code != 422 || errCode(m) != "cpu_out_of_range" {
		t.Fatalf("limit error: %d %v", code, m)
	}
	code, m = h.call("POST", "/api/workspaces", `{"name":"gpu","cpu":2,"ramGb":4,"storageGb":20,"gpu":true}`)
	if code != 422 || errCode(m) != "gpu_unavailable" {
		t.Fatalf("gpu: %d %v", code, m)
	}

	// §15: "A user can create a workspace using Ubuntu 24.04 LTS"; "reaches the canvas only after provisioning reports readiness."
	ws := h.must(201, "POST", "/api/workspaces", `{"name":"orders-api","description":"Stripe webhooks","cpu":4,"ramGb":8,"storageGb":40}`)
	id := ws["id"].(string)
	if ws["os"] != "ubuntu-24.04" || ws["status"] != "allocating" {
		t.Fatalf("%v", ws)
	}
	seen := map[string]bool{}
	h.eventually("workspace ready", func() bool {
		w := h.workspace(id)
		seen[w["status"].(string)] = true
		return w["status"] == "ready"
	})
	if !seen["allocating"] && !seen["preparing"] && !seen["starting"] {
		t.Error("provisioning states were never observable")
	}
	if code, m := h.call("POST", "/api/workspaces/"+id+"/components", `{"catalogId":"redis"}`); code != 201 {
		t.Fatalf("add card: %d %v", code, m)
	}
	// (the Redis card above is used for the install flow below)

	list := h.must(200, "GET", "/api/workspaces/"+id+"/components", "")["components"].([]any)
	redis := list[0].(map[string]any)["id"].(string)
	add := func(catalogID string) string {
		return h.must(201, "POST", "/api/workspaces/"+id+"/components", `{"catalogId":"`+catalogID+`","x":40,"y":60}`)["id"].(string)
	}
	mongo, nest := add("mongodb"), add("nestjs")

	// §15: card state mirrors real configuration. Dropping a card creates a Draft; nothing installs.
	if c := h.card(id, mongo); c["observedState"] != "draft" || c["mode"] != "" {
		t.Fatalf("%v", c)
	}

	// ---- Install mode (§10, §15)
	base := "/api/workspaces/" + id + "/components/"
	if code, m := h.call("POST", base+redis+"/install", `{"version":"7.4.1","start":true}`); code != 422 || errCode(m) != "confirmation_required" {
		t.Fatalf("install without confirmation: %d %v", code, m)
	}
	// "NestJS requires Node.js" before the runtime exists.
	if code, m := h.call("POST", base+nest+"/install", `{"version":"10.4.7","confirm":true}`); code != 422 || errCode(m) != "missing_dependency" {
		t.Fatalf("dependency check: %d %v", code, m)
	}
	// "Python, Node.js, and Go can be installed through the separate global runtime workflow."
	h.must(202, "POST", "/api/workspaces/"+id+"/runtimes/nodejs/install", `{"version":"22.11.0","activate":true}`)
	h.eventually("node runtime recorded", func() bool {
		r := h.workspace(id)["runtimes"].(map[string]any)
		n, ok := r["nodejs"].(map[string]any)
		return ok && n["active"] == "22.11.0"
	})
	// "The selected version and settings are persisted before an installation operation begins."
	h.must(202, "POST", base+redis+"/install", `{"version":"7.4.1","start":true,"confirm":true,"settings":{"port":6379}}`)
	c := h.card(id, redis)
	if c["version"] != "7.4.1" || c["mode"] != "installed" || c["settings"].(map[string]any)["port"] != float64(6379) {
		t.Fatalf("desired state not persisted: %v", c)
	}
	running := h.waitCard(id, redis, "running")
	if running["desiredState"] != "running" {
		t.Fatalf("%v", running)
	}
	if o, _ := h.drv.InspectService(context.Background(), id, "redis"); !o.Running {
		t.Fatal("card says running but the service container is not")
	}
	h.must(202, "POST", base+nest+"/install", `{"version":"10.4.7","confirm":true}`)
	h.waitCard(id, nest, "ready")
	// A failed start is reported with its reason and can be retried.
	kafka := add("kafka")
	h.drv.BusyPorts[9092] = true
	h.must(202, "POST", base+kafka+"/install", `{"version":"3.8.1","start":true,"confirm":true}`)
	failed := h.waitCard(id, kafka, "failed")
	if failed["message"] != "start failed: port 9092 already in use" {
		t.Fatalf("%v", failed["message"])
	}
	delete(h.drv.BusyPorts, 9092)
	h.must(202, "POST", base+kafka+"/retry", "")
	h.waitCard(id, kafka, "running")

	// ---- Connect mode (§11, §15)
	pc, parsed := h.call("POST", "/api/connections/parse", `{"catalogId":"mongodb","uri":"mongodb+srv://orders_app:`+strings.ReplaceAll(dbPassword, "&", "%26")+`@orders.k3x9p.mongodb.net/orders"}`)
	if pc != 200 || parsed["fields"].(map[string]any)["host"] != "orders.k3x9p.mongodb.net" {
		t.Fatalf("parse: %d %v", pc, parsed)
	}
	conn := func(pw string) string {
		return `{"connName":"prod-orders-db","environment":"production","fields":{"host":"orders.k3x9p.mongodb.net","port":27017,"database":"orders","username":"orders_app","password":"` + pw + `"}}`
	}
	// "Add to Workspace is disabled until a test passes for the current values."
	if code, m := h.call("POST", base+mongo+"/connection", conn(dbPassword)); code != 422 || errCode(m) != "test_required" {
		t.Fatalf("add without test: %d %v", code, m)
	}
	// Test Connection reports a specific failure category.
	_, res := h.call("POST", base+mongo+"/connection/test", conn("wrong-password-1"))
	if res["passed"] == true || res["failedStage"] != "authentication" || !strings.Contains(res["message"].(string), "authentication failed") {
		t.Fatalf("%v", res)
	}
	time.Sleep(1100 * time.Millisecond) // test rate limit
	_, res = h.call("POST", base+mongo+"/connection/test", conn(dbPassword))
	if res["passed"] != true || len(res["stages"].([]any)) != 7 {
		t.Fatalf("%v", res)
	}
	// "any edit invalidates the result"
	edited := strings.Replace(conn(dbPassword), `"database":"orders"`, `"database":"orders2"`, 1)
	if code, m := h.call("POST", base+mongo+"/connection", edited); code != 422 || errCode(m) != "test_required" {
		t.Fatalf("edit must invalidate the test: %d %v", code, m)
	}
	added := h.must(200, "POST", base+mongo+"/connection", conn(dbPassword))
	if added["mode"] != "connected" || added["observedState"] != "connected" || added["environment"] != "production" {
		t.Fatalf("%v", added)
	}
	exposed := added["exposedAs"].([]any)
	if exposed[0] != "PROD_ORDERS_DB_URI" {
		t.Fatalf("%v", exposed)
	}
	// "its connection details are available to workspace projects": through the agent, with secrets.
	env, _ := h.app.Components.ConnectionEnv(context.Background(), id)
	if env["PROD_ORDERS_DB_PASSWORD"] != dbPassword || !strings.Contains(env["PROD_ORDERS_DB_URI"], "orders.k3x9p.mongodb.net") {
		t.Fatalf("%v", env)
	}
	// Re-test records health; rotating needs a passing test; credentials changed at the provider degrade the card.
	time.Sleep(1100 * time.Millisecond)
	h.goodPW = dbPassword2
	h.app.Components.HealthOnce(context.Background())
	if c := h.card(id, mongo); c["observedState"] != "degraded" {
		t.Fatalf("expired credentials must degrade the card: %v", c["observedState"])
	}
	time.Sleep(1100 * time.Millisecond)
	h.must(200, "POST", base+mongo+"/connection/rotate", `{"secrets":{"password":"`+dbPassword2+`"}}`)
	if c := h.card(id, mongo); c["observedState"] != "connected" {
		t.Fatalf("%v", c["observedState"])
	}
	env, _ = h.app.Components.ConnectionEnv(context.Background(), id)
	if env["PROD_ORDERS_DB_PASSWORD"] != dbPassword2 {
		t.Fatal("rotated secret not used")
	}

	// ---- Workspace Settings (§12, §15)
	// "The operating system ... cannot be changed."
	code, m = h.call("PATCH", "/api/workspaces/"+id+"/settings", `{"os":"debian-12"}`)
	if code != 422 || errCode(m) != "os_locked" {
		t.Fatalf("%d %v", code, m)
	}
	h.must(200, "PATCH", "/api/workspaces/"+id+"/settings", `{"cpu":6,"description":"Stripe webhooks and jobs"}`)
	if w := h.workspace(id); w["cpu"] != float64(6) || w["os"] != "ubuntu-24.04" {
		t.Fatalf("%v", w)
	}
	// Restart-requiring changes need confirmation; the preview says so.
	pv := h.must(200, "POST", "/api/workspaces/"+id+"/settings", `{"storageGb":80}`)
	if pv["restartRequired"] != true {
		t.Fatalf("%v", pv)
	}
	code, m = h.call("PATCH", "/api/workspaces/"+id+"/settings", `{"storageGb":80}`)
	if code != 422 || errCode(m) != "restart_confirmation_required" {
		t.Fatalf("%d %v", code, m)
	}
	h.must(200, "PATCH", "/api/workspaces/"+id+"/settings", `{"storageGb":80,"confirmRestart":true}`)
	h.eventually("restart to finish", func() bool { w := h.workspace(id); return w["status"] == "ready" && w["restartRequired"] == false })
	// "Reducing storage below current usage is rejected with a clear error."
	code, m = h.call("PATCH", "/api/workspaces/"+id+"/settings", `{"storageGb":20,"confirmRestart":true}`)
	if code != 422 || !strings.Contains(m["error"].(map[string]any)["message"].(string), "storage") {
		t.Fatalf("%d %v", code, m)
	}

	// ---- Code Agent (§6.4, §12, §15)
	agent := func(key string) string {
		return `{"name":"Claude for backend work","provider":"anthropic","modelId":"claude-sonnet-4-5","apiKey":"` + key + `"}`
	}
	if code, m := h.call("POST", "/api/workspaces/"+id+"/agent-configs", agent(apiKeyGood)); code != 422 || errCode(m) != "test_required" {
		t.Fatalf("save without test: %d %v", code, m)
	}
	_, tr := h.call("POST", "/api/agent/test", agent(apiKeyGood))
	if tr["passed"] != true {
		t.Fatalf("%v", tr)
	}
	saved := h.must(201, "POST", "/api/workspaces/"+id+"/agent-configs", agent(apiKeyGood))
	if saved["hasKey"] != true || saved["isDefault"] != true {
		t.Fatalf("%v", saved)
	}
	// Advanced Setup at creation: absent never blocks, started-but-untested does.
	h.must(201, "POST", "/api/workspaces", `{"name":"plain","cpu":1,"ramGb":1,"storageGb":5}`)
	code, m = h.call("POST", "/api/workspaces", `{"name":"gated","cpu":1,"ramGb":1,"storageGb":5,"agent":`+agent(apiKeyGood)+`}`)
	if code != 201 { // the same values were just tested by this user, so creation is allowed
		t.Fatalf("%d %v", code, m)
	}
	code, m = h.call("POST", "/api/workspaces", `{"name":"gated2","cpu":1,"ramGb":1,"storageGb":5,"agent":`+agent("sk-ant-never-tested-0000")+`}`)
	if code != 422 || errCode(m) != "test_required" {
		t.Fatalf("untested agent must block: %d %v", code, m)
	}

	// ---- Disconnect keeps remote data (nothing to delete) and removes secrets (§11.6, §15)
	secretRef := ""
	if recs, err := h.app.Store.ListComponents(context.Background(), id); err == nil {
		for _, c := range recs {
			if c.ID == mongo {
				secretRef = c.SecretRef
			}
		}
	}
	if secretRef == "" {
		t.Fatal("connected component should reference stored secrets")
	}
	h.must(204, "DELETE", base+mongo+"/connection", "")
	if _, err := h.app.Store.GetSecret(context.Background(), secretRef); err == nil {
		t.Fatal("disconnect must delete stored secrets")
	}
	if c := h.card(id, mongo); c["mode"] != "" || c["observedState"] != "disconnected" {
		t.Fatalf("%v", c)
	}

	// ---- Audit: every state change is recorded with an actor type; no secrets (§18.4 rules 3, 4)
	audit := h.must(200, "GET", "/api/workspaces/"+id+"/audit", "")
	actions := map[string]bool{}
	for _, e := range audit["events"].([]any) {
		em := e.(map[string]any)
		actions[em["action"].(string)] = true
		if at := em["actorType"]; at != domain.ActorUser && at != domain.ActorSystem {
			t.Errorf("event %v has actor %v", em["action"], at)
		}
	}
	for _, want := range []string{
		"workspace.create", "component.add", "component.install", "runtime.install", "connection.test", "connection.add",
		"connection.rotate_credentials", "connection.health_changed", "connection.disconnect", "workspace.settings", "agent_config.create",
	} {
		if !actions[want] {
			t.Errorf("audit is missing %q (have %v)", want, actions)
		}
	}
	rawAudit, _ := json.Marshal(audit)

	// No response, event or audit record contains a secret value.
	all := h.log.String() + string(rawAudit)
	for _, secret := range []string{dbPassword, dbPassword2, apiKeyGood, "sk-ant-never-tested-0000"} {
		if strings.Contains(all, secret) {
			t.Errorf("a secret value appears in API output: %q", secret)
		}
	}
	// And none reached the store unencrypted.
	for _, sec := range secretRecords(t, h.app.Store, id) {
		for _, s := range []string{dbPassword, dbPassword2, apiKeyGood} {
			if bytes.Contains(sec, []byte(s)) {
				t.Errorf("a secret is stored in plaintext")
			}
		}
	}

	// ---- Deleting a workspace requires typing its name and removes its secrets (§P2)
	if code, m := h.call("DELETE", "/api/workspaces/"+id+"?confirmName=nope", ""); code != 422 {
		t.Fatalf("%d %v", code, m)
	}
	h.must(204, "DELETE", "/api/workspaces/"+id+"?confirmName=orders-api", "")
	cfgs, _ := h.app.Store.ListAgentConfigs(context.Background(), id)
	if len(cfgs) != 0 {
		t.Fatal("agent configurations must go with the workspace")
	}
	if o, _ := h.drv.InspectWorkspace(context.Background(), id); o.Exists {
		t.Fatal("workspace container still exists")
	}
}

func secretRecords(t *testing.T, st store.Store, _ string) [][]byte {
	t.Helper()
	var out [][]byte
	// The memory store exposes secrets by id only; walk the ids the product uses.
	ws, _ := st.ListWorkspaces(context.Background(), "")
	for _, w := range ws {
		comps, _ := st.ListComponents(context.Background(), w.ID)
		for _, c := range comps {
			for _, id := range []string{"connsec_" + c.ID, "connpending_" + c.ID} {
				if r, err := st.GetSecret(context.Background(), id); err == nil {
					out = append(out, r.Ciphertext, r.WrappedKey)
				}
			}
		}
		cfgs, _ := st.ListAgentConfigs(context.Background(), w.ID)
		for _, c := range cfgs {
			if r, err := st.GetSecret(context.Background(), "agentsec_"+c.ID); err == nil {
				out = append(out, r.Ciphertext, r.WrappedKey)
			}
		}
	}
	return out
}

// The browser receives live updates over SSE; those must not leak secrets either.
func TestEventStreamCarriesNoSecrets(t *testing.T) {
	h := newHarness(t)
	ws := h.must(201, "POST", "/api/workspaces", `{"name":"sse","cpu":1,"ramGb":2,"storageGb":10}`)
	id := ws["id"].(string)
	h.eventually("ready", func() bool { return h.workspace(id)["status"] == "ready" })

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.ts.URL+"/api/workspaces/"+id+"/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var mu sync.Mutex
	var stream strings.Builder
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			mu.Lock()
			stream.WriteString(sc.Text() + "\n")
			mu.Unlock()
		}
	}()
	time.Sleep(200 * time.Millisecond)
	mongo := h.must(201, "POST", "/api/workspaces/"+id+"/components", `{"catalogId":"mongodb"}`)["id"].(string)
	body := `{"connName":"db","environment":"development","fields":{"host":"h.example.com","port":27017,"database":"d","username":"u","password":"` + dbPassword + `"}}`
	h.call("POST", "/api/workspaces/"+id+"/components/"+mongo+"/connection/test", body)
	h.must(200, "POST", "/api/workspaces/"+id+"/components/"+mongo+"/connection", body)
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	out := stream.String()
	mu.Unlock()
	if !strings.Contains(out, "event: component") || !strings.Contains(out, `"observedState":"connected"`) {
		t.Fatalf("expected live component events, got:\n%s", out)
	}
	if strings.Contains(out, dbPassword) || strings.Contains(out, "secretRef") || strings.Contains(out, "connsec_") {
		t.Fatalf("the event stream leaks secrets or internal references:\n%s", out)
	}
}
