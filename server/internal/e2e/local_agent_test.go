package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/app"
	"github.com/nexio-developer-group/nexmesh/server/internal/config"
)

// With the fake driver and a listen address, each workspace gets an in-process
// agent, so the Code view, terminal and commands work without Docker.
func TestFakeDriverRunsLocalAgent(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := t.TempDir()
	t.Setenv("NEXMESH_FAKE_WORKSPACE_DIR", base)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{AuthDisabled: true, Addr: addr, Store: "memory", Driver: "fake", CatalogDir: "../../../catalog",
		MasterKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200, PublicURL: "http://" + addr}
	a, err := app.New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), app.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.Start(ctx)
	srv := &http.Server{Addr: addr, Handler: a.Handler}
	go srv.ListenAndServe()
	defer srv.Close()
	defer a.Close()

	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for i := 0; i < 100; i++ {
		if c, _ := do("GET", "/healthz", ""); c == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	code, body := do("POST", "/api/workspaces", `{"name":"local-dev","cpu":2,"ramGb":4,"storageGb":20}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	id := between(body, `"id":"`, `"`)

	var list string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, b := do("GET", "/api/workspaces/"+id+"/files", ""); c == 200 {
			list = b
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(list, "README.md") {
		t.Fatalf("local agent never served the seeded workspace: %q", list)
	}
	if c, b := do("PUT", "/api/workspaces/"+id+"/files/content", `{"path":"hello.sh","content":"echo from-$NAME\n"}`); c != 200 {
		t.Fatalf("write: %d %s", c, b)
	}
	if _, err := os.Stat(filepath.Join(base, id, "hello.sh")); err != nil {
		t.Fatalf("file is not a real file on disk: %v", err)
	}
	if c, b := do("POST", "/api/workspaces/"+id+"/exec", `{"command":"NAME=nx sh hello.sh"}`); c != 200 || !strings.Contains(b, "from-nx") {
		t.Fatalf("exec: %d %s", c, b)
	}
	// T2.2: variables reach every process the agent starts, and edits apply to the next one.
	if c, b := do("PUT", "/api/workspaces/"+id+"/variables/DB_PASSWORD", `{"secret":true,"value":"s3cr3t-one"}`); c != 200 || strings.Contains(b, "s3cr3t") {
		t.Fatalf("set secret: %d %s", c, b)
	}
	if c, b := do("PUT", "/api/workspaces/"+id+"/variables/APP_MODE", `{"value":"dev"}`); c != 200 {
		t.Fatalf("set plain: %d %s", c, b)
	}
	if c, b := do("POST", "/api/workspaces/"+id+"/exec", `{"command":"echo $APP_MODE/$DB_PASSWORD"}`); c != 200 || !strings.Contains(b, "dev/s3cr3t-one") {
		t.Fatalf("exec env: %d %s", c, b)
	}
	do("PUT", "/api/workspaces/"+id+"/variables/DB_PASSWORD", `{"secret":true,"value":"s3cr3t-two"}`)
	if _, b := do("POST", "/api/workspaces/"+id+"/exec", `{"command":"echo $DB_PASSWORD"}`); !strings.Contains(b, "s3cr3t-two") {
		t.Fatalf("rotated secret not applied to new process: %s", b)
	}
	if _, b := do("GET", "/api/workspaces/"+id+"/variables", ""); strings.Contains(b, "s3cr3t") || !strings.Contains(b, `"APP_MODE"`) {
		t.Fatalf("listing leaked or lost data: %s", b)
	}
	if _, b := do("GET", "/api/workspaces/"+id, ""); strings.Contains(b, "s3cr3t") {
		t.Fatalf("workspace payload leaked a secret: %s", b)
	}
	if c, b := do("POST", "/api/workspaces/"+id+"/variables/import", `{"content":"IMPORTED_API_KEY=imp-secret-9\nPLAIN=1\n","secrets":"auto"}`); c != 200 || strings.Contains(b, "imp-secret-9") || !strings.Contains(b, "IMPORTED_API_KEY") {
		t.Fatalf("import: %d %s", c, b)
	}
	if c, b := do("GET", "/api/workspaces/"+id+"/variables/export", ""); c != 200 || !strings.Contains(b, "IMPORTED_API_KEY=\n") || strings.Contains(b, "imp-secret-9") || strings.Contains(b, "s3cr3t") {
		t.Fatalf("export: %d %s", c, b)
	}
	if c, _ := do("DELETE", "/api/workspaces/"+id+"?confirmName=local-dev", ""); c != 202 && c != 204 && c != 200 {
		t.Logf("delete returned %d (informational)", c)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	return s[:strings.Index(s, b)]
}
