package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (h *harness) raw(method, path, body string) (int, string) {
	h.t.Helper()
	code, m := h.call(method, path, body)
	b, _ := json.Marshal(m)
	return code, string(b)
}

// Export, drift and import through the real API: the file is secret-free, the drift report lists
// every difference, and an import rebuilds an equivalent workspace after secrets are re-supplied.
func TestWorkspaceDefinitionExportDriftImport(t *testing.T) {
	h := newHarness(t)
	ws := h.must(201, "POST", "/api/workspaces", `{"name":"src-app","description":"Orders","cpu":2,"ramGb":8,"storageGb":40}`)
	id := ws["id"].(string)
	h.eventually("ready", func() bool { return h.workspace(id)["status"] == "ready" })
	base := "/api/workspaces/" + id + "/components/"
	add := func(cat string, x, y int) string {
		return h.must(201, "POST", "/api/workspaces/"+id+"/components", `{"catalogId":"`+cat+`","x":`+itoa(x)+`,"y":`+itoa(y)+`}`)["id"].(string)
	}
	redis, mongo := add("redis", 40, 40), add("mongodb", 290, 40)
	h.must(202, "POST", base+redis+"/install", `{"version":"7.4.1","start":true,"confirm":true,"settings":{"port":6379}}`)
	h.waitCard(id, redis, "running")
	conn := `{"connName":"prod-orders-db","environment":"production","fields":{"host":"orders.k3x9p.mongodb.net","port":27017,"database":"orders","username":"orders_app","password":"` + dbPassword + `"}}`
	h.must(200, "POST", base+mongo+"/connection/test", conn)
	h.must(200, "POST", base+mongo+"/connection", conn)
	h.must(200, "PUT", "/api/workspaces/"+id+"/variables/APP_MODE", `{"value":"development"}`)
	h.must(200, "PUT", "/api/workspaces/"+id+"/variables/STRIPE_KEY", `{"secret":true,"value":"sk_live_VERY_SECRET_VALUE"}`)
	h.must(204, "PUT", "/api/workspaces/"+id+"/ports/3000", `{"label":"web"}`)

	// ---- Export: complete and secret-free (F12 acceptance).
	code, yamlText := h.raw("GET", "/api/workspaces/"+id+"/definition", "")
	_ = code
	req := h.rawGet("/api/workspaces/" + id + "/definition")
	for _, leak := range []string{dbPassword, "VERY_SECRET", "sk_live"} {
		if strings.Contains(req, leak) {
			t.Fatalf("definition leaks %q:\n%s", leak, req)
		}
	}
	for _, want := range []string{"kind: NexmeshWorkspace", "schemaVersion: 1", "name: src-app", "catalog: redis", "version: 7.4.1", "port: 6379", "name: prod-orders-db",
		"host: orders.k3x9p.mongodb.net", "secretFields:", "- password", "name: STRIPE_KEY", "secret: true", "value: development", "port: 3000", "label: web"} {
		if !strings.Contains(req, want) {
			t.Errorf("definition missing %q:\n%s", want, req)
		}
	}
	_ = yamlText

	post := func(path string, v any) (int, map[string]any) {
		b, _ := json.Marshal(v)
		return h.call("POST", path, string(b))
	}

	// ---- Drift: a fresh export is in sync; every change shows up.
	if c, rep := post("/api/workspaces/"+id+"/drift", map[string]any{"definition": req}); c != 200 || rep["inSync"] != true {
		t.Fatalf("fresh definition must be in sync: %d %v", c, rep)
	}
	h.must(200, "PUT", "/api/workspaces/"+id+"/variables/APP_MODE", `{"value":"production"}`)
	h.must(200, "PUT", "/api/workspaces/"+id+"/variables/EXTRA_FLAG", `{"value":"1"}`)
	h.must(204, "DELETE", "/api/workspaces/"+id+"/ports/3000", "")
	h.must(202, "POST", base+redis+"/stop", "")
	h.waitCard(id, redis, "stopped")
	_, _ = h.call("PATCH", "/api/workspaces/"+id+"/settings", `{"cpu":4}`)
	_, rep := post("/api/workspaces/"+id+"/drift", map[string]any{"definition": req})
	if rep["inSync"] != false {
		t.Fatalf("drift not detected: %v", rep)
	}
	diffs, _ := json.Marshal(rep["differences"])
	type dd struct{ Category, Subject, Field, Expected, Actual, Kind string }
	var found []dd
	_ = json.Unmarshal(diffs, &found)
	has := func(want dd) bool {
		for _, f := range found {
			if (want.Category == "" || f.Category == want.Category) && (want.Subject == "" || f.Subject == want.Subject) && (want.Field == "" || f.Field == want.Field) &&
				(want.Expected == "" || f.Expected == want.Expected) && (want.Actual == "" || f.Actual == want.Actual) && (want.Kind == "" || f.Kind == want.Kind) {
				return true
			}
		}
		return false
	}
	for _, want := range []dd{
		{Category: "workspace", Subject: "resources", Field: "cpu", Expected: "2", Actual: "4", Kind: "changed"},
		{Category: "variable", Subject: "APP_MODE", Field: "value", Expected: "development", Actual: "production", Kind: "changed"},
		{Category: "variable", Subject: "EXTRA_FLAG", Kind: "extra"},
		{Category: "port", Subject: "3000", Kind: "missing"},
		{Category: "component", Subject: "redis", Field: "desired state", Expected: "running", Actual: "stopped", Kind: "changed"},
	} {
		if !has(want) {
			t.Errorf("drift report missing %+v:\n%s", want, diffs)
		}
	}
	if strings.Contains(string(diffs), dbPassword) || strings.Contains(string(diffs), "VERY_SECRET") {
		t.Fatal("drift report leaked a secret")
	}

	// ---- Import validation.
	bad := strings.Replace(req, "secretFields:", "secretFields:", 1)
	withPw := strings.Replace(bad, "host: orders.k3x9p.mongodb.net", "host: orders.k3x9p.mongodb.net\n      password: hunter2-hunter2", 1)
	c, m := post("/api/workspaces/import", map[string]any{"definition": withPw, "name": "copy-a", "dryRun": true})
	if c != 422 || errCode(m) != "invalid_definition" || !strings.Contains(m["error"].(map[string]any)["issues"].([]any)[0].(map[string]any)["message"].(string), "credential") {
		t.Fatalf("embedded credential must be rejected: %d %v", c, m)
	}
	for name, doc := range map[string]string{
		"unknown tech":   strings.Replace(req, "catalog: redis", "catalog: cobol", 1),
		"bad version":    strings.Replace(req, "version: 7.4.1", "version: 0.0.1", 1),
		"future schema":  strings.Replace(req, "schemaVersion: 1", "schemaVersion: 2", 1),
		"wrong kind":     strings.Replace(req, "kind: NexmeshWorkspace", "kind: Other", 1),
		"unknown field":  req + "\nsurprise: true\n",
		"secret var val": strings.Replace(req, "name: STRIPE_KEY", "name: STRIPE_KEY\n    value: sk_live_x", 1),
	} {
		if c, m := post("/api/workspaces/import", map[string]any{"definition": doc, "name": "copy-b", "dryRun": true}); c != 422 {
			t.Errorf("%s accepted: %d %v", name, c, m)
		}
	}
	if c, m := post("/api/workspaces/import", map[string]any{"definition": req, "dryRun": true}); c != 409 || errCode(m) != "name_taken" {
		t.Fatalf("importing over an existing name: %d %v", c, m)
	}
	c, m = post("/api/workspaces/import", map[string]any{"definition": req, "name": "copy-a", "dryRun": true})
	if c != 200 || m["dryRun"] != true || m["workspace"] != nil {
		t.Fatalf("dry run: %d %v", c, m)
	}
	todos, _ := json.Marshal(m["todos"])
	for _, want := range []string{`"kind":"credential"`, "password", `"kind":"secret_variable"`, "STRIPE_KEY", `"kind":"confirm_install"`} {
		if !strings.Contains(string(todos), want) {
			t.Errorf("todo list missing %s: %s", want, todos)
		}
	}
	if ws, _ := h.call("GET", "/api/workspaces/name-available?name=copy-a", ""); ws != 200 {
		t.Fatal("name check")
	}

	// ---- Import for real, with auto-install.
	c, m = post("/api/workspaces/import", map[string]any{"definition": req, "name": "copy-a", "autoInstall": true})
	if c != 201 {
		t.Fatalf("import: %d %v", c, m)
	}
	nid := m["workspace"].(map[string]any)["id"].(string)
	h.eventually("copy ready", func() bool { return h.workspace(nid)["status"] == "ready" })
	var cards []any
	h.eventually("cards applied", func() bool {
		cards = h.must(200, "GET", "/api/workspaces/"+nid+"/components", "")["components"].([]any)
		return len(cards) == 2
	})
	byCat := map[string]map[string]any{}
	for _, cc := range cards {
		cm := cc.(map[string]any)
		byCat[cm["catalogId"].(string)] = cm
	}
	h.waitCard(nid, byCat["redis"]["id"].(string), "running") // installed because autoInstall was chosen
	mg := byCat["mongodb"]
	if mg["observedState"] != "draft" || mg["connName"] != "prod-orders-db" || mg["environment"] != "production" ||
		mg["connection"].(map[string]any)["host"] != "orders.k3x9p.mongodb.net" || mg["suggestedMode"] != "connect" {
		t.Fatalf("connection card must be a Draft prefilled without credentials: %v", mg)
	}
	vars, _ := h.raw2("GET", "/api/workspaces/"+nid+"/variables")
	if !strings.Contains(vars, "APP_MODE") || strings.Contains(vars, "STRIPE_KEY") {
		t.Fatalf("plain variables imported, secret ones left for the user: %s", vars)
	}
	if env, _ := h.app.Components.ConnectionEnv(h.ctx(), nid); len(env) != 0 {
		t.Fatalf("no credentials may exist until re-supplied: %v", env)
	}
	// The re-supplied credentials complete the equivalent workspace.
	mc := mg["id"].(string)
	time.Sleep(1100 * time.Millisecond)
	nbase := "/api/workspaces/" + nid + "/components/"
	h.must(200, "POST", nbase+mc+"/connection/test", conn)
	h.must(200, "POST", nbase+mc+"/connection", conn)
	h.must(200, "PUT", "/api/workspaces/"+nid+"/variables/STRIPE_KEY", `{"secret":true,"value":"sk_live_new"}`)
	h.must(204, "PUT", "/api/workspaces/"+nid+"/ports/3000", `{"label":"web"}`)
	req2 := h.rawGet("/api/workspaces/" + nid + "/definition")
	_, same := post("/api/workspaces/"+nid+"/drift", map[string]any{"definition": strings.Replace(req, "name: src-app", "name: copy-a", 1)})
	if same["inSync"] != true {
		d, _ := json.Marshal(same["differences"])
		t.Fatalf("the imported workspace must match the definition after secrets are re-supplied:\n%s\n%s", d, req2)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func (h *harness) ctx() context.Context { return context.Background() }

func (h *harness) rawGet(path string) string {
	h.t.Helper()
	res, err := http.Get(h.ts.URL + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		h.t.Fatalf("GET %s: %d %s", path, res.StatusCode, b)
	}
	return string(b)
}

func (h *harness) raw2(method, path string) (string, int) {
	code, s := h.raw(method, path, "")
	return s, code
}
