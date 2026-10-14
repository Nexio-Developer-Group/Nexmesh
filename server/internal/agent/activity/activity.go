// Package activity lets the agent report whether work is running in the
// workspace, so an idle stop never interrupts a build, a long command or a
// foreground process in a terminal that nobody is currently looking at.
package activity

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

// Source reports a reason the workspace is busy, or "" when it is not.
type Source func() string

type Monitor struct {
	sources []Source
	running atomic.Int64
}

func New() *Monitor { return &Monitor{} }

// Add registers a source.
func (m *Monitor) Add(s Source) { m.sources = append(m.sources, s) }

// Begin marks one unit of work (a command, a git operation) as running and
// returns the function that ends it.
func (m *Monitor) Begin() func() {
	m.running.Add(1)
	return func() { m.running.Add(-1) }
}

type Status struct {
	Busy    bool     `json:"busy"`
	Reasons []string `json:"reasons"`
}

func (m *Monitor) Status() Status {
	st := Status{Reasons: []string{}}
	if n := m.running.Load(); n > 0 {
		st.Reasons = append(st.Reasons, "a command is running")
	}
	for _, s := range m.sources {
		if r := s(); r != "" {
			st.Reasons = append(st.Reasons, r)
		}
	}
	st.Busy = len(st.Reasons) > 0
	return st
}

type installer interface {
	Handle(method string, h agentproto.Handler)
}

func (m *Monitor) Install(a installer) {
	a.Handle("activity.status", func(context.Context, json.RawMessage) (any, error) { return m.Status(), nil })
}
