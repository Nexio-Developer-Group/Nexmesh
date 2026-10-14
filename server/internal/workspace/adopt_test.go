package workspace

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func TestAdoptReRegistersWorkspacesTheBackendForgot(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 8, MaxRAMGB: 16, MaxStorageGB: 100})
	svc := NewService(st, drv, ops.NewEngine(st, events.NewBus(), log, 1), events.NewBus(), audit.New(st, log), log)
	mk := func(id, status string) {
		_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: id, OwnerID: "u", Name: id, Status: status, CPU: 1, RAMGB: 1, StorageGB: 5})
	}
	mk("ready1", domain.WSReady)
	mk("stopped1", domain.WSStopped)
	mk("failed1", domain.WSFailed)

	got := svc.Adopt(ctx)
	if len(got) != 2 {
		t.Fatalf("adopted %v, want the ready and stopped workspaces only", got)
	}
	if o, _ := drv.InspectWorkspace(ctx, "ready1"); !o.Exists || !o.Running {
		t.Fatalf("ready workspace should be running: %+v", o)
	}
	if o, _ := drv.InspectWorkspace(ctx, "stopped1"); !o.Exists || o.Running {
		t.Fatalf("stopped workspace should exist but stay stopped: %+v", o)
	}
	if o, _ := drv.InspectWorkspace(ctx, "failed1"); o.Exists {
		t.Fatal("failed workspace must not be adopted")
	}
	if again := svc.Adopt(ctx); len(again) != 0 {
		t.Fatalf("second adopt should be a no-op, got %v", again)
	}
}
