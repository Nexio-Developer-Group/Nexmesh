package component

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

type fixture struct {
	svc *Service
	st  store.Store
	ctx context.Context
	ws  *domain.Workspace
	bus *events.Bus
}

func newFixture(t *testing.T) *fixture {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	bus := events.NewBus()
	svc := NewService(st, cat, bus, audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))))
	ctx := context.Background()
	ws := &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady}
	_ = st.CreateWorkspace(ctx, ws)
	return &fixture{svc, st, ctx, ws, bus}
}

func TestAddCreatesDraftCard(t *testing.T) {
	f := newFixture(t)
	ch, cancel := f.bus.Subscribe("ws1")
	defer cancel()
	c, err := f.svc.Add(f.ctx, "u1", "ws1", "mongodb", 120, 80)
	if err != nil {
		t.Fatal(err)
	}
	if c.Observed != domain.StateDraft || c.Mode != domain.ModeUnset || c.Version != "8.0.4" || c.X != 120 {
		t.Fatalf("%+v", c)
	}
	if ev := <-ch; ev.Type != "component" {
		t.Fatalf("event %s", ev.Type)
	}
}

func TestAddValidation(t *testing.T) {
	f := newFixture(t)
	var ve *runtime.ValidationError
	if _, err := f.svc.Add(f.ctx, "u1", "ws1", "nope", 0, 0); !errors.As(err, &ve) || ve.Code != "unknown_component" {
		t.Fatalf("%v", err)
	}
	if _, err := f.svc.Add(f.ctx, "intruder", "ws1", "redis", 0, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user add: %v", err)
	}
	_, _ = f.st.UpdateWorkspace(f.ctx, "ws1", func(w *domain.Workspace) error { w.Status = domain.WSStopped; return nil })
	if _, err := f.svc.Add(f.ctx, "u1", "ws1", "redis", 0, 0); !errors.Is(err, ErrWorkspaceState) {
		t.Fatalf("stopped workspace: %v", err)
	}
}

func TestUpdateAndRemove(t *testing.T) {
	f := newFixture(t)
	c, _ := f.svc.Add(f.ctx, "u1", "ws1", "mongodb", 0, 0)
	x := 10.5
	v := "7.0.14"
	got, err := f.svc.Update(f.ctx, "u1", "ws1", c.ID, Patch{X: &x, Version: &v})
	if err != nil || got.X != 10.5 || got.Version != "7.0.14" {
		t.Fatalf("%v %+v", err, got)
	}
	bad := "1.0"
	var ve *runtime.ValidationError
	if _, err := f.svc.Update(f.ctx, "u1", "ws1", c.ID, Patch{Version: &bad}); !errors.As(err, &ve) {
		t.Fatalf("%v", err)
	}
	if err := f.svc.Remove(f.ctx, "u1", "ws1", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Get(f.ctx, "u1", "ws1", c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("not removed")
	}
}

func TestRunningComponentCannotBeDeletedDirectly(t *testing.T) {
	f := newFixture(t)
	c, _ := f.svc.Add(f.ctx, "u1", "ws1", "redis", 0, 0)
	_, _ = f.st.UpdateComponent(f.ctx, c.ID, func(c *domain.Component) error { c.Observed = domain.StateRunning; return nil })
	if err := f.svc.Remove(f.ctx, "u1", "ws1", c.ID); !errors.Is(err, ErrBadComponent) {
		t.Fatalf("%v", err)
	}
}

func TestSummary(t *testing.T) {
	f := newFixture(t)
	if f.svc.Summary(f.ctx, "ws1") != "" {
		t.Fatal("empty workspace has no summary")
	}
	a, _ := f.svc.Add(f.ctx, "u1", "ws1", "redis", 0, 0)
	b, _ := f.svc.Add(f.ctx, "u1", "ws1", "kafka", 0, 0)
	_, _ = f.st.UpdateComponent(f.ctx, a.ID, func(c *domain.Component) error { c.Mode = domain.ModeConnected; return nil })
	_, _ = f.st.UpdateComponent(f.ctx, b.ID, func(c *domain.Component) error { c.Observed = domain.StateFailed; return nil })
	if got := f.svc.Summary(f.ctx, "ws1"); got != "2 components · 1 connected · 1 failed" {
		t.Fatal(got)
	}
}
