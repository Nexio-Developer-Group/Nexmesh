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

// The agent can only suggest canvas changes. Accepting a card proposal adds a
// Draft card, accepting a connection creates the line, and a change set names
// the components and variables it touches.
func TestAgentCanvasProposalsAndImpact(t *testing.T) {
	const key = "sk-canvas-test-0123456789"
	var mu sync.Mutex
	step := 0
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
		call := func(name, args string) {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"t` + name + `","type":"function","function":{"name":"` + name + `","arguments":` + jsonStr(args) + `}}]}}]}`))
		}
		switch n {
		case 1:
			call("propose_card", `{"catalog_id":"redis","reason":"cache sessions"}`)
		case 2:
			call("propose_connection", `{"from":"NestJS","to":"MongoDB","reason":"store orders"}`)
		case 3:
			call("propose_changes", `{"title":"Use Mongo","changes":[{"path":"db.js","content":"const uri = process.env.MONGODB_URI\n"}]}`)
		default:
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"Done."}}]}`))
		}
	}))
	defer model.Close()

	do := startLocalAppCfg(t, func(c *config.Config) { c.AllowLoopbackEgress = true })
	for i := 0; i < 100; i++ {
		if c, _ := do("GET", "/healthz", ""); c == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, body := do("POST", "/api/workspaces", `{"name":"canvas-agent","cpu":2,"ramGb":4,"storageGb":20}`)
	id := between(body, `"id":"`, `"`)
	W := "/api/workspaces/" + id
	for i := 0; i < 300; i++ {
		if c, _ := do("GET", W+"/files", ""); c == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	do("POST", W+"/components", `{"catalogId":"nestjs"}`)
	do("POST", W+"/components", `{"catalogId":"mongodb"}`)
	cfg := `{"name":"Scripted","provider":"custom","protocol":"openai","endpoint":"` + model.URL + `","modelId":"m1","apiKey":"` + key + `"}`
	do("POST", "/api/agent/test", cfg)
	if c, o := do("POST", W+"/agent-configs", cfg); c != 201 {
		t.Fatalf("config: %d %s", c, o)
	}
	_, o := do("POST", W+"/agent/sessions", `{}`)
	sid := between(o, `"id":"`, `"`)
	do("POST", W+"/agent/sessions/"+sid+"/messages", `{"text":"design this"}`)
	var sess string
	for i := 0; i < 300; i++ {
		_, sess = do("GET", W+"/agent/sessions/"+sid, "")
		if strings.Contains(sess, `"status":"idle"`) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !strings.Contains(sess, `"status":"idle"`) {
		t.Fatalf("agent did not finish: %s", sess)
	}
	countCards := func() int {
		_, l := do("GET", W+"/components", "")
		return strings.Count(l, `"catalogId"`)
	}
	_, wl := do("GET", W+"/wires", "")
	if countCards() != 2 || strings.Contains(wl, `"fromId"`) {
		t.Fatalf("the agent changed the canvas before approval: cards=%d wires=%s", countCards(), wl)
	}
	_, pl := do("GET", W+"/agent/proposals", "")
	var props struct {
		Proposals []struct{ ID, Kind, Status string }
	}
	_ = json.Unmarshal([]byte(pl), &props)
	if len(props.Proposals) != 2 {
		t.Fatalf("proposals: %s", pl)
	}
	for _, p := range props.Proposals {
		if c, o := do("POST", W+"/agent/proposals/"+p.ID+"/accept", ""); c != 200 || !strings.Contains(o, `"status":"accepted"`) {
			t.Fatalf("accept %s: %d %s", p.Kind, c, o)
		}
	}
	if countCards() != 3 {
		t.Fatalf("accepted card missing: %d", countCards())
	}
	if _, l := do("GET", W+"/components", ""); !strings.Contains(l, `"redis"`) || !strings.Contains(l, `"observedState":"draft"`) {
		t.Fatalf("redis should be a draft card: %s", l)
	}
	if _, wl := do("GET", W+"/wires", ""); !strings.Contains(wl, `"fromId"`) {
		t.Fatalf("accepted connection missing: %s", wl)
	}
	// The change set names the variable and the component it touches.
	csID := between(sess, `"changeSetId":"`, `"`)
	_, cv := do("GET", W+"/changes/"+csID, "")
	if !strings.Contains(cv, `"variables":["MONGODB_URI"]`) || !strings.Contains(cv, `"name":"MongoDB"`) || !strings.Contains(cv, `"name":"NestJS"`) {
		t.Fatalf("impact: %s", cv)
	}
}
