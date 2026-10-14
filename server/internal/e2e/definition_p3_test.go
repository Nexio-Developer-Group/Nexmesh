package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/app"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
)

// The definition carries connection lines, environments, gates, rules and the
// agent policy, never a secret, and an import rebuilds them (PRD F12).
func TestDefinitionCarriesPhase3State(t *testing.T) {
	passing := func(_ context.Context, p probe.Params, _ probe.Report) *domain.TestResult {
		r := &domain.TestResult{At: time.Now().UTC(), LatencyMS: 5, Passed: true}
		for _, st := range probe.StageOrder {
			r.Stages = append(r.Stages, domain.StageResult{Stage: st, Status: "passed"})
		}
		return r
	}
	do := startLocalAppOpts(t, nil, app.Options{Probe: passing})
	h := &p3h{t: t, do: do}
	for i := 0; i < 100; i++ {
		if c, _ := do("GET", "/healthz", ""); c == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ws := h.must(201, "POST", "/api/workspaces", `{"name":"p3-src","cpu":2,"ramGb":4,"storageGb":20}`)
	id := ws["id"].(string)
	W := "/api/workspaces/" + id
	h.eventually("agent online", func() bool { c, _ := h.call("GET", W+"/files", ""); return c == 200 })

	add := func(cat string) string {
		return h.must(201, "POST", W+"/components", `{"catalogId":"`+cat+`"}`)["id"].(string)
	}
	nest, mongo := add("nestjs"), add("mongodb")
	h.must(201, "POST", W+"/wires", `{"fromId":"`+nest+`","toId":"`+mongo+`","name":"orders-db","confirm":true}`)

	// Two environments for the database card; credentials are never exported.
	const stagingPw = "staging-db-password-xyz"
	prodConn := `{"connName":"orders-db","environment":"production","fields":{"host":"prod.example.net","port":27017,"database":"orders","username":"app","password":"prod-db-password-xyz"}}`
	h.must(200, "POST", W+"/components/"+mongo+"/connection/test", prodConn)
	h.must(200, "POST", W+"/components/"+mongo+"/connection", prodConn)
	time.Sleep(1100 * time.Millisecond)
	stgConn := `{"connName":"orders-db","environment":"staging","fields":{"host":"staging.example.net","port":27017,"database":"orders","username":"app","password":"` + stagingPw + `"}}`
	h.must(200, "POST", W+"/components/"+mongo+"/connection/test", stgConn)
	h.must(200, "PUT", W+"/components/"+mongo+"/connection", stgConn)
	h.must(200, "PUT", W+"/variables/LOG_LEVEL", `{"environment":"staging","value":"debug"}`)
	h.must(200, "PUT", W+"/variables/LOG_LEVEL", `{"environment":"production","value":"warn"}`)
	h.must(200, "POST", W+"/environment", `{"environment":"staging"}`)

	h.must(200, "PUT", W+"/gates", `{"gates":[
		{"id":"tests","name":"Tests","kind":"test","command":"go test ./...","policy":"required","enabled":true},
		{"id":"secrets","name":"Secret scan","kind":"secrets","command":"builtin:secrets","policy":"required","enabled":true}]}`)
	h.must(200, "PUT", W+"/rules", `{"title":"API style","scope":"path","paths":["src/api/**"],"protect":["migrations/"],"body":"Use snake_case for JSON."}`)
	h.must(200, "PUT", W+"/agent/settings", `{"level":"execute","autoAccept":true}`)

	req := h.rawGet(W + "/definition")
	for _, leak := range []string{"prod-db-password-xyz", stagingPw} {
		if strings.Contains(req, leak) {
			t.Fatalf("definition leaks a credential:\n%s", req)
		}
	}
	for _, want := range []string{"wires:", "from: nestjs", "to: mongodb", "name: orders-db", "environments:", "active: staging", "profiles:", "staging.example.net",
		"gates:", "id: tests", "command: go test ./...", "rules:", "name: api-style", "- src/api/**", "- migrations/", "Use snake_case for JSON.", "agentPolicy:", "level: execute", "environment: staging"} {
		if !strings.Contains(req, want) {
			t.Errorf("definition missing %q:\n%s", want, req)
		}
	}
	if strings.Contains(req, "autoAccept") {
		t.Fatal("auto-accept must never travel in a definition")
	}

	post := func(path string, v any) (int, map[string]any) {
		b, _ := json.Marshal(v)
		return h.call("POST", path, string(b))
	}
	if c, rep := post(W+"/drift", map[string]any{"definition": req}); c != 200 || rep["inSync"] != true {
		t.Fatalf("a fresh export must be in sync: %d %v", c, rep)
	}

	// ---- Import: wires, environments, rules and the agent policy are rebuilt; commands start switched off.
	c, m := post("/api/workspaces/import", map[string]any{"definition": req, "name": "p3-copy"})
	if c != 201 {
		t.Fatalf("import: %d %v", c, m)
	}
	nid := m["workspace"].(map[string]any)["id"].(string)
	N := "/api/workspaces/" + nid
	h.eventually("copy ready", func() bool { return h.workspace(nid)["status"] == "ready" })
	todos, _ := json.Marshal(m["todos"])
	for _, want := range []string{`"kind":"review_gates"`, "MongoDB (staging)", "orders-db (MongoDB)"} {
		if !strings.Contains(string(todos), want) {
			t.Errorf("todos lack %s: %s", want, todos)
		}
	}
	h.eventually("wires applied", func() bool {
		_, wl := h.raw("GET", N+"/wires", "")
		return strings.Contains(wl, `"orders-db"`)
	})
	h.eventually("rules written", func() bool {
		_, rl := h.raw("GET", N+"/rules", "")
		return strings.Contains(rl, "Use snake_case for JSON.")
	})
	if ws := h.workspace(nid); ws["activeEnvironment"] != "staging" {
		t.Fatalf("active environment: %v", ws["activeEnvironment"])
	}
	_, gl := h.raw("GET", N+"/gates", "")
	var gc struct {
		Gates []struct {
			ID      string
			Enabled bool
		}
	}
	_ = json.Unmarshal([]byte(gl), &gc)
	enabled := map[string]bool{}
	for _, g := range gc.Gates {
		enabled[g.ID] = g.Enabled
	}
	if len(gc.Gates) != 2 || enabled["tests"] || !enabled["secrets"] {
		t.Fatalf("imported command gates must start off, built-ins stay on: %s", gl)
	}
	if _, st := h.raw("GET", N+"/agent/settings", ""); !strings.Contains(st, `"level":"execute"`) || strings.Contains(st, `"autoAccept":true`) {
		t.Fatalf("agent policy: %s", st)
	}
	_, vars := h.raw("GET", N+"/variables", "")
	if !strings.Contains(vars, `"environment":"staging"`) || !strings.Contains(vars, `"environment":"production"`) {
		t.Fatalf("environment variables: %s", vars)
	}

	// ---- Drift names Phase 3 differences.
	h.must(200, "PUT", W+"/gates", `{"gates":[{"id":"tests","name":"Tests","kind":"test","command":"go test ./...","policy":"advisory","enabled":true}]}`)
	h.must(200, "PUT", W+"/rules", `{"name":"api-style","title":"API style","scope":"path","paths":["src/api/**"],"protect":["migrations/"],"body":"Use camelCase."}`)
	h.must(200, "POST", W+"/environment", `{"environment":"development"}`)
	h.must(200, "PUT", W+"/agent/settings", `{"level":"read","autoAccept":false}`)
	c, rep := post(W+"/drift", map[string]any{"definition": req})
	if c != 200 || rep["inSync"] != false {
		t.Fatalf("drift: %d %v", c, rep)
	}
	raw, _ := json.Marshal(rep["differences"])
	for _, want := range []string{`"category":"gate"`, `"subject":"Secret scan"`, `"category":"rule"`, `"field":"text"`, `"category":"environment"`, `"field":"active"`, `"field":"level"`, `"field":"policy"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("drift lacks %s:\n%s", want, raw)
		}
	}
}

// p3h is a tiny JSON helper over the local-agent test app.
type p3h struct {
	t  *testing.T
	do func(method, path, body string) (int, string)
}

func (h *p3h) call(method, path, body string) (int, map[string]any) {
	c, o := h.do(method, path, body)
	var m map[string]any
	_ = json.Unmarshal([]byte(o), &m)
	return c, m
}
func (h *p3h) raw(method, path, body string) (int, string) { return h.do(method, path, body) }
func (h *p3h) rawGet(path string) string {
	c, o := h.do("GET", path, "")
	if c != 200 {
		h.t.Fatalf("GET %s: %d %s", path, c, o)
	}
	return o
}
func (h *p3h) must(code int, method, path, body string) map[string]any {
	h.t.Helper()
	c, o := h.do(method, path, body)
	if c != code {
		h.t.Fatalf("%s %s = %d, want %d: %s", method, path, c, code, o)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(o), &m)
	return m
}
func (h *p3h) workspace(id string) map[string]any {
	_, m := h.call("GET", "/api/workspaces/"+id, "")
	return m
}
func (h *p3h) eventually(what string, cond func() bool) {
	h.t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
}
