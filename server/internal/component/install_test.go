package component

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type ifx struct {
	*fixture
	drv *runtime.Fake
}

func newInstallFixture(t *testing.T, ramGB int) *ifx {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200})
	eng := ops.NewEngine(st, bus, log, 2)
	svc := NewService(st, cat, bus, audit.New(st, log)).WithRuntime(drv, eng)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	eng.Start(ctx)
	ws := &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady, CPU: 4, RAMGB: ramGB, StorageGB: 40, Runtimes: map[string]domain.RuntimeState{}}
	_ = st.CreateWorkspace(ctx, ws)
	_ = drv.CreateWorkspace(ctx, runtime.WorkspaceSpec{ID: "ws1", Resources: runtime.Resources{CPU: 1, RAMGB: 1, StorageGB: 5}}, func(string, string) {})
	return &ifx{&fixture{svc, st, ctx, ws, bus}, drv}
}

func (f *ifx) add(t *testing.T, id string) *domain.Component {
	c, err := f.svc.Add(f.ctx, "u1", "ws1", id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *ifx) wait(t *testing.T, id, state string) *domain.Component {
	t.Helper()
	for i := 0; i < 300; i++ {
		c, err := f.st.GetComponent(f.ctx, id)
		if err == nil && c.Observed == state {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, _ := f.st.GetComponent(f.ctx, id)
	t.Fatalf("component state %v, want %s", c, state)
	return nil
}

func errCode(err error) string {
	var ve *runtime.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func TestInstallServiceRequiresConfirmationThenRuns(t *testing.T) {
	f := newInstallFixture(t, 8)
	c := f.add(t, "redis")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "7.4.1", Start: true}); errCode(err) != "confirmation_required" {
		t.Fatalf("%v", err)
	}
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateDraft || got.Mode != "" {
		t.Fatal("nothing may change before confirmation")
	}
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "7.4.1", Start: true, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	got := f.wait(t, c.ID, domain.StateRunning)
	if got.Mode != domain.ModeInstalled || got.Version != "7.4.1" || got.Settings["port"].(float64) != 6379 {
		t.Fatalf("%+v", got)
	}
	if o, _ := f.drv.InspectService(f.ctx, "ws1", "redis"); !o.Running {
		t.Fatal("service container not running")
	}
}

func TestInstallWithoutStartLeavesServiceStopped(t *testing.T) {
	f := newInstallFixture(t, 8)
	c := f.add(t, "redis")
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "7.4.1", Confirm: true})
	f.wait(t, c.ID, domain.StateStopped)
	if o, _ := f.drv.InspectService(f.ctx, "ws1", "redis"); !o.Exists || o.Running {
		t.Fatalf("%+v", o)
	}
	// Start and stop are separate lifecycle actions.
	if _, err := f.svc.Start(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, c.ID, domain.StateRunning)
	if _, err := f.svc.Stop(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, c.ID, domain.StateStopped)
}

func TestInstallPrerequisites(t *testing.T) {
	f := newInstallFixture(t, 1) // 1 GB workspace
	redis := f.add(t, "redis")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", redis.ID, InstallInput{Version: "7.4.1", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	f.wait(t, redis.ID, domain.StateStopped)
	kafka := f.add(t, "kafka") // needs 1 GB; only 768 MB are left
	_, err := f.svc.Install(f.ctx, "u1", "ws1", kafka.ID, InstallInput{Version: "3.8.1", Confirm: true})
	if errCode(err) != "insufficient_resources" || !strings.Contains(err.Error(), "needs 1 GB RAM; this workspace has 0.8 GB free") {
		t.Fatalf("kafka with 768 MB free: %v", err)
	}
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", kafka.ID, InstallInput{Version: "9.9", Confirm: true}); errCode(err) != "unsupported_version" {
		t.Fatalf("version: %v", err)
	}
	nest := f.add(t, "nestjs")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", nest.ID, InstallInput{Version: "10.4.7", Confirm: true}); errCode(err) != "missing_dependency" {
		t.Fatalf("nest without node: %v", err)
	}
	mongo := f.add(t, "mongodb")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", mongo.ID, InstallInput{Version: "8.0.4", Settings: map[string]any{"port": 70000}, Confirm: true}); errCode(err) != "invalid_port" {
		t.Fatalf("port range: %v", err)
	}
}

