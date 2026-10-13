package ops

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func setup(t *testing.T) (*Engine, store.Store, context.Context) {
	st := store.NewMemory()
	e := NewEngine(st, events.NewBus(), slog.New(slog.NewTextHandler(io.Discard, nil)), 4)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return e, st, ctx
}

func waitStatus(t *testing.T, st store.Store, id, want string) *domain.Operation {
	t.Helper()
	for i := 0; i < 200; i++ {
		o, _ := st.GetOperation(context.Background(), id)
		if o.Status == want {
			return o
		}
		time.Sleep(10 * time.Millisecond)
	}
	o, _ := st.GetOperation(context.Background(), id)
	t.Fatalf("op status %s, want %s (err=%s)", o.Status, want, o.Error)
	return nil
}

func TestSuccessFailurePanic(t *testing.T) {
	e, st, ctx := setup(t)
	e.Register("ok", func(_ context.Context, _ *domain.Operation, p Progress) error { p.Logf("hello"); p.Set(50); return nil })
	e.Register("bad", func(context.Context, *domain.Operation, Progress) error { return errors.New("boom") })
	e.Register("panic", func(context.Context, *domain.Operation, Progress) error { panic("oh no") })
	e.Start(ctx)
	ok, _ := e.Enqueue(ctx, "w1", "", "ok")
	bad, _ := e.Enqueue(ctx, "w2", "", "bad")
	pn, _ := e.Enqueue(ctx, "w3", "", "panic")
	o := waitStatus(t, st, ok.ID, domain.OpSucceeded)
	if len(o.Log) != 1 || o.Progress != 100 {
		t.Fatalf("%+v", o)
	}
	if o := waitStatus(t, st, bad.ID, domain.OpFailed); o.Error != "boom" {
		t.Fatal(o.Error)
	}
	waitStatus(t, st, pn.ID, domain.OpFailed)
}

func TestSerialWithinWorkspace(t *testing.T) {
	e, st, ctx := setup(t)
	var mu sync.Mutex
	var order []string
	active := 0
	e.Register("slow", func(_ context.Context, op *domain.Operation, _ Progress) error {
		mu.Lock()
		active++
		if active > 1 {
			t.Error("two ops ran concurrently in one workspace")
		}
		order = append(order, op.ID)
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return nil
	})
	e.Start(ctx)
	a, _ := e.Enqueue(ctx, "w", "", "slow")
	b, _ := e.Enqueue(ctx, "w", "", "slow")
	waitStatus(t, st, a.ID, domain.OpSucceeded)
	waitStatus(t, st, b.ID, domain.OpSucceeded)
	if order[0] != a.ID {
		t.Fatalf("order %v", order)
	}
}

func TestCancelRunning(t *testing.T) {
	e, st, ctx := setup(t)
	started := make(chan struct{})
	e.Register("wait", func(c context.Context, _ *domain.Operation, _ Progress) error {
		close(started)
		<-c.Done()
		return c.Err()
	})
	e.Start(ctx)
	op, _ := e.Enqueue(ctx, "w", "", "wait")
	<-started
	if err := e.Cancel(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, op.ID, domain.OpCancelled)
}

func TestRecoversRunningOps(t *testing.T) {
	e, st, ctx := setup(t)
	op := &domain.Operation{ID: "op_stale", WorkspaceID: "w", Type: "ok", Status: domain.OpRunning, CreatedAt: time.Now()}
	_ = st.CreateOperation(ctx, op)
	e.Register("ok", func(context.Context, *domain.Operation, Progress) error { return nil })
	e.Start(ctx)
	waitStatus(t, st, "op_stale", domain.OpSucceeded)
}
