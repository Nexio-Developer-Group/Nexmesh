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

// A commit that includes files written by an accepted agent change carries an
// Assisted-by trailer, and the Git status says which files those are (PRD F18).
func TestAgentAuthoredCommitsAreMarked(t *testing.T) {
	const key = "sk-git-test-0123456789"
	var mu sync.Mutex
	n := 0
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
		n++
		step := n
		mu.Unlock()
		if step == 1 {
			args := `{"title":"Add feature","changes":[{"path":"app/feature.txt","content":"agent wrote this\n"}]}`
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
	_, body := do("POST", "/api/workspaces", `{"name":"git-agent","cpu":2,"ramGb":4,"storageGb":20}`)
	id := between(body, `"id":"`, `"`)
	W := "/api/workspaces/" + id
	for i := 0; i < 300; i++ {
		if c, _ := do("GET", W+"/files", ""); c == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	run := func(cmd string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"command": cmd, "timeoutSec": 30})
		if c, o := do("POST", W+"/exec", string(b)); c != 200 || !strings.Contains(o, `"exitCode":0`) {
			t.Fatalf("%s: %d %s", cmd, c, o)
		}
	}
	run("mkdir -p app && cd app && git init -q && git config user.email t@example.com && git config user.name T && echo base > base.txt && git add . && git commit -qm base")

	cfg := `{"name":"Scripted","provider":"custom","protocol":"openai","endpoint":"` + model.URL + `","modelId":"m1","apiKey":"` + key + `"}`
	do("POST", "/api/agent/test", cfg)
	if c, o := do("POST", W+"/agent-configs", cfg); c != 201 {
		t.Fatalf("config: %d %s", c, o)
	}
	_, o := do("POST", W+"/agent/sessions", `{}`)
	sid := between(o, `"id":"`, `"`)
	do("POST", W+"/agent/sessions/"+sid+"/messages", `{"text":"add a feature file"}`)
	var sess string
	for i := 0; i < 300; i++ {
		_, sess = do("GET", W+"/agent/sessions/"+sid, "")
		if strings.Contains(sess, `"status":"idle"`) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	csID := between(sess, `"changeSetId":"`, `"`)
	if c, o := do("POST", W+"/changes/"+csID+"/accept", `{"all":true}`); c != 200 {
		t.Fatalf("accept: %d %s", c, o)
	}
	// A file the user writes by hand is not marked.
	do("PUT", W+"/files/content", `{"path":"app/mine.txt","content":"by hand\n"}`)

	_, st := do("GET", W+"/git/status?repo=app", "")
	var status struct {
		AgentFiles []string
		Files      []struct{ Path string }
	}
	_ = json.Unmarshal([]byte(st), &status)
	if len(status.AgentFiles) != 1 || status.AgentFiles[0] != "feature.txt" || len(status.Files) != 2 {
		t.Fatalf("status: %s", st)
	}
	if c, o := do("POST", W+"/git/stage", `{"repo":"app","paths":["feature.txt","mine.txt"]}`); c >= 300 {
		t.Fatalf("stage: %d %s", c, o)
	}
	if c, o := do("POST", W+"/git/commit", `{"repo":"app","message":"add feature"}`); c >= 300 {
		t.Fatalf("commit: %d %s", c, o)
	}
	_, lg := do("GET", W+"/git/log?repo=app", "")
	if !strings.Contains(lg, "add feature") {
		t.Fatalf("log: %s", lg)
	}
	run("cd app && git log -1 --format=%B | grep -q 'Assisted-by: Nexmesh Code Agent'")
	// Once committed the files are no longer pending, so the next commit is not marked.
	do("PUT", W+"/files/content", `{"path":"app/later.txt","content":"later\n"}`)
	do("POST", W+"/git/stage", `{"repo":"app","paths":["later.txt"]}`)
	do("POST", W+"/git/commit", `{"repo":"app","message":"later work"}`)
	run("cd app && ! git log -1 --format=%B | grep -q 'Assisted-by'")
	_, aud := do("GET", W+"/audit", "")
	if !strings.Contains(aud, `"agentAssisted":true`) {
		t.Fatalf("audit lacks the marker: %s", aud)
	}
}