func TestPortConflictAndDuplicateInstall(t *testing.T) {
	f := newInstallFixture(t, 16)
	a := f.add(t, "redis")
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", a.ID, InstallInput{Version: "7.4.1", Start: true, Confirm: true})
	f.wait(t, a.ID, domain.StateRunning)
	b := f.add(t, "rabbitmq")
	_, err := f.svc.Install(f.ctx, "u1", "ws1", b.ID, InstallInput{Version: "4.0.4", Settings: map[string]any{"port": 6379}, Confirm: true})
	if errCode(err) != "port_conflict" || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("%v", err)
	}
	dup := f.add(t, "redis")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", dup.ID, InstallInput{Version: "7.4.1", Confirm: true}); errCode(err) != "already_installed" {
		t.Fatalf("%v", err)
	}
}

func TestStartFailureThenRetry(t *testing.T) {
	f := newInstallFixture(t, 16)
	f.drv.BusyPorts[9092] = true
	c := f.add(t, "kafka")
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "3.8.1", Start: true, Confirm: true})
	got := f.wait(t, c.ID, domain.StateFailed)
	if got.Message != "start failed: port 9092 already in use" {
		t.Fatal(got.Message)
	}
	delete(f.drv.BusyPorts, 9092)
	if _, err := f.svc.Retry(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, c.ID, domain.StateRunning)
}

func TestRuntimeInstallRecordsVersionsAndEnablesFrameworks(t *testing.T) {
	f := newInstallFixture(t, 8)
	if _, err := f.svc.InstallRuntime(f.ctx, "u1", "ws1", "nodejs", RuntimeInstall{Version: "22.11.0", Activate: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		w, _ := f.st.GetWorkspace(f.ctx, "ws1")
		if w.Runtimes["nodejs"].Active == "22.11.0" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := f.st.GetWorkspace(f.ctx, "ws1")
	if r := w.Runtimes["nodejs"]; r.Active != "22.11.0" || len(r.Installed) != 1 {
		t.Fatalf("%+v", r)
	}
	if len(f.drv.ExecLog) == 0 || !strings.Contains(f.drv.ExecLog[0], "mise install node@22.11.0") || !strings.Contains(f.drv.ExecLog[0], "mise use -g node@22.11.0") {
		t.Fatalf("exec log: %v", f.drv.ExecLog)
	}
	if _, err := f.svc.InstallRuntime(f.ctx, "u1", "ws1", "nodejs", RuntimeInstall{Version: "1.0"}); errCode(err) != "unsupported_version" {
		t.Fatalf("%v", err)
	}
	if _, err := f.svc.InstallRuntime(f.ctx, "u1", "ws1", "redis", RuntimeInstall{Version: "7.4.1"}); errCode(err) != "unknown_runtime" {
		t.Fatalf("%v", err)
	}
	// NestJS can now be installed, as a pinned npm package set.
	n := f.add(t, "nestjs")
	if _, err := f.svc.Install(f.ctx, "u1", "ws1", n.ID, InstallInput{Version: "10.4.7", Start: false, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	f.wait(t, n.ID, domain.StateReady)
	last := f.drv.ExecLog[len(f.drv.ExecLog)-1]
	if !strings.Contains(last, "'@nestjs/core@10.4.7'") {
		t.Fatalf("framework install command: %s", last)
	}
}

func TestInstallingRuntimeCardRecordsRuntime(t *testing.T) {
	f := newInstallFixture(t, 8)
	c := f.add(t, "python")
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "3.12.7", Start: true, Confirm: true})
	f.wait(t, c.ID, domain.StateReady)
	if w, _ := f.st.GetWorkspace(f.ctx, "ws1"); w.Runtimes["python"].Active != "3.12.7" {
		t.Fatalf("%+v", w.Runtimes)
	}
}

func TestRemoveInstalledDeletesCardAndService(t *testing.T) {
	f := newInstallFixture(t, 8)
	c := f.add(t, "redis")
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "7.4.1", Start: true, Confirm: true})
	f.wait(t, c.ID, domain.StateRunning)
	if err := f.svc.Remove(f.ctx, "u1", "ws1", c.ID); !errors.Is(err, ErrBadComponent) {
		t.Fatalf("direct delete of running component: %v", err)
	}
	if _, err := f.svc.RemoveInstalled(f.ctx, "u1", "ws1", c.ID, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if _, err := f.st.GetComponent(f.ctx, c.ID); errors.Is(err, store.ErrNotFound) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := f.st.GetComponent(f.ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("card not removed")
	}
	if o, _ := f.drv.InspectService(f.ctx, "ws1", "redis"); o.Exists {
		t.Fatal("service not removed")
	}
}

func TestCapacityReflectsInstalledComponents(t *testing.T) {
	f := newInstallFixture(t, 4)
	c := f.add(t, "mongodb")
	before, _ := f.svc.Capacity(f.ctx, "u1", "ws1")
	if before.FreeRAMMB != 4096 {
		t.Fatalf("%+v", before)
	}
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "8.0.4", Start: true, Confirm: true})
	f.wait(t, c.ID, domain.StateRunning)
	after, _ := f.svc.Capacity(f.ctx, "u1", "ws1")
	if after.FreeRAMMB != 3072 || after.UsedStore != 10 {
		t.Fatalf("%+v", after)
	}
}

