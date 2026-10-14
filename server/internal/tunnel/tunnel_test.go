package tunnel_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/tunnel"
)

func rig(t *testing.T) (*tunnel.Dialer, *agentapi.Registry, context.Context) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := store.NewMemory()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u", Name: "n", Status: domain.WSReady})
	reg := agentapi.NewRegistry(st, events.NewBus())
	api := httpapi.New(log)
	reg.Register(api)
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	tok, _ := reg.IssueToken(ctx, "ws1")
	ag := agent.New(agent.Config{ControlURL: ts.URL, WorkspaceID: "ws1", Token: tok}, log)
	stop, err := ag.Serve(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	go func() { _ = ag.Run(ctx) }()
	for i := 0; i < 200 && !reg.Online("ws1"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !reg.Online("ws1") {
		t.Fatal("agent offline")
	}
	return tunnel.NewDialer(reg), reg, ctx
}

func TestHTTPThroughTunnelToLoopbackOnlyServer(t *testing.T) {
	d, _, ctx := rig(t)
	// A dev server bound to loopback only: unreachable from outside, reachable via the agent.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Host", r.Host)
		_, _ = w.Write([]byte("hello " + r.URL.Path + " " + string(b)))
	})}
	go srv.Serve(l)
	defer srv.Close()
	port := l.Addr().(*net.TCPAddr).Port

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(c context.Context, _, _ string) (net.Conn, error) { return d.DialContext(c, "ws1", port) }}}
	res, err := client.Post("http://workspace/api/x", "text/plain", strings.NewReader(strings.Repeat("A", 200_000)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.HasPrefix(string(body), "hello /api/x AAAA") || len(body) != len("hello /api/x ")+200_000 {
		t.Fatalf("body len %d", len(body))
	}
	_ = ctx
}

func TestClosedPortAndStreamLifecycle(t *testing.T) {
	d, reg, ctx := rig(t)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if _, err := d.DialContext(ctx, "ws1", closedPort); err == nil || !strings.Contains(err.Error(), "Nothing is listening") {
		t.Fatalf("closed port: %v", err)
	}
	if _, err := d.DialContext(ctx, "ws1", 0); err == nil {
		t.Fatal("port 0 accepted")
	}

	// Raw echo-then-close server: remote close surfaces as EOF locally.
	e, _ := net.Listen("tcp", "127.0.0.1:0")
	defer e.Close()
	go func() {
		c, _ := e.Accept()
		line, _ := bufio.NewReader(c).ReadString('\n')
		_, _ = c.Write([]byte("echo:" + line))
		c.Close()
	}()
	c, err := d.DialContext(ctx, "ws1", e.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("ping\n"))
	b, err := io.ReadAll(c)
	if err != nil || string(b) != "echo:ping\n" {
		t.Fatalf("read: %q %v", b, err)
	}

	// Dropping the agent link closes tunnelled connections instead of hanging them.
	hold, _ := net.Listen("tcp", "127.0.0.1:0")
	defer hold.Close()
	go func() { c, _ := hold.Accept(); time.Sleep(30 * time.Second); c.Close() }()
	c2, err := d.DialContext(ctx, "ws1", hold.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c2.Read(make([]byte, 1)); done <- err }()
	time.Sleep(100 * time.Millisecond)
	reg.Drop("ws1")
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("want EOF, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tunnelled read hung after the link dropped")
	}
}

func TestReadDeadline(t *testing.T) {
	d, _, ctx := rig(t)
	hold, _ := net.Listen("tcp", "127.0.0.1:0")
	defer hold.Close()
	go func() { c, _ := hold.Accept(); time.Sleep(5 * time.Second); c.Close() }()
	c, err := d.DialContext(ctx, "ws1", hold.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	start := time.Now()
	if _, err := c.Read(make([]byte, 1)); err == nil || time.Since(start) > time.Second {
		t.Fatalf("deadline not honoured: %v after %v", err, time.Since(start))
	}
}
