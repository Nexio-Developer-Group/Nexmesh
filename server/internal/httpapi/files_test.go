package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/gitrun"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/ports"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/runner"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/terminal"
	"github.com/nexio-developer-group/nexmesh/server/internal/agent/workfs"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitops"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/preview"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/tunnel"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// filesRig runs the real HTTP layer, agent registry and a real agent serving a
// temp directory, so these tests cover the whole path a browser request takes.
func filesRig(t *testing.T, status string) (*httptest.Server, store.Store, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := store.NewMemory()
	bus := events.NewBus()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "local", Name: "n", Status: status})
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "other", OwnerID: "someone-else", Name: "o", Status: domain.WSReady})
	aud := audit.New(st, log)
	reg := agentapi.NewRegistry(st, bus)
	wsSvc := workspace.NewService(st, runtime.NewFake(runtime.Capabilities{MaxCPU: 4, MaxRAMGB: 8, MaxStorageGB: 50}), ops.NewEngine(st, bus, log, 1), bus, aud, log)
	api := httpapi.New(log)
	reg.Register(api)
	api.RegisterFiles(httpapi.FilesDeps{Svc: wsSvc, Agent: reg, Audit: aud})
	api.RegisterTerminal(httpapi.FilesDeps{Svc: wsSvc, Agent: reg, Audit: aud})
	api.RegisterPorts(httpapi.FilesDeps{Svc: wsSvc, Agent: reg, Audit: aud}, st, preview.NewService(preview.Config{Key: make([]byte, 32)}, st, tunnel.NewDialer(reg)))
	be, _ := secrets.NewStoreBackend(st, make([]byte, 32))
	creds := gitcred.NewService(st, secrets.New(be), aud)
	api.RegisterGitCredentials(creds)
	api.RegisterAudit(aud, wsSvc)
	api.RegisterGit(httpapi.FilesDeps{Svc: wsSvc, Agent: reg, Audit: aud}, gitops.NewService(reg, creds, aud))
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)

	root := t.TempDir()
	fsys, err := workfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := reg.IssueToken(ctx, "ws1")
	ag := agent.New(agent.Config{ControlURL: ts.URL, WorkspaceID: "ws1", Token: tok}, log)
	fsys.Install(ag)
	(&runner.Runner{Root: root}).Install(ag)
	ports.NewScanner().Install(ag)
	(&gitrun.Runner{Root: root}).Install(ag)
	tm := terminal.NewManager(root, ag.Emit, nil)
	tm.Install(ag)
	t.Cleanup(tm.CloseAll)
	go func() { _ = ag.Run(ctx) }()
	for i := 0; i < 200 && !reg.Online("ws1"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !reg.Online("ws1") {
		t.Fatal("agent never connected")
	}
	return ts, st, root
}