func TestInstallScriptsUseOnlyCatalogValues(t *testing.T) {
	cat, _ := catalog.Load("../../../catalog")
	for _, id := range []string{"nodejs", "python", "go", "react", "nestjs", "angular", "fastapi", "certbot", "bash", "shell", "htmlcss", "javascript"} {
		def, _ := cat.Get(id)
		if def.Install.Strategy == "image" {
			continue
		}
		script, err := installScript(def, def.Versions.Default)
		if err != nil || script == "" {
			t.Errorf("%s: %q %v", id, script, err)
		}
	}
	if s := shellQuoteAll([]string{"a'b"}); s != `'a'\''b'` {
		t.Fatal(s)
	}
}

func TestReconcileInstalledShowsRealState(t *testing.T) {
	f := newInstallFixture(t, 8)
	c := f.add(t, "redis")
	_, _ = f.svc.Install(f.ctx, "u1", "ws1", c.ID, InstallInput{Version: "7.4.1", Start: true, Confirm: true})
	f.wait(t, c.ID, domain.StateRunning)

	f.svc.ReconcileInstalled(f.ctx)
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateRunning {
		t.Fatalf("healthy service must stay running: %+v", got)
	}
	// The container is stopped behind the control plane's back.
	_ = f.drv.StopService(f.ctx, "ws1", "redis")
	f.svc.ReconcileInstalled(f.ctx)
	got, _ := f.st.GetComponent(f.ctx, c.ID)
	if got.Observed != domain.StateFailed || !strings.Contains(got.Message, "stopped unexpectedly") {
		t.Fatalf("%+v", got)
	}
	// A failed card is owned by its operation: reconciliation leaves it alone.
	_ = f.drv.StartService(f.ctx, "ws1", "redis")
	f.svc.ReconcileInstalled(f.ctx)
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateFailed {
		t.Fatalf("failed state should wait for Retry: %+v", got)
	}
	// Stopped by the user, then started outside: observed catches up.
	f.drv.ExecLog = nil
	_, _ = f.st.UpdateComponent(f.ctx, c.ID, func(x *domain.Component) error { x.Observed = domain.StateStopped; x.Message = ""; return nil })
	f.svc.ReconcileInstalled(f.ctx)
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateRunning {
		t.Fatalf("%+v", got)
	}
	// Container removed on the backend.
	_ = f.drv.RemoveService(f.ctx, "ws1", "redis", false)
	f.svc.ReconcileInstalled(f.ctx)
	if got, _ := f.st.GetComponent(f.ctx, c.ID); got.Observed != domain.StateFailed || !strings.Contains(got.Message, "no longer exists") {
		t.Fatalf("%+v", got)
	}
}
