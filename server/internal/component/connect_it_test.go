package component

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// End to end through the real prober against a real Redis (NEXMESH_IT=1, see scripts/it).
func TestConnectToRealRedisEndToEnd(t *testing.T) {
	if os.Getenv("NEXMESH_IT") != "1" {
		t.Skip("integration test")
	}
	port, _ := strconv.Atoi(os.Getenv("NX_IT_REDIS_PORT"))
	pw := os.Getenv("NX_IT_PASS")
	cat, _ := catalog.Load("../../../catalog")
	st := store.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := bytes.Repeat([]byte{3}, 32)
	be, _ := secrets.NewStoreBackend(st, key)
	svc := NewService(st, cat, events.NewBus(), audit.New(st, log)).WithConnect(ConnectConfig{
		Secrets: secrets.New(be), Guard: netguard.New(netguard.Policy{AllowLoopback: true}), FingerprintKey: key, Log: log})
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	c, _ := svc.Add(ctx, "u1", "ws1", "redis", 0, 0)

	in := ConnectionInput{ConnName: "shared-cache", Environment: "staging", Fields: map[string]any{"host": "127.0.0.1", "port": port, "password": "wrong-password-xyz"}}
	res, err := svc.Test(ctx, "u1", "ws1", c.ID, in)
	if err != nil || res.Passed || res.FailedStage != "authentication" {
		t.Fatalf("%v %+v", err, res)
	}
	svc.conn.tests = map[string][]time.Time{}
	in.Fields["password"] = pw
	res, err = svc.Test(ctx, "u1", "ws1", c.ID, in)
	if err != nil || !res.Passed {
		t.Fatalf("%v %+v", err, res)
	}
	added, err := svc.Save(ctx, "u1", "ws1", c.ID, in)
	if err != nil || added.Observed != domain.StateConnected {
		t.Fatalf("%v %+v", err, added)
	}
	env, _ := svc.ConnectionEnv(ctx, "ws1")
	if env["SHARED_CACHE_PASSWORD"] != pw || env["SHARED_CACHE_HOST"] != "127.0.0.1" {
		t.Fatalf("%v", env)
	}
	svc.conn.tests = map[string][]time.Time{}
	if res, err := svc.Retest(ctx, "u1", "ws1", c.ID); err != nil || !res.Passed {
		t.Fatalf("%v %+v", err, res)
	}
}
