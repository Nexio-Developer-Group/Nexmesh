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

// The Code Agent end to end: a real workspace agent with real files, the real
// model-provider client talking to a scripted OpenAI-compatible server, and the
// real change-set service. The model reads a file, proposes an edit, asks for a
// risky command and a secret file; the user accepts, declines and reverts.
func TestCodeAgentOverRealWorkspace(t *testing.T) {
	const key = "sk-agent-test-0123456789"
	var mu sync.Mutex
	var bodies []string
	step := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
			return
		}
		var req struct {
			Messages []map[string]any `json:"messages"`
			Tools    []any            `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) == 0 { // the configuration test, not the agent
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
			return
		}
		raw, _ := json.Marshal(req)
		mu.Lock()
		bodies = append(bodies, string(raw))
		step++
		n := step
		mu.Unlock()
		call := func(id, name, args string) string {
			return `{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + jsonStr(args) + `}}]}}],"usage":{"prompt_tokens":20,"completion_tokens":8}}`
		}
		var out string
		switch n {
		case 1:
			out = call("c1", "read_file", `{"path":"app.py"}`)
		case 2:
			out = call("c2", "read_file", `{"path":".env"}`)
		case 3:
			out = call("c3", "propose_changes", `{"title":"Greet by name","summary":"Use the name argument","changes":[{"path":"app.py","content":"def greet(name):\n    return 'hello ' + name\n"}]}`)
		case 4:
			out = call("c4", "run_command", `{"command":"echo done > out.txt"}`)
		default:
			out = `{"choices":[{"finish_reason":"stop","message":{"content":"I proposed a change and asked to run a command."}}]}`
		}
		_, _ = w.Write([]byte(out))
	}))
	defer model.Close()

	do := startLocalAppCfg(t, func(c *config.Config) { c.AllowLoopbackEgress = true })
	for i := 0; i < 100; i++ {
		if c, _ := do("GET", "/healthz", ""); c == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, body := do("POST", "/api/workspaces", `{"name":"agent-dev","cpu":2,"ramGb":4,"storageGb":20}`)
	id := between(body, `"id":"`, `"`)
	for i := 0; i < 300; i++ {
		if c, _ := do("GET", "/api/workspaces/"+id+"/files", ""); c == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	do("PUT", "/api/workspaces/"+id+"/files/content", `{"path":"app.py","content":"def greet():\n    return 'hello'\n"}`)
	do("PUT", "/api/workspaces/"+id+"/files/content", `{"path":".env","content":"STRIPE_KEY=sk_live_REAL_SECRET_VALUE\n"}`)
	if c, o := do("PUT", "/api/workspaces/"+id+"/variables/DB_PASSWORD", `{"value":"hunter2-very-secret","secret":true}`); c != 200 {
		t.Fatalf("variable: %d %s", c, o)
	}

	cfg := `{"name":"Scripted","provider":"custom","protocol":"openai","endpoint":"` + model.URL + `","modelId":"m1","apiKey":"` + key + `"}`
	if c, o := do("POST", "/api/agent/test", cfg); c != 200 || !strings.Contains(o, `"passed":true`) {
		t.Fatalf("model test: %d %s", c, o)
	}
	if c, o := do("POST", "/api/workspaces/"+id+"/agent-configs", cfg); c != 201 {
		t.Fatalf("save config: %d %s", c, o)
	}

	// Default level is propose: the agent has no run_command tool yet.
	if c, o := do("GET", "/api/workspaces/"+id+"/agent/settings", ""); c != 200 || !strings.Contains(o, `"level":"propose"`) {
		t.Fatalf("settings: %d %s", c, o)
	}
	if c, o := do("PUT", "/api/workspaces/"+id+"/agent/settings", `{"level":"execute"}`); c != 200 {
		t.Fatalf("settings put: %d %s", c, o)
	}
	_, ctxText := do("GET", "/api/workspaces/"+id+"/agent/context", "")
	if strings.Contains(ctxText, "hunter2") || !strings.Contains(ctxText, "DB_PASSWORD") {
		t.Fatalf("context preview: %s", ctxText)
	}

	c, o := do("POST", "/api/workspaces/"+id+"/agent/sessions", `{}`)
	if c != 201 {
		t.Fatalf("session: %d %s", c, o)
	}
	sid := between(o, `"id":"`, `"`)
	if c, o := do("POST", "/api/workspaces/"+id+"/agent/sessions/"+sid+"/messages", `{"text":"make greet take a name"}`); c != 202 {
		t.Fatalf("send: %d %s", c, o)
	}
	var sess struct {
		Status  string
		Pending *struct{ CallID, Command string }
		Turns   []struct {
			Role  string
			Calls []struct{ Name, Status, Result, ChangeSetID string }
		}
	}
	poll := func(want string) {
		t.Helper()
		for i := 0; i < 300; i++ {
			_, o := do("GET", "/api/workspaces/"+id+"/agent/sessions/"+sid, "")
			sess = struct {
				Status  string
				Pending *struct{ CallID, Command string }
				Turns   []struct {
					Role  string
					Calls []struct{ Name, Status, Result, ChangeSetID string }
				}
			}{}
			_ = json.Unmarshal([]byte(o), &sess)
			if sess.Status == want {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("session never reached %s: %+v", want, sess)
	}
	poll("awaiting_approval")
	if sess.Pending == nil || sess.Pending.Command != "echo done > out.txt" {
		t.Fatalf("pending: %+v", sess.Pending)
	}
	if c, _ := do("GET", "/api/workspaces/"+id+"/files/content?path=out.txt", ""); c != 404 {
		t.Fatal("the risky command ran before approval")
	}
	// Decline it.
	if c, o := do("POST", "/api/workspaces/"+id+"/agent/sessions/"+sid+"/approve", `{"callId":"`+sess.Pending.CallID+`","approve":false}`); c != 204 {
		t.Fatalf("approve: %d %s", c, o)
	}
	poll("idle")
	if c, _ := do("GET", "/api/workspaces/"+id+"/files/content?path=out.txt", ""); c != 404 {
		t.Fatal("declined command ran")
	}
	var csID, readStatus, envStatus string
	for _, tn := range sess.Turns {
		for _, cl := range tn.Calls {
			switch cl.Name {
			case "propose_changes":
				csID = cl.ChangeSetID
			case "read_file":
				if strings.Contains(cl.Result, "def greet") {
					readStatus = cl.Status
				} else {
					envStatus = cl.Status
				}
			}
		}
	}
	if readStatus != "ok" || envStatus != "denied" || csID == "" {
		t.Fatalf("read=%s env=%s cs=%q turns=%+v", readStatus, envStatus, csID, sess.Turns)
	}
	// Nothing has been written by the agent; the user reviews and accepts.
	if _, f := do("GET", "/api/workspaces/"+id+"/files/content?path=app.py", ""); strings.Contains(f, "name") {
		t.Fatalf("staged change reached the file: %s", f)
	}
	if c, o := do("POST", "/api/workspaces/"+id+"/changes/"+csID+"/accept", `{"all":true}`); c != 200 {
		t.Fatalf("accept: %d %s", c, o)
	}
	if _, f := do("GET", "/api/workspaces/"+id+"/files/content?path=app.py", ""); !strings.Contains(f, "greet(name)") {
		t.Fatalf("accepted change missing: %s", f)
	}
	// What the model was sent never contained a secret.
	mu.Lock()
	all := strings.Join(bodies, "\n")
	mu.Unlock()
	for _, bad := range []string{"sk_live_REAL_SECRET_VALUE", "hunter2-very-secret"} {
		if strings.Contains(all, bad) {
			t.Fatalf("secret %q reached the model provider", bad)
		}
	}
	// The record shows the denied read and the declined command, attributed to the agent.
	_, aud := do("GET", "/api/workspaces/"+id+"/audit", "")
	for _, a := range []string{"agent.action_denied", "agent.command_declined", "changeset.proposed", "changeset.applied", "agent.run_finished"} {
		if !strings.Contains(aud, a) {
			t.Fatalf("audit lacks %s", a)
		}
	}
	if strings.Contains(aud, "hunter2") || strings.Contains(aud, "sk_live") {
		t.Fatal("audit leaks a secret")
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }
