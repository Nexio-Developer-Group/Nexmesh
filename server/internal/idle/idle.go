// Package idle tracks when a workspace was last used so it can be stopped
// automatically (PRD F7). "Used" means any request for the workspace, any open
// long-lived connection (event stream, terminal, preview WebSocket), or work
// the agent reports as running.
package idle

import (
	"sync"
	"time"
)

type Tracker struct {
	mu    sync.Mutex
	now   func() time.Time
	start time.Time
	last  map[string]time.Time
	open  map[string]int
}

func NewTracker() *Tracker {
	return &Tracker{now: time.Now, start: time.Now(), last: map[string]time.Time{}, open: map[string]int{}}
}

// SetClock replaces the time source (tests).
func (t *Tracker) SetClock(f func() time.Time) { t.mu.Lock(); t.now = f; t.start = f(); t.mu.Unlock() }

// Touch records activity now.
func (t *Tracker) Touch(ws string) {
	t.mu.Lock()
	t.last[ws] = t.now()
	t.mu.Unlock()
}

// Open marks a long-lived connection; call the returned function when it ends.
func (t *Tracker) Open(ws string) (end func()) {
	t.mu.Lock()
	t.open[ws]++
	t.last[ws] = t.now()
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.open[ws]--
			t.last[ws] = t.now()
			t.mu.Unlock()
		})
	}
}

// IdleFor reports how long the workspace has been unused. A workspace with an
// open connection is never idle. Before any activity has been seen, idleness
// counts from when the tracker started, so a control-plane restart does not
// stop workspaces immediately.
func (t *Tracker) IdleFor(ws string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open[ws] > 0 {
		return 0
	}
	last, ok := t.last[ws]
	if !ok {
		last = t.start
	}
	return t.now().Sub(last)
}

// Forget drops tracking state for a deleted workspace.
func (t *Tracker) Forget(ws string) {
	t.mu.Lock()
	delete(t.last, ws)
	delete(t.open, ws)
	t.mu.Unlock()
}
