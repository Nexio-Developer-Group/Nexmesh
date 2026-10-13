package workspace

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type env struct {
	svc *Service
	st  store.Store
	drv *runtime.Fake
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200})
	eng := ops.NewEngine(st, bus, log, 2)
	svc := NewService(st, drv, eng, bus, audit.New(st, log), log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eng.Start(ctx)
	return &env{svc, st, drv, ctx}
}

func (e *env) waitStatus(t *testing.T, id, want string) *domain.Workspace {
	t.Helper()
	for i := 0; i < 300; i++ {
		w, _ := e.st.GetWorkspace(e.ctx, id)
		if w.Status == want {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := e.st.GetWorkspace(e.ctx, id)
	t.Fatalf("status %s (%s), want %s", w.Status, w.Error, want)
	return nil
}

func TestCreateProvisionsToReady(t *testing.T) {
	e := newEnv(t)
	w, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: "orders-api", CPU: 4, RAMGB: 8, StorageGB: 40})
	if err != nil {
		t.Fatal(err)
	}
	if w.OS != domain.OSUbuntu2404 || w.Status != domain.WSAllocating {
		t.Fatalf("%+v", w)
	}
	e.waitStatus(t, w.ID, domain.WSReady)
	obs, _ := e.drv.InspectWorkspace(e.ctx, w.ID)
	if !obs.Running {
		t.Fatal("container not running")
	}
	aud, _ := e.st.ListAudit(e.ctx, w.ID, 10)
	if len(aud) == 0 || aud[len(aud)-1].Action != "workspace.create" {
		t.Fatalf("audit %+v", aud)
	}
}

func TestCreateRejectsInvalid(t *testing.T) {
	e := newEnv(t)
	var ve *runtime.ValidationError
	if _, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: "Bad Name", CPU: 4, RAMGB: 8, StorageGB: 40}); !errors.As(err, &ve) || ve.Code != "invalid_name" {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: "big", CPU: 4, RAMGB: 64, StorageGB: 40}); !errors.As(err, &ve) || ve.Code != "ram_out_of_range" {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: "gpu-ws", CPU: 4, RAMGB: 8, StorageGB: 40, GPU: true}); !errors.As(err, &ve) || ve.Code != "gpu_unavailable" {
		t.Fatalf("%v", err)
	}
	list, _ := e.svc.List(e.ctx, "u1")
	if len(list) != 0 {
		t.Fatal("rejected requests must not create workspaces")
	}
	if _, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: "dup", CPU: 1, RAMGB: 1, StorageGB: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.ctx, "u1", CreateInput{Name: "dup", CPU: 1, RAMGB: 1, StorageGB: 5}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("%v", err)
	}
	if ok, _ := e.svc.NameAvailable(e.ctx, "u1", "dup"); ok {
		t.Fatal("name should be taken")
	}
}

func TestProvisionFailureIsReported(t *testing.T) {
	e := newEnv(t)
	e.drv.FailNext["create"] = errors.New("host has 11.2 GB free")
	w, _ := e.svc.Create(e.ctx, "u1", CreateInput{Name: "legacy-cms", CPU: 2, RAMGB: 16, StorageGB: 80})
	got := e.waitStatus(t, w.ID, domain.WSFailed)
	if got.Error == "" {
		t.Fatal("failure reason missing")
	}
}

func TestStopStartDelete(t *testing.T) {
	e := newEnv(t)
	w, _ := e.svc.Create(e.ctx, "u1", CreateInput{Name: "checkout", CPU: 2, RAMGB: 4, StorageGB: 20})
	e.waitStatus(t, w.ID, domain.WSReady)
	if _, err := e.svc.Start(e.ctx, "u1", w.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("start while ready: %v", err)
	}
	_, _ = e.svc.Stop(e.ctx, "u1", w.ID)
	e.waitStatus(t, w.ID, domain.WSStopped)
	_, _ = e.svc.Start(e.ctx, "u1", w.ID)
	e.waitStatus(t, w.ID, domain.WSReady)
	if err := e.svc.Delete(e.ctx, "u1", w.ID, "wrong"); !errors.Is(err, ErrConfirmName) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Get(e.ctx, "other-user", w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("other users must not see the workspace")
	}
	if err := e.svc.Delete(e.ctx, "u1", w.ID, "checkout"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.GetWorkspace(e.ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("not deleted")
	}
	if o, _ := e.drv.InspectWorkspace(e.ctx, w.ID); o.Exists {
		t.Fatal("container not removed")
	}
}

func TestReconcileDetectsDrift(t *testing.T) {
	e := newEnv(t)
	w, _ := e.svc.Create(e.ctx, "u1", CreateInput{Name: "drift", CPU: 1, RAMGB: 1, StorageGB: 5})
	e.waitStatus(t, w.ID, domain.WSReady)
	_ = e.drv.StopWorkspace(e.ctx, w.ID) // stopped behind our back
	e.svc.Reconcile(e.ctx)
	e.waitStatus(t, w.ID, domain.WSStopped)
	_ = e.drv.RemoveWorkspace(e.ctx, w.ID)
	e.svc.Reconcile(e.ctx)
	e.waitStatus(t, w.ID, domain.WSFailed)
}
