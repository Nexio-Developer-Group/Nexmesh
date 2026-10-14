package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/config"
)

// Quality gates over a real workspace: real shell commands run in the
// workspace, gates see the proposed content temporarily, and a required
// failing gate blocks an agent change set until it is fixed or overridden.
func TestQualityGatesBlockAgentChangeSets(t *testing.T) {
	const key = "sk-gates-test-0123456789"
	var mu sync.Mutex
	step := 0
	proposals := []string{"ok but BROKEN now\n", "still fine\n"}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			return
		}
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
			return
		}
		var req struct{ Tools []any }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) == 0 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
			return
		}
		mu.Lock()
		step++
		n := step
		mu.Unlock()
		if n%2 == 1 {
			args := `{"title":"Edit main","changes":[{"path":"main.txt","content":` + jsonStr(proposals[(n-1)/2]) + `}]}`
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"p","type":"function","function":{"name":"propose_changes","arguments":` + jsonStr(args) + `}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"Proposed."}}]}`))
	}))
	defer model.Close()

	do := startLocalAppCfg(t, func(c *config.Config) { c.AllowLoopbackEgress = true })
	for i := 0; i < 100; i++ {
		if c, _ := do("GET", "/healthz", ""); c == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, body := do("POST", "/api/workspaces", `{"name":"gates-dev","cpu":2,"ramGb":4,"storageGb":20}`)
	id := between(body, `"id":"`, `"`)
	for i := 0; i < 300; i++ {
		if c, _ := do("GET", "/api/workspaces/"+id+"/files", ""); c == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	W := "/api/workspaces/" + id
	do("PUT", W+"/files/content", `{"path":"main.txt","content":"ok\n"}`)
	read := func() string {
		_, o := do("GET", W+"/files/content?path=main.txt", "")
		var f struct{ Content string }
		_ = json.Unmarshal([]byte(o), &f)
		return f.Content
	}
	cfg := `{"name":"Scripted","provider":"custom","protocol":"openai","endpoint":"` + model.URL + `","modelId":"m1","apiKey":"` + key + `"}`
	do("POST", "/api/agent/test", cfg)
	if c, o := do("POST", W+"/agent-configs", cfg); c != 201 {
		t.Fatalf("config: %d %s", c, o)
	}

	// Detection suggests the built-in checks and saves nothing.
	if c, o := do("GET", W+"/gates/detect", ""); c != 200 || !strings.Contains(o, `"builtin:secrets"`) {
		t.Fatalf("detect: %d %s", c, o)
	}
	if _, o := do("GET", W+"/gates", ""); !strings.Contains(o, `"confirmed":false`) {
		t.Fatalf("config before confirm: %s", o)
	}
	gatesCfg := `{"gates":[
		{"id":"nobroken","name":"No BROKEN marker","kind":"custom","command":"! grep -q BROKEN main.txt","policy":"required","enabled":true},
		{"id":"secrets","name":"Secret scan","kind":"secrets","command":"builtin:secrets","policy":"required","enabled":true},
		{"id":"noisy","name":"Advisory lint","kind":"lint","command":"echo warn; exit 3","policy":"advisory","enabled":true}]}`
	if c, o := do("PUT", W+"/gates", gatesCfg); c != 200 || !strings.Contains(o, `"confirmed":true`) {
		t.Fatalf("save gates: %d %s", c, o)
	}

	askAgent := func() string {
		_, o := do("POST", W+"/agent/sessions", `{}`)
		sid := between(o, `"id":"`, `"`)
		do("POST", W+"/agent/sessions/"+sid+"/messages", `{"text":"edit main"}`)
		for i := 0; i < 300; i++ {
			_, s := do("GET", W+"/agent/sessions/"+sid, "")
			if strings.Contains(s, `"status":"idle"`) {
				return between(s, `"changeSetId":"`, `"`)
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("agent did not finish")
		return ""
	}
	runGates := func(csID string) map[string]any {
		t.Helper()
		c, o := do("POST", W+"/gates/runs", `{"setId":"`+csID+`"}`)
		if c != 202 {
			t.Fatalf("start run: %d %s", c, o)
		}
		rid := between(o, `"id":"`, `"`)
		for i := 0; i < 400; i++ {
			_, r := do("GET", W+"/gates/runs/"+rid, "")
			var run map[string]any
			_ = json.Unmarshal([]byte(r), &run)
			if run["status"] != "running" {
				return run
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("gates did not finish")
		return nil
	}

	bad := askAgent()
	if bad == "" {
		t.Fatal("no change set proposed")
	}
	if c, o := do("POST", W+"/changes/"+bad+"/accept", `{"all":true}`); c != 422 || !strings.Contains(o, "gates_not_run") {
		t.Fatalf("accept before gates: %d %s", c, o)
	}
	run := runGates(bad)
	if run["passed"] != false || run["status"] != "done" {
		t.Fatalf("run: %v", run)
	}
	var logs []string
	for _, r := range run["results"].([]any) {
		m := r.(map[string]any)
		logs = append(logs, m["gateId"].(string)+"="+m["status"].(string))
	}
	if strings.Join(logs, ",") != "nobroken=failed,secrets=passed,noisy=failed" {
		t.Fatalf("results: %v", logs)
	}
	if read() != "ok\n" {
		t.Fatalf("the trial leaked into the workspace: %q", read())
	}
	if c, o := do("POST", W+"/changes/"+bad+"/accept", `{"all":true}`); c != 422 || !strings.Contains(o, "gates_failed") {
		t.Fatalf("accept after failed gates: %d %s", c, o)
	}
	_, view := do("GET", W+"/changes/"+bad, "")
	if !strings.Contains(view, `"gate":{"configured":true,"ran":true,"passed":false`) {
		t.Fatalf("view lacks gate state: %s", view)
	}
	if c, o := do("POST", W+"/changes/"+bad+"/accept", `{"all":true,"overrideGates":"prototype only"}`); c != 200 || !strings.Contains(o, `"gateOverride"`) {
		t.Fatalf("override: %d %s", c, o)
	}
	if !strings.Contains(read(), "BROKEN") {
		t.Fatal("overridden change not applied")
	}

	// Revert, then a good proposal passes its gates and needs no override.
	_, cps := do("GET", W+"/checkpoints", "")
	cpID := between(cps, `"id":"`, `"`)
	if c, o := do("POST", W+"/checkpoints/"+cpID+"/revert", `{}`); c != 200 {
		t.Fatalf("revert: %d %s", c, o)
	}
	good := askAgent()
	if r := runGates(good); r["passed"] != true {
		t.Fatalf("good run: %v", r)
	}
	if c, o := do("POST", W+"/changes/"+good+"/accept", `{"all":true}`); c != 200 || strings.Contains(o, `"gateOverride"`) {
		t.Fatalf("accept after passing: %d %s", c, o)
	}
	if read() != "still fine\n" {
		t.Fatalf("content: %q", read())
	}
	_, aud := do("GET", W+"/audit", "")
	for _, a := range []string{"gates.configured", "gates.run_started", "gates.run_finished", "gates.overridden", "changeset.trial"} {
		if !strings.Contains(aud, a) {
			t.Fatalf("audit lacks %s", a)
		}
	}
}
