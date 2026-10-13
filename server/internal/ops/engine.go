// Package ops runs durable asynchronous operations (PRD 13.1, 20.3.5).
// Operations are rows in the store; workers claim them, run a registered
// handler, and record progress, logs and failures. Operations within one
// workspace run strictly in order; different workspaces run in parallel.
package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// Progress lets a handler report progress and append log lines.
type Progress interface {
	Logf(format string, args ...any)
	Set(percent int)
}

type Handler func(ctx context.Context, op *domain.Operation, p Progress) error

type Engine struct {
	st       store.Store
	bus      *events.Bus
	log      *slog.Logger
	handlers map[string]Handler
	workers  int
	wake     chan struct{}

	mu      sync.Mutex
	busy    map[string]bool // workspace id -> an op is running
	cancels map[string]context.CancelFunc
}

func NewEngine(st store.Store, bus *events.Bus, log *slog.Logger, workers int) *Engine {
	if workers < 1 {
		workers = 4
	}
	return &Engine{st: st, bus: bus, log: log, handlers: map[string]Handler{}, workers: workers,
		wake: make(chan struct{}, 1), busy: map[string]bool{}, cancels: map[string]context.CancelFunc{}}
}

func (e *Engine) Register(opType string, h Handler) { e.handlers[opType] = h }

// Enqueue persists an operation and wakes the workers.
func (e *Engine) Enqueue(ctx context.Context, workspaceID, componentID, opType string) (*domain.Operation, error) {
	return e.EnqueueParams(ctx, workspaceID, componentID, opType, nil)
}

// EnqueueParams is Enqueue with a payload for the handler. Params never carry secrets.
func (e *Engine) EnqueueParams(ctx context.Context, workspaceID, componentID, opType string, params map[string]any) (*domain.Operation, error) {
	op := &domain.Operation{Params: params, ID: store.NewID("op"), WorkspaceID: workspaceID, ComponentID: componentID, Type: opType,
		Status: domain.OpPending, Log: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := e.st.CreateOperation(ctx, op); err != nil {
		return nil, err
	}
	e.publish(op)
	e.signal()
	return op, nil
}

func (e *Engine) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) publish(op *domain.Operation) {
	e.bus.Publish(op.WorkspaceID, events.Event{Type: "operation", Data: op})
}

// Cancel asks a running operation to stop; pending ones are cancelled directly.
func (e *Engine) Cancel(ctx context.Context, opID string) error {
	e.mu.Lock()
	cancel, running := e.cancels[opID]
	e.mu.Unlock()
	if running {
		cancel()
		return nil
	}
	op, err := e.st.UpdateOperation(ctx, opID, func(o *domain.Operation) error {
		if o.Status != domain.OpPending {
			return store.ErrConflict
		}
		o.Status = domain.OpCancelled
		return nil
	})
	if err == nil {
		e.publish(op)
	}
	return err
}

// Start recovers interrupted work, then runs workers until ctx is cancelled.
func (e *Engine) Start(ctx context.Context) {
	// Operations left "running" by a crashed process are re-queued; handlers
	// are written to be idempotent (PRD 13.1: recoverable failure states).
	if stale, err := e.st.ListOperationsByStatus(ctx, domain.OpRunning); err == nil {
		for _, o := range stale {
			_, _ = e.st.UpdateOperation(ctx, o.ID, func(x *domain.Operation) error {
				x.Status = domain.OpPending
				x.Log = append(x.Log, "recovered after control-plane restart")
				return nil
			})
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < e.workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.loop(ctx) }()
	}
	e.signal()
	go func() { <-ctx.Done(); wg.Wait() }()
}

func (e *Engine) loop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		for e.runOne(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-tick.C:
		}
	}
}

// claim atomically moves one runnable pending operation to running.
func (e *Engine) claim(ctx context.Context) *domain.Operation {
	pending, err := e.st.ListOperationsByStatus(ctx, domain.OpPending)
	if err != nil {
		return nil
	}
	for _, p := range pending {
		e.mu.Lock()
		if e.busy[p.WorkspaceID] {
			e.mu.Unlock()
			continue
		}
		e.busy[p.WorkspaceID] = true
		e.mu.Unlock()
		op, err := e.st.UpdateOperation(ctx, p.ID, func(o *domain.Operation) error {
			if o.Status != domain.OpPending {
				return store.ErrConflict
			}
			o.Status = domain.OpRunning
			return nil
		})
		if err != nil {
			e.release(p.WorkspaceID)
			continue
		}
		return op
	}
	return nil
}

func (e *Engine) release(ws string) {
	e.mu.Lock()
	delete(e.busy, ws)
	e.mu.Unlock()
	e.signal()
}

func (e *Engine) runOne(ctx context.Context) bool {
	op := e.claim(ctx)
	if op == nil {
		return false
	}
	defer e.release(op.WorkspaceID)
	e.publish(op)

	opCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancels[op.ID] = cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.cancels, op.ID)
		e.mu.Unlock()
	}()

	h, ok := e.handlers[op.Type]
	var herr error
	if !ok {
		herr = fmt.Errorf("no handler for operation type %q", op.Type)
	} else {
		herr = e.safeRun(opCtx, h, op)
	}

	final, _ := e.st.UpdateOperation(context.Background(), op.ID, func(o *domain.Operation) error {
		switch {
		case herr == nil:
			o.Status, o.Progress = domain.OpSucceeded, 100
		case errors.Is(herr, context.Canceled) && ctx.Err() == nil:
			o.Status, o.Error = domain.OpCancelled, "cancelled"
		default:
			o.Status, o.Error = domain.OpFailed, herr.Error()
		}
		return nil
	})
	if final != nil {
		e.publish(final)
	}
	return true
}

func (e *Engine) safeRun(ctx context.Context, h Handler, op *domain.Operation) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("operation panicked: %v", r)
			e.log.Error("operation panic", "op", op.ID, "panic", r)
		}
	}()
	return h(ctx, op, &progress{e: e, id: op.ID})
}

type progress struct {
	e  *Engine
	id string
}

func (p *progress) Logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if op, err := p.e.st.UpdateOperation(context.Background(), p.id, func(o *domain.Operation) error {
		o.Log = append(o.Log, line)
		return nil
	}); err == nil {
		p.e.publish(op)
	}
}

func (p *progress) Set(percent int) {
	if op, err := p.e.st.UpdateOperation(context.Background(), p.id, func(o *domain.Operation) error {
		o.Progress = percent
		return nil
	}); err == nil {
		p.e.publish(op)
	}
}
