package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/app"
	"github.com/nexio-developer-group/nexmesh/server/internal/config"
)

// startLocalApp runs the whole product with the local agent driver on a real port.
func startLocalApp(t *testing.T) func(method, path, body string) (int, string) {
	return startLocalAppCfg(t, nil)
}

func startLocalAppCfg(t *testing.T, mod func(*config.Config)) func(method, path, body string) (int, string) {
	t.Helper()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	t.Setenv("NEXMESH_FAKE_WORKSPACE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := config.Config{AuthDisabled: true, Addr: addr, Store: "memory", Driver: "fake", CatalogDir: "../../../catalog",
		MasterKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200, PublicURL: "http://" + addr}
	if mod != nil {
		mod(&cfg)
	}
	a, err := app.New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), app.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.Start(ctx)
	srv := &http.Server{Addr: addr, Handler: a.Handler}
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.Close(); a.Close() })
	for i := 0; i < 100; i++ {
		if res, err := http.Get("http://" + addr + "/healthz"); err == nil {
			res.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func(method, path, body string) (int, string) {
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
}

func TestDevContainerImportRunsPostCreateAndExportRoundTrips(t *testing.T) {
	do := startLocalApp(t)
	wait := func(what string, cond func() bool) {
		t.Helper()
		for i := 0; i < 400; i++ {
			if cond() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out: %s", what)
	}
	file := `{
  // a typical file
  "name": "Orders API",
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu-24.04",
  "forwardPorts": [3000],
  "portsAttributes": {"3000": {"label": "web"}},
  "containerEnv": {"APP_MODE": "dev", "DB_PASSWORD": "should-not-import"},
  "hostRequirements": {"cpus": 2, "memory": "4gb", "storage": "20gb"},
  "postCreateCommand": "echo created-$((6*7)) > built.txt",
  "remoteUser": "vscode"
}`
	esc := func(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) }
	body := func(extra string) string { return `{"content":"` + esc(file) + `"` + extra + `}` }

	// Dry run: report only, nothing created.
	c, out := do("POST", "/api/devcontainer/import", body(`,"dryRun":true`))
	if c != 200 || !strings.Contains(out, `"dryRun":true`) || !strings.Contains(out, `"field":"remoteUser"`) || !strings.Contains(out, `"status":"unsupported"`) || !strings.Contains(out, `"name":"orders-api"`) {
		t.Fatalf("dry run: %d %s", c, out)
	}
	if c, list := do("GET", "/api/workspaces", ""); c != 200 || strings.Contains(list, "orders-api") {
		t.Fatalf("dry run created a workspace: %s", list)
	}

	// For real, with the post-create command opted in.
	c, out = do("POST", "/api/devcontainer/import", body(`,"runPostCreate":true`))
	if c != 201 {
		t.Fatalf("import: %d %s", c, out)
	}
	id := between(out, `"id":"`, `"`)
	wait("post-create output", func() bool {
		cc, content := do("GET", "/api/workspaces/"+id+"/files/content?path=built.txt", "")
		return cc == 200 && strings.Contains(content, "created-42")
	})
	if _, vars := do("GET", "/api/workspaces/"+id+"/variables", ""); !strings.Contains(vars, "APP_MODE") || strings.Contains(vars, "should-not-import") {
		t.Fatalf("variables: %s", vars)
	}
	if _, ports := do("GET", "/api/workspaces/"+id+"/ports", ""); !strings.Contains(ports, `"label":"web"`) {
		t.Fatalf("ports: %s", ports)
	}
	if _, ops := do("GET", "/api/workspaces/"+id+"/operations", ""); !strings.Contains(ops, "post_create") {
		t.Fatalf("operations: %s", ops)
	}

	// Without the opt-in, nothing runs.
	c, out = do("POST", "/api/devcontainer/import", strings.Replace(body(`,"name":"no-run"`), "Orders API", "Second", 1))
	if c != 201 {
		t.Fatalf("second import: %d %s", c, out)
	}
	id2 := between(out, `"id":"`, `"`)
	wait("second workspace files", func() bool { cc, _ := do("GET", "/api/workspaces/"+id2+"/files", ""); return cc == 200 })
	if cc, _ := do("GET", "/api/workspaces/"+id2+"/files/content?path=built.txt", ""); cc != 404 {
		t.Fatalf("post-create ran without opt-in (%d)", cc)
	}

	// Export: valid JSON for dev container tools, no secrets, with a report.
	cc, exported := do("GET", "/api/workspaces/"+id+"/devcontainer", "")
	if cc != 200 || !strings.Contains(exported, `"forwardPorts"`) || !strings.Contains(exported, `"APP_MODE": "dev"`) || strings.Contains(exported, "should-not-import") {
		t.Fatalf("export: %d %s", cc, exported)
	}
	if cc, rep := do("GET", "/api/workspaces/"+id+"/devcontainer?format=report", ""); cc != 200 || !strings.Contains(rep, `"report"`) {
		t.Fatalf("report: %d %s", cc, rep)
	}
	// Invalid input is explained.
	if c, out := do("POST", "/api/devcontainer/import", `{"content":"{ not json"}`); c != 422 || !strings.Contains(out, "not valid devcontainer.json") {
		t.Fatalf("bad file: %d %s", c, out)
	}
}
