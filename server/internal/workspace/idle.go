package workspace

import (
	"context"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// IdleSource reports how long a workspace has been unused.
type IdleSource interface {
	IdleFor(workspaceID string) time.Duration
}

// BusyCheck asks the workspace agent whether work is running (a build, a
// foreground process). Offline agents report not busy.
type BusyCheck func(ctx context.Context, workspaceID string) (busy bool, reasons []string)

type idleState struct {
	src      IdleSource
	busy     BusyCheck
	mu       sync.Mutex
	stopping map[string]time.Time
}

// WithIdle enables automatic stopping of idle workspaces.
func (s *Service) WithIdle(src IdleSource, busy BusyCheck) *Service {
	s.idle = &idleState{src: src, busy: busy, stopping: map[string]time.Time{}}
	return s
}

// StopIdle runs one pass: every running workspace whose idle timeout has
// elapsed, and whose agent reports no running work, is stopped. The stop is
// recorded as done by the system with the idle time, and the workspace keeps
// its files, so starting it again resumes where it left off.
func (s *Service) StopIdle(ctx context.Context) []string {
	if s.idle == nil {
		return nil
	}
	list, err := s.st.ListWorkspaces(ctx, "")
	if err != nil {
		return nil
	}
	var stopped []string
	for _, w := range list {
		if w.Status != domain.WSReady || w.IdleStopMinutes <= 0 {
			continue
		}
		limit := time.Duration(w.IdleStopMinutes) * time.Minute
		idle := s.idle.src.IdleFor(w.ID)
		if idle < limit {
			continue
		}
		s.idle.mu.Lock()
		if t, ok := s.idle.stopping[w.ID]; ok && time.Since(t) < 2*time.Minute {
			s.idle.mu.Unlock()
			continue
		}
		s.idle.mu.Unlock()
		if s.idle.busy != nil {
			if busy, reasons := s.idle.busy(ctx, w.ID); busy {
				s.log.Info("idle stop skipped: workspace is busy", "workspace", w.ID, "reasons", reasons)
				continue
			}
		}
		s.idle.mu.Lock()
		s.idle.stopping[w.ID] = time.Now()
		s.idle.mu.Unlock()
		if _, err := s.ops.EnqueueParams(ctx, w.ID, "", "stop_workspace", map[string]any{"reason": "idle"}); err != nil {
			s.log.Error("idle stop failed to enqueue", "workspace", w.ID, "err", err)
			continue
		}
		s.aud.Record(ctx, domain.ActorSystem, "", w.ID, "workspace.idle_stop", w.Name,
			map[string]any{"idleMinutes": int(idle.Minutes()), "limitMinutes": w.IdleStopMinutes})
		stopped = append(stopped, w.ID)
	}
	return stopped
}

// RunIdleStopper calls StopIdle on an interval until ctx ends.
func (s *Service) RunIdleStopper(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.StopIdle(ctx)
		}
	}
}
