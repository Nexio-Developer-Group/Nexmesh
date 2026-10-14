package workspace

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/idle"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type idleRig struct {
	svc     *Service
	st      store.Store
	tr      *idle.Tracker
	now     *time.Time
	ctx     context.Context
	busy    *bool
	checked *int
}

func newIdleRig(t *testing.T) *idleRig {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := store.NewMemory()
	bus := events.NewBus()
	drv := runtime.NewFake(runtime.Capabilities{MaxCPU: 8, MaxRAMGB: 16, MaxStorageGB: 100})
	eng := ops.NewEngine(st, bus, log, 2)
	svc := NewService(st, drv, eng, bus, audit.New(st, log), log)
	eng.Start(ctx)
	now := time.Now()
	tr := idle.NewTracker()
	tr.SetClock(func() time.Time { return now })
	busy, checked := false, 0
	svc.WithIdle(tr, func(context.Context, string) (bool, []string) { checked++; return busy, []string{"a process is running in Terminal 1"} })
	w := &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady, CPU: 2, RAMGB: 4, StorageGB: 20, IdleStopMinutes: 30}
	_ = st.CreateWorkspace(ctx, w)
	_ = drv.CreateWorkspace(ctx, runtime.WorkspaceSpec{ID: "ws1", Resources: runtime.Resources{CPU: 2, RAMGB: 4, StorageGB: 20}}, func(string, string) {})
	return &idleRig{svc: svc, st: st, tr: tr, now: &now, ctx: ctx, busy: &busy, checked: &checked}
}

func (r *idleRig) waitStatus(t *testing.T, want string) *domain.Workspace {
	t.Helper()
	for i := 0; i < 200; i++ {
		if w, _ := r.st.GetWorkspace(r.ctx, "ws1"); w.Status == want {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := r.st.GetWorkspace(r.ctx, "ws1")
	t.Fatalf("status %q never became %q", w.Status, want)
	return nil
}

func TestIdleWorkspaceIsStoppedWithReasonAndAudit(t *testing.T) {
	r := newIdleRig(t)
	*r.now = r.now.Add(29 * time.Minute)
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 {
		t.Fatalf("stopped before the timeout: %v", got)
	}
	*r.now = r.now.Add(2 * time.Minute)
	if got := r.svc.StopIdle(r.ctx); len(got) != 1 {
		t.Fatalf("not stopped after the timeout: %v", got)
	}
	w := r.waitStatus(t, domain.WSStopped)
	if w.StopReason != "idle" {
		t.Fatalf("stop reason = %q", w.StopReason)
	}
	evs, _ := r.st.ListAudit(r.ctx, "ws1", 10)
	if len(evs) != 1 || evs[0].Action != "workspace.idle_stop" || evs[0].ActorType != domain.ActorSystem || evs[0].Detail["limitMinutes"] != 30 {
		t.Fatalf("audit: %+v", evs)
	}
	// Starting again resumes and clears the reason.
	if _, err := r.svc.Start(r.ctx, "u1", "ws1"); err != nil {
		t.Fatal(err)
	}
	if w = r.waitStatus(t, domain.WSReady); w.StopReason != "" {
		t.Fatalf("reason not cleared: %q", w.StopReason)
	}
	// A user stop is recorded as such.
	if _, err := r.svc.Stop(r.ctx, "u1", "ws1"); err != nil {
		t.Fatal(err)
	}
	if w = r.waitStatus(t, domain.WSStopped); w.StopReason != "user" {
		t.Fatalf("user stop reason = %q", w.StopReason)
	}
}

func TestActivityKeepsWorkspaceAwake(t *testing.T) {
	r := newIdleRig(t)
	*r.now = r.now.Add(25 * time.Minute)
	r.tr.Touch("ws1") // a request arrives
	*r.now = r.now.Add(25 * time.Minute)
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 {
		t.Fatalf("stopped 25 minutes after activity with a 30 minute limit: %v", got)
	}
	// An open connection (terminal, event stream) means never idle, however long.
	end := r.tr.Open("ws1")
	*r.now = r.now.Add(10 * time.Hour)
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 {
		t.Fatalf("stopped with an open connection: %v", got)
	}
	end()
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 {
		t.Fatalf("closing a connection counts as activity: %v", got)
	}
	*r.now = r.now.Add(31 * time.Minute)
	if got := r.svc.StopIdle(r.ctx); len(got) != 1 {
		t.Fatalf("should stop once idle again: %v", got)
	}
}

func TestBusyAgentBlocksStopAndDisabledNeverStops(t *testing.T) {
	r := newIdleRig(t)
	*r.now = r.now.Add(2 * time.Hour)
	*r.busy = true
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 || *r.checked != 1 {
		t.Fatalf("busy workspace stopped: %v (checked %d)", got, *r.checked)
	}
	if w, _ := r.st.GetWorkspace(r.ctx, "ws1"); w.Status != domain.WSReady {
		t.Fatalf("status %s", w.Status)
	}
	*r.busy = false
	_, _ = r.st.UpdateWorkspace(r.ctx, "ws1", func(w *domain.Workspace) error { w.IdleStopMinutes = 0; return nil })
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 {
		t.Fatalf("idle stop disabled but stopped: %v", got)
	}
	// Enabling it again, a pass right after enqueueing does not enqueue twice.
	_, _ = r.st.UpdateWorkspace(r.ctx, "ws1", func(w *domain.Workspace) error { w.IdleStopMinutes = 30; return nil })
	if got := r.svc.StopIdle(r.ctx); len(got) != 1 {
		t.Fatalf("first pass: %v", got)
	}
	if got := r.svc.StopIdle(r.ctx); len(got) != 0 {
		t.Fatalf("double enqueue: %v", got)
	}
}

func TestIdleSettingValidationAndDefault(t *testing.T) {
	r := newIdleRig(t)
	set := func(m int) error {
		_, err := r.svc.ApplySettings(r.ctx, "u1", "ws1", SettingsChange{IdleStopMinutes: &m})
		return err
	}
	for _, bad := range []int{1, 4, 1441, -5} {
		if err := set(bad); err == nil || !strings.Contains(err.Error(), "5 minutes and 24 hours") {
			t.Errorf("%d accepted: %v", bad, err)
		}
	}
	if err := set(120); err != nil {
		t.Fatal(err)
	}
	if w, _ := r.st.GetWorkspace(r.ctx, "ws1"); w.IdleStopMinutes != 120 || w.RestartRequired {
		t.Fatalf("setting not applied live: %+v", w)
	}
	if err := set(0); err != nil {
		t.Fatal(err)
	}
	r.svc.WithDefaultIdle(45)
	w, err := r.svc.Create(r.ctx, "u1", CreateInput{Name: "fresh", CPU: 1, RAMGB: 2, StorageGB: 10})
	if err != nil || w.IdleStopMinutes != 45 {
		t.Fatalf("default idle: %v %+v", err, w)
	}
}
