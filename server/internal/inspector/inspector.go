// Package inspector runs the database and broker tools on cards (PRD F13). It
// resolves the card's connection (credentials never leave the control plane),
// applies a per-card rate limit, runs one read-only operation through the probe
// package, and records every request in the audit log without any result data.
package inspector

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
)

// ErrRateLimited is returned when a card is queried too often.
var ErrRateLimited = errors.New("too many queries on this card; wait a few seconds")

const (
	perMinute = 40
	statement = 500 // longest statement text kept in the audit log
)

type Service struct {
	comp *component.Service
	aud  *audit.Recorder
	now  func() time.Time

	mu    sync.Mutex
	calls map[string][]time.Time
}

func New(comp *component.Service, aud *audit.Recorder) *Service {
	return &Service{comp: comp, aud: aud, now: time.Now, calls: map[string][]time.Time{}}
}

func (s *Service) allow(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var recent []time.Time
	for _, t := range s.calls[id] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= perMinute {
		s.calls[id] = recent
		return false
	}
	s.calls[id] = append(recent, now)
	return true
}

// Capabilities describes what a card's inspector can do.
type Capabilities struct {
	Available  bool       `json:"available"`
	Reason     string     `json:"reason,omitempty"`
	ReadOnly   bool       `json:"readOnly"`
	Production bool       `json:"production,omitempty"`
	Ops        []probe.Op `json:"ops"`
}

// Describe reports whether a card can be inspected and how.
func (s *Service) Describe(ctx context.Context, owner, wsID, id string) (*Capabilities, error) {
	info, err := s.comp.ConnectionFor(ctx, owner, wsID, id)
	if err != nil {
		// Not connected yet, or nothing for this environment: say so rather than fail.
		c, cerr := s.comp.Get(ctx, owner, wsID, id)
		if cerr != nil {
			return nil, cerr
		}
		reason := "Connect this card to a service first."
		if c.Mode == domain.ModeInstalled {
			reason = "Inspection works on connected services. Installed services are reachable from the terminal in the Code tab."
		} else if c.Mode == domain.ModeConnected {
			reason = "This card has no connection for the active environment."
		}
		return &Capabilities{Available: false, Reason: reason, ReadOnly: true, Ops: []probe.Op{}}, nil
	}
	if !probe.Inspectable(info.Def.ID) {
		return &Capabilities{Available: false, Reason: info.Def.Name + " has no inspector.", ReadOnly: true, Ops: []probe.Op{}}, nil
	}
	return &Capabilities{Available: true, ReadOnly: true, Production: info.Environment == domain.EnvProduction, Ops: probe.Ops(info.Def.ID)}, nil
}

// Result is what the API returns. Production marks results from a
// production-labelled connection so the UI can say so.
type Result struct {
	*probe.InspectResult
	Production bool `json:"production,omitempty"`
}

// Failure is a refused or failed operation. It carries a stable code.
type Failure struct {
	Code    string
	Message string
}

func (f *Failure) Error() string { return f.Message }

// Run executes one read-only operation for an actor.
func (s *Service) Run(ctx context.Context, actorType, actorID, owner, wsID, id string, req probe.InspectRequest) (*Result, error) {
	info, err := s.comp.ConnectionFor(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if !probe.Inspectable(info.Def.ID) {
		return nil, &Failure{"not_inspectable", info.Def.Name + " has no inspector."}
	}
	if !s.allow(id) {
		return nil, ErrRateLimited
	}
	p := info.Params(20 * time.Second)
	res, ie := probe.Inspect(ctx, p, req)

	detail := map[string]any{"componentId": id, "op": req.Op, "environment": info.Environment, "readOnly": true}
	for _, k := range []string{"database", "table", "collection", "topic", "partition", "vhost", "pattern", "key", "limit"} {
		if v, ok := req.Args[k]; ok && v != nil && v != "" {
			detail[k] = v
		}
	}
	// The statement is recorded (scrubbed of credentials) so a query can be reviewed; its results never are.
	for _, k := range []string{"sql", "filter", "sort", "command"} {
		if v, ok := req.Args[k].(string); ok && v != "" {
			if len(v) > statement {
				v = v[:statement] + "…"
			}
			detail[k] = p.Scrub(v)
		}
	}
	if ie != nil {
		detail["error"] = ie.Code
		s.aud.Record(ctx, actorType, actorID, wsID, "inspect.query_failed", info.Def.ID, detail)
		return nil, &Failure{ie.Code, ie.Msg}
	}
	detail["rows"], detail["truncated"], detail["elapsedMs"] = len(res.Rows), res.Truncated, res.ElapsedMS
	s.aud.Record(ctx, actorType, actorID, wsID, "inspect.query", info.Def.ID, detail)
	return &Result{InspectResult: res, Production: info.Environment == domain.EnvProduction}, nil
}