func req(t *testing.T, method, u, body string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest(method, u, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestFilesAPIEndToEnd(t *testing.T) {
	ts, st, _ := filesRig(t, domain.WSReady)
	base := ts.URL + "/api/workspaces/ws1"

	if c, b := req(t, "POST", base+"/files/dir", `{"path":"src"}`); c != 204 {
		t.Fatalf("mkdir: %d %s", c, b)
	}
	c, b := req(t, "PUT", base+"/files/content", `{"path":"src/main.go","content":"package main\n"}`)
	if c != 200 || !strings.Contains(b, `"modTime"`) {
		t.Fatalf("write: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/files/content?path="+url.QueryEscape("src/main.go"), ""); c != 200 || !strings.Contains(b, "package main") {
		t.Fatalf("read: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/files?path=src", ""); c != 200 || !strings.Contains(b, `"main.go"`) {
		t.Fatalf("list: %d %s", c, b)
	}
	// Stale write is a 409 conflict.
	if c, b = req(t, "PUT", base+"/files/content", `{"path":"src/main.go","content":"x","expectedModTime":"2001-01-01T00:00:00Z"}`); c != 409 || !strings.Contains(b, `"conflict"`) {
		t.Fatalf("conflict: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/files/content?path=nope.txt", ""); c != 404 {
		t.Fatalf("missing: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/files/search?q=main&content=true", ""); c != 200 || !strings.Contains(b, "src/main.go") {
		t.Fatalf("search: %d %s", c, b)
	}
	if c, _ = req(t, "POST", base+"/files/rename", `{"from":"src/main.go","to":"src/app.go"}`); c != 204 {
		t.Fatalf("rename: %d", c)
	}
	if c, b = req(t, "POST", base+"/exec", `{"command":"cat src/app.go && echo token=abc123 >/dev/null"}`); c != 200 || !strings.Contains(b, "package main") || !strings.Contains(b, `"exitCode":0`) {
		t.Fatalf("exec: %d %s", c, b)
	}
	if c, b = req(t, "GET", base+"/ports", ""); c != 200 || !strings.Contains(b, `"ports"`) {
		t.Fatalf("ports: %d %s", c, b)
	}
	if c, _ = req(t, "DELETE", base+"/files?path=src&recursive=true", ""); c != 204 {
		t.Fatalf("delete: %d", c)
	}

	// Audit has the mutations and never the command text or file content.
	evs, _ := st.ListAudit(context.Background(), "ws1", 50)
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Action] = true
		if e.ActorType != domain.ActorUser {
			t.Fatalf("actor type %q", e.ActorType)
		}
		for _, v := range e.Detail {
			if s, ok := v.(string); ok && (strings.Contains(s, "abc123") || strings.Contains(s, "package main")) {
				t.Fatalf("audit leaked content: %+v", e)
			}
		}
	}
	for _, a := range []string{"workspace.file_written", "workspace.dir_created", "workspace.file_renamed", "workspace.command_run", "workspace.file_deleted"} {
		if !seen[a] {
			t.Fatalf("missing audit event %s (have %v)", a, seen)
		}
	}
}

func TestFilesAPIEscapeAndGuards(t *testing.T) {
	ts, _, _ := filesRig(t, domain.WSReady)
	base := ts.URL + "/api/workspaces/ws1"
	if c, b := req(t, "GET", base+"/files/content?path="+url.QueryEscape("../../etc/passwd"), ""); c == 200 && strings.Contains(b, "root:") {
		t.Fatalf("traversal leaked /etc/passwd: %s", b)
	}
	if c, _ := req(t, "DELETE", base+"/files?path=&recursive=true", ""); c != 422 {
		t.Fatalf("root delete = %d, want 422", c)
	}
	// Another user's workspace is invisible.
	if c, _ := req(t, "GET", ts.URL+"/api/workspaces/other/files", ""); c != 404 {
		t.Fatalf("foreign workspace = %d, want 404", c)
	}
}

func TestFilesAPINotReadyOrOffline(t *testing.T) {
	ts, st, _ := filesRig(t, domain.WSStopped)
	if c, b := req(t, "GET", ts.URL+"/api/workspaces/ws1/files", ""); c != 409 || !strings.Contains(b, "workspace_not_ready") {
		t.Fatalf("stopped: %d %s", c, b)
	}
	_ = st.CreateWorkspace(context.Background(), &domain.Workspace{ID: "ws2", OwnerID: "local", Name: "two", Status: domain.WSReady})
	if c, b := req(t, "GET", ts.URL+"/api/workspaces/ws2/files", ""); c != 409 || !strings.Contains(b, "agent_offline") {
		t.Fatalf("offline: %d %s", c, b)
	}
}

func TestFilesAPIBinaryRoundTripAndLargeWrite(t *testing.T) {
	ts, _, root := filesRig(t, domain.WSReady)
	base := ts.URL + "/api/workspaces/ws1"
	bin := []byte{0x89, 'P', 'N', 'G', 0, 0xff, 0xfe, 1, 2, 3}
	enc := base64.StdEncoding.EncodeToString(bin)
	if c, b := req(t, "PUT", base+"/files/content", `{"path":"logo.png","content":"`+enc+`","encoding":"base64"}`); c != 200 {
		t.Fatalf("binary upload: %d %s", c, b)
	}
	got, _ := os.ReadFile(filepath.Join(root, "logo.png"))
	if !bytes.Equal(got, bin) {
		t.Fatalf("on-disk bytes differ: %v", got)
	}
	r, _ := http.Get(base + "/files/download?path=logo.png")
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !bytes.Equal(body, bin) || !strings.Contains(r.Header.Get("Content-Disposition"), `filename=logo.png`) || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download: %d %q %v", r.StatusCode, body, r.Header)
	}
	// Reading a binary file as text reports binary instead of garbage.
	if c, b := req(t, "GET", base+"/files/content?path=logo.png", ""); c != 200 || !strings.Contains(b, `"binary":true`) {
		t.Fatalf("binary read: %d %s", c, b)
	}
	// A ~4.5 MB file travels through the whole path.
	big := strings.Repeat("0123456789abcdef", 280000)
	payload, _ := json.Marshal(map[string]string{"path": "big.txt", "content": big})
	if c, b := req(t, "PUT", base+"/files/content", string(payload)); c != 200 {
		t.Fatalf("big write: %d %.200s", c, b)
	}
	if fi, err := os.Stat(filepath.Join(root, "big.txt")); err != nil || fi.Size() != int64(len(big)) {
		t.Fatalf("big file on disk: %v %v", fi, err)
	}
	if c, _ := req(t, "GET", base+"/files/download?path=nope", ""); c != 404 {
		t.Fatalf("download missing = %d", c)
	}
}
