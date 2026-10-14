package component

import (
	"context"
	"fmt"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// Per-environment connections (PRD F4). A connected card has one main
// connection (labelled with c.Environment) and optionally a profile for each
// other environment. The workspace's active environment decides which one is
// used for health checks and variable injection. Profiles never share values or
// credentials: a profile's password is stored under its own secret and is never
// borrowed from another environment.

const ptestKind = "profile_test"

func profileSecretID(cid, env string) string  { return "connsec_" + cid + "@" + env }
func profilePendingID(cid, env string) string { return "connpending_" + cid + "@" + env }

// activeEnv returns the active environment and whether the workspace matches
// connections to it strictly (see Workspace.UsesEnvironments).
func (s *Service) activeEnv(ctx context.Context, wsID string) (env string, strict bool) {
	if w, err := s.st.GetWorkspace(ctx, wsID); err == nil {
		return w.ActiveEnv(), w.UsesEnvironments()
	}
	return domain.EnvDevelopment, false
}

// activeConn is the connection in effect for a connected card.
type activeConn struct {
	Env    string
	Fields map[string]any
	Secret map[string]string
}

// activeConn returns the connection the card uses in the workspace's active
// environment, or nil when it has none there.
func (s *Service) activeConn(ctx context.Context, c *domain.Component) *activeConn {
	if c.Mode != domain.ModeConnected {
		return nil
	}
	env, strict := s.activeEnv(ctx, c.WorkspaceID)
	if !strict || env == c.PrimaryEnv() {
		f, sec := s.storedFields(ctx, c)
		return &activeConn{Env: c.PrimaryEnv(), Fields: f, Secret: sec}
	}
	p, ok := c.Profiles[env]
	if !ok || p == nil {
		return nil
	}
	all := map[string]any{}
	for k, v := range p.Connection {
		all[k] = v
	}
	sec := s.secretsFor(ctx, profileSecretID(c.ID, env))
	for k, v := range sec {
		all[k] = v
	}
	return &activeConn{Env: env, Fields: all, Secret: sec}
}

// HasConnectionFor reports whether the card can be used in env. When the
// workspace does not use environments yet, any connected card can.
func HasConnectionFor(c *domain.Component, env string, strict bool) bool {
	if c.Mode != domain.ModeConnected {
		return false
	}
	if !strict || env == c.PrimaryEnv() {
		return true
	}
	p, ok := c.Profiles[env]
	return ok && p != nil
}

func (s *Service) isProfileOp(c *domain.Component, env string) bool {
	return c.Mode == domain.ModeConnected && env != "" && env != c.PrimaryEnv()
}

type profileTest struct {
	Passed      bool      `json:"passed"`
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
	LatencyMS   int64     `json:"latencyMs"`
}

// profileFields merges request values with the credentials held for this
// environment only (the pending tested ones, then the saved ones).
func (s *Service) profileFields(ctx context.Context, c *domain.Component, def *catalog.Component, env string, in ConnectionInput) (map[string]any, map[string]string) {
	plain, sec := splitFields(def, in.Fields)
	stored := s.secretsFor(ctx, profilePendingID(c.ID, env), profileSecretID(c.ID, env))
	for _, f := range def.Connection.Fields {
		if f.Type != "password" || !fieldVisible(f, plain) {
			continue
		}
		if _, ok := sec[f.Name]; !ok {
			if v, ok := stored[f.Name]; ok {
				sec[f.Name] = v
			}
		}
	}
	all := map[string]any{}
	for k, v := range plain {
		all[k] = v
	}
	for k, v := range sec {
		all[k] = v
	}
	return all, sec
}

func (s *Service) profileFingerprint(def *catalog.Component, env string, fields map[string]any) string {
	return s.fingerprint(def.ID+"@"+env, fields)
}

func (s *Service) testProfile(ctx context.Context, owner string, c *domain.Component, def *catalog.Component, in ConnectionInput) (*domain.TestResult, error) {
	env := in.Environment
	if !s.conn.allow(c.ID) {
		return nil, ErrRateLimited
	}
	fields, sec := s.profileFields(ctx, c, def, env, in)
	res := s.runProbe(ctx, c, def, fields, sec, false)
	res.Fingerprint = s.profileFingerprint(def, env, fields)
	if err := store.PutJSON(ctx, s.st, ptestKind, c.WorkspaceID, c.ID+"@"+env, profileTest{Passed: res.Passed, Fingerprint: res.Fingerprint, At: res.At, LatencyMS: res.LatencyMS}); err != nil {
		return nil, err
	}
	if res.Passed && len(sec) > 0 {
		if err := s.conn.Secrets.Put(ctx, profilePendingID(c.ID, env), sec); err != nil {
			return nil, err
		}
	}
	s.aud.User(ctx, owner, c.WorkspaceID, "connection.test", def.ID, map[string]any{"componentId": c.ID, "environment": env, "passed": res.Passed, "stage": res.FailedStage, "latencyMs": res.LatencyMS})
	return res, nil
}

func (s *Service) saveProfile(ctx context.Context, owner string, c *domain.Component, def *catalog.Component, in ConnectionInput) (*domain.Component, error) {
	env := in.Environment
	fields, sec := s.profileFields(ctx, c, def, env, in)
	pt, err := store.GetJSON[profileTest](ctx, s.st, ptestKind, c.ID+"@"+env)
	if err != nil || !pt.Passed || pt.Fingerprint != s.profileFingerprint(def, env, fields) || s.conn.now().Sub(pt.At) > s.conn.TestFreshness {
		return nil, vErr("test", "test_required", "Run Test Connection again: it must pass for exactly these values before they can be added.")
	}
	if len(sec) > 0 {
		if err := s.conn.Secrets.Put(ctx, profileSecretID(c.ID, env), sec); err != nil {
			return nil, err
		}
	}
	_ = s.conn.Secrets.Delete(ctx, profilePendingID(c.ID, env))
	_ = s.st.DeleteDoc(ctx, ptestKind, c.ID+"@"+env)
	plain, _ := splitFields(def, in.Fields)
	_, existed := c.Profiles[env]
	if existed && len(in.Fields) == 0 {
		plain = c.Profiles[env].Connection
	}
	c2, err := s.st.UpdateComponent(ctx, c.ID, func(x *domain.Component) error {
		if x.Profiles == nil {
			x.Profiles = map[string]*domain.ConnProfile{}
		}
		p := &domain.ConnProfile{Connection: plain, UpdatedAt: time.Now().UTC()}
		if len(sec) > 0 || (existed && x.Profiles[env].SecretRef != "") {
			p.SecretRef = profileSecretID(c.ID, env)
		}
		x.Profiles[env] = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	action := "connection.profile_add"
	if existed {
		action = "connection.profile_edit"
	}
	s.aud.User(ctx, owner, c.WorkspaceID, action, def.ID, map[string]any{"componentId": c.ID, "environment": env, "host": plain["host"], "latencyMs": pt.LatencyMS})
	s.publish(c2)
	if cur, strict := s.activeEnv(ctx, c.WorkspaceID); strict && cur == env {
		go s.HealthForWorkspace(context.WithoutCancel(ctx), c.WorkspaceID)
	}
	return c2, nil
}

// DeleteProfile removes a card's connection for one non-main environment.
func (s *Service) DeleteProfile(ctx context.Context, owner, wsID, id, env string) (*domain.Component, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	def, err := s.connectable(c)
	if err != nil {
		return nil, err
	}
	if c.Mode != domain.ModeConnected {
		return nil, ErrBadComponent
	}
	if env == c.PrimaryEnv() {
		return nil, vErr("environment", "main_connection", "This is the card's main connection. Disconnect the card to remove it.")
	}
	if _, ok := c.Profiles[env]; !ok {
		return nil, store.ErrNotFound
	}
	_ = s.conn.Secrets.Delete(ctx, profileSecretID(id, env))
	_ = s.conn.Secrets.Delete(ctx, profilePendingID(id, env))
	c2, err := s.st.UpdateComponent(ctx, id, func(x *domain.Component) error {
		delete(x.Profiles, env)
		if len(x.Profiles) == 0 {
			x.Profiles = nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "connection.profile_remove", def.ID, map[string]any{"componentId": id, "environment": env})
	s.publish(c2)
	if cur, strict := s.activeEnv(ctx, wsID); strict && cur == env {
		go s.HealthForWorkspace(context.WithoutCancel(ctx), wsID)
	}
	return c2, nil
}

// noConnection records that a connected card has nothing for the active
// environment. The card stays connected in the other environments.
func (s *Service) noConnection(ctx context.Context, c *domain.Component) {
	env, _ := s.activeEnv(ctx, c.WorkspaceID)
	c2, err := s.st.UpdateComponent(ctx, c.ID, func(x *domain.Component) error {
		x.Observed = domain.StateDisconnect
		x.Message = fmt.Sprintf("No %s connection is set up for this card. Add one in the Connect tab.", env)
		return nil
	})
	if err == nil {
		s.publish(c2)
	}
}

// InspectInfo is what an inspector needs to talk to a connected service: the
// connection in effect for the active environment, with secrets, and the guard
// and scrubber to use. It never leaves the control plane.
type InspectInfo struct {
	Def         *catalog.Component
	Component   *domain.Component
	Fields      map[string]any
	Environment string
	Params      func(timeout time.Duration) probe.Params
}

// ConnectionFor resolves a connected card for an inspector.
func (s *Service) ConnectionFor(ctx context.Context, owner, wsID, id string) (*InspectInfo, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if s.conn == nil {
		return nil, ErrBadComponent
	}
	def, ok := s.cat.Get(c.CatalogID)
	if !ok {
		return nil, ErrUnknownCatalog
	}
	if c.Mode != domain.ModeConnected {
		return nil, ErrBadComponent
	}
	ac := s.activeConn(ctx, c)
	if ac == nil {
		return nil, vErr("environment", "no_connection", "This card has no connection for the active environment.")
	}
	for _, v := range ac.Secret {
		s.conn.Secrets.Register(v)
	}
	return &InspectInfo{Def: def, Component: c, Fields: ac.Fields, Environment: ac.Env,
		Params: func(timeout time.Duration) probe.Params {
			return probe.Params{Def: def, Fields: ac.Fields, Guard: s.conn.Guard, Scrub: s.conn.Secrets.Scrub, Timeout: timeout}
		}}, nil
}
