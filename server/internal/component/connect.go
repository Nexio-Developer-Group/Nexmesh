package component

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/connenv"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

var (
	ErrRateLimited    = errors.New("too many connection tests; wait a moment and try again")
	ErrNotConnectable = errors.New("this component cannot be connected to an external service")
)

// ProbeFunc runs Test Connection. Production uses probe.Run; tests inject fakes.
type ProbeFunc func(ctx context.Context, p probe.Params, report probe.Report) *domain.TestResult

// ConnectConfig wires Connect mode.
type ConnectConfig struct {
	Secrets        *secrets.Service
	Guard          *netguard.Guard
	FingerprintKey []byte // HMAC key; tests are tied to exact values without storing them
	Probe          ProbeFunc
	Log            *slog.Logger
	// TestFreshness bounds how long a passing test authorizes Add (default 15m).
	TestFreshness time.Duration
}

type connectState struct {
	ConnectConfig
	mu    sync.Mutex
	tests map[string][]time.Time // component id -> recent test times
	now   func() time.Time
}

// WithConnect enables Connect mode: test, add, re-test, edit, rotate, disconnect.
func (s *Service) WithConnect(cfg ConnectConfig) *Service {
	if cfg.Probe == nil {
		cfg.Probe = probe.Run
	}
	if cfg.TestFreshness == 0 {
		cfg.TestFreshness = 15 * time.Minute
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s.conn = &connectState{ConnectConfig: cfg, tests: map[string][]time.Time{}, now: time.Now}
	return s
}

type ConnectionInput struct {
	ConnName    string         `json:"connName"`
	Environment string         `json:"environment"`
	Fields      map[string]any `json:"fields"`
}

func pendingID(cid string) string { return "connpending_" + cid }
func activeID(cid string) string  { return "connsec_" + cid }

var environments = map[string]bool{"": true, "development": true, "staging": true, "production": true}

// allow enforces the test rate limit: one per second and ten per minute per card.
func (cs *connectState) allow(id string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	now := cs.now()
	var recent []time.Time
	for _, t := range cs.tests[id] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 || (len(recent) > 0 && now.Sub(recent[len(recent)-1]) < time.Second) {
		cs.tests[id] = recent
		return false
	}
	cs.tests[id] = append(recent, now)
	return true
}

func (s *Service) connectable(c *domain.Component) (*catalog.Component, error) {
	if s.conn == nil {
		return nil, errors.New("connect is not enabled")
	}
	def, ok := s.cat.Get(c.CatalogID)
	if !ok {
		return nil, ErrUnknownCatalog
	}
	if !def.SupportsMode(catalog.ModeConnect) || def.Connection == nil {
		return nil, ErrNotConnectable
	}
	if c.Mode == domain.ModeInstalled {
		return nil, ErrBadComponent
	}
	return def, nil
}

// splitFields keeps only catalog-defined fields and separates secret ones.
func splitFields(def *catalog.Component, in map[string]any) (plain map[string]any, secret map[string]string) {
	plain, secret = map[string]any{}, map[string]string{}
	for _, f := range def.Connection.Fields {
		v, ok := in[f.Name]
		if !ok || v == nil {
			continue
		}
		if f.Type == "password" {
			if sv := fmt.Sprint(v); sv != "" {
				secret[f.Name] = sv
			}
			continue
		}
		plain[f.Name] = v
	}
	return plain, secret
}

func (s *Service) secretsFor(ctx context.Context, ids ...string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		if m, err := s.conn.Secrets.Get(ctx, id); err == nil {
			for k, v := range m {
				if _, set := out[k]; !set {
					out[k] = v
				}
			}
		}
	}
	return out
}

// merge builds the full field set for a test: request values win, then the
// pending (tested) secrets, then the active secrets.
func (s *Service) merge(ctx context.Context, c *domain.Component, def *catalog.Component, in ConnectionInput) (map[string]any, map[string]string) {
	plain, sec := splitFields(def, in.Fields)
	stored := s.secretsFor(ctx, pendingID(c.ID), activeID(c.ID))
	for _, name := range def.SecretFields() {
		if _, ok := sec[name]; !ok {
			if v, ok := stored[name]; ok {
				sec[name] = v
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

func (s *Service) fingerprint(id string, fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := hmac.New(sha256.New, s.conn.FingerprintKey)
	h.Write([]byte(id))
	for _, k := range keys {
		b, _ := json.Marshal(fields[k])
		h.Write([]byte{0})
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) validateMeta(ctx context.Context, c *domain.Component, in ConnectionInput) error {
	if !environments[in.Environment] {
		return vErr("environment", "invalid_environment", "Environment must be development, staging or production.")
	}
	if in.ConnName != "" && !connenv.ValidName(in.ConnName) {
		return vErr("connName", "invalid_conn_name", "Use lowercase letters, digits, hyphens or underscores, starting with a letter (for example prod-orders-db).")
	}
	return nil
}

func (s *Service) runProbe(ctx context.Context, c *domain.Component, def *catalog.Component, fields map[string]any, secretVals map[string]string, publish bool) *domain.TestResult {
	for _, v := range secretVals {
		s.conn.Secrets.Register(v)
	}
	var stages []domain.StageResult
	report := func(sr domain.StageResult) {
		if !publish {
			return
		}
		found := false
		for i := range stages {
			if stages[i].Stage == sr.Stage {
				stages[i], found = sr, true
			}
		}
		if !found {
			stages = append(stages, sr)
		}
		c2, err := s.st.UpdateComponent(ctx, c.ID, func(x *domain.Component) error {
			x.LastTest = &domain.TestResult{Stages: append([]domain.StageResult(nil), stages...), At: time.Now().UTC()}
			return nil
		})
		if err == nil {
			s.publish(c2)
		}
	}
	res := s.conn.Probe(ctx, probe.Params{Def: def, Fields: fields, Guard: s.conn.Guard, Scrub: s.conn.Secrets.Scrub, Timeout: 20 * time.Second}, report)
	res.Fingerprint = s.fingerprint(def.ID, fields)
	return res
}

// Test runs Test Connection from the control plane. A passing result is bound
// to the exact values tested; the tested secrets are held (encrypted) so Add
// can use them without the user retyping a write-only password.
func (s *Service) Test(ctx context.Context, owner, wsID, id string, in ConnectionInput) (*domain.TestResult, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	def, err := s.connectable(c)
	if err != nil {
		return nil, err
	}
	if err := s.validateMeta(ctx, c, in); err != nil {
		return nil, err
	}
	if s.isProfileOp(c, in.Environment) {
		return s.testProfile(ctx, owner, c, def, in)
	}
	if !s.conn.allow(id) {
		return nil, ErrRateLimited
	}
	fields, sec := s.merge(ctx, c, def, in)
	prev := c.Observed
	c2, _ := s.st.UpdateComponent(ctx, id, func(x *domain.Component) error {
		x.Observed, x.Message = domain.StateTesting, ""
		if x.Mode != domain.ModeConnected {
			x.ConnName, x.Environment = in.ConnName, in.Environment
			plain, _ := splitFields(def, in.Fields)
			x.Connection = plain
		}
		return nil
	})
	if c2 != nil {
		s.publish(c2)
	}
	res := s.runProbe(ctx, c, def, fields, sec, true)

	final, err := s.st.UpdateComponent(ctx, id, func(x *domain.Component) error {
		x.LastTest = res
		switch {
		case x.Mode == domain.ModeConnected:
			x.Observed = prev // editing a live connection never changes its health state here
			if x.Observed == domain.StateTesting {
				x.Observed = domain.StateConnected
			}
		case res.Passed:
			x.Observed, x.Message = domain.StateTestPassed, ""
		default:
			x.Observed, x.Message = domain.StateTestFailed, res.Message
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if res.Passed && len(sec) > 0 {
		if err := s.conn.Secrets.Put(ctx, pendingID(id), sec); err != nil {
			return nil, err
		}
	}
	s.publish(final)
	s.aud.User(ctx, owner, wsID, "connection.test", def.ID, map[string]any{"componentId": id, "passed": res.Passed, "stage": res.FailedStage, "latencyMs": res.LatencyMS})
	return res, nil
}

// Save implements Add to Workspace and Edit. It is rejected unless the last
// test passed for exactly these values (PRD 11.3 step 5).
func (s *Service) Save(ctx context.Context, owner, wsID, id string, in ConnectionInput) (*domain.Component, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	def, err := s.connectable(c)
	if err != nil {
		return nil, err
	}
	if err := s.validateMeta(ctx, c, in); err != nil {
		return nil, err
	}
	if s.isProfileOp(c, in.Environment) {
		return s.saveProfile(ctx, owner, c, def, in)
	}
	editing := c.Mode == domain.ModeConnected
	name := in.ConnName
	if editing && name == "" {
		name = c.ConnName
	}
	if name == "" {
		return nil, vErr("connName", "invalid_conn_name", "Give this connection a name, for example prod-orders-db.")
	}
	if !connenv.ValidName(name) {
		return nil, vErr("connName", "invalid_conn_name", "Use lowercase letters, digits, hyphens or underscores, starting with a letter (for example prod-orders-db).")
	}
	others, _ := s.st.ListComponents(ctx, wsID)
	for _, o := range others {
		if o.ID != id && o.Mode == domain.ModeConnected && o.ConnName == name {
			return nil, vErr("connName", "name_taken", fmt.Sprintf("A connection named %q already exists in this workspace.", name))
		}
	}
	fields, sec := s.merge(ctx, c, def, in)
	lt := c.LastTest
	if lt == nil || !lt.Passed || lt.Fingerprint != s.fingerprint(def.ID, fields) || s.conn.now().Sub(lt.At) > s.conn.TestFreshness {
		return nil, vErr("test", "test_required", "Run Test Connection again: it must pass for exactly these values before they can be added.")
	}
	if len(sec) > 0 {
		if err := s.conn.Secrets.Put(ctx, activeID(id), sec); err != nil {
			return nil, err
		}
	}
	_ = s.conn.Secrets.Delete(ctx, pendingID(id))
	plain, _ := splitFields(def, in.Fields)
	if editing && len(in.Fields) == 0 {
		plain = c.Connection
	}
	env := in.Environment
	if editing && env == "" {
		env = c.Environment
	}
	c2, err := s.st.UpdateComponent(ctx, id, func(x *domain.Component) error {
		x.Mode, x.Connection, x.ConnName, x.Environment = domain.ModeConnected, plain, name, env
		x.Desired, x.Observed = domain.StateConnected, domain.StateConnected
		if len(sec) > 0 {
			x.SecretRef = activeID(id)
		}
		x.Message = "Last check passed, " + latencyText(lt.LatencyMS)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish(c2)
	action := "connection.add"
	if editing {
		action = "connection.edit"
	}
	s.aud.User(ctx, owner, wsID, action, def.ID, map[string]any{"componentId": id, "connName": name, "environment": env, "host": plain["host"], "latencyMs": lt.LatencyMS})
	return c2, nil
}

// storedFields returns a connected component's full field set (secrets included, in memory only).
func (s *Service) storedFields(ctx context.Context, c *domain.Component) (map[string]any, map[string]string) {
	all := map[string]any{}
	for k, v := range c.Connection {
		all[k] = v
	}
	sec := s.secretsFor(ctx, activeID(c.ID))
	for k, v := range sec {
		all[k] = v
	}
	return all, sec
}

// Retest re-runs the probe against the stored connection and records health.
func (s *Service) Retest(ctx context.Context, owner, wsID, id string) (*domain.TestResult, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	def, err := s.connectable(c)
	if err != nil || c.Mode != domain.ModeConnected {
		return nil, ErrBadComponent
	}
	if !s.conn.allow(id) {
		return nil, ErrRateLimited
	}
	res := s.applyHealth(ctx, c, def, true)
	s.aud.User(ctx, owner, wsID, "connection.retest", def.ID, map[string]any{"componentId": id, "passed": res.Passed, "stage": res.FailedStage, "latencyMs": res.LatencyMS})
	return res, nil
}

// applyHealth probes a connected component and updates its observed state.
func (s *Service) applyHealth(ctx context.Context, c *domain.Component, def *catalog.Component, publishStages bool) *domain.TestResult {
	ac := s.activeConn(ctx, c)
	if ac == nil {
		s.noConnection(ctx, c)
		return &domain.TestResult{Passed: false, FailedStage: "environment", Message: "No connection for the active environment.", At: time.Now().UTC()}
	}
	res := s.runProbe(ctx, c, def, ac.Fields, ac.Secret, publishStages)
	prev := c.Observed
	c2, err := s.st.UpdateComponent(ctx, c.ID, func(x *domain.Component) error {
		x.LastTest = res
		if res.Passed {
			x.Observed, x.Message = domain.StateConnected, "Last check passed, "+latencyText(res.LatencyMS)
		} else {
			x.Observed, x.Message = domain.StateDegraded, "Health check failed: "+res.Message
		}
		return nil
	})
	if err == nil {
		s.publish(c2)
		s.recordHealth(ctx, c2, c2.Observed, res.LatencyMS, c2.Message)
		if prev != c2.Observed {
			s.aud.Record(ctx, domain.ActorSystem, "", c.WorkspaceID, "connection.health_changed", def.ID,
				map[string]any{"componentId": c.ID, "from": prev, "to": c2.Observed, "stage": res.FailedStage})
		}
	}
	return res
}

// Rotate replaces secret values. The new credentials must pass a test before
// they replace the stored ones.
func (s *Service) Rotate(ctx context.Context, owner, wsID, id string, newSecrets map[string]string) (*domain.TestResult, *domain.Component, error) {
	c, err := s.component(ctx, owner, wsID, id)
	if err != nil {
		return nil, nil, err
	}
	def, err := s.connectable(c)
	if err != nil || c.Mode != domain.ModeConnected {
		return nil, nil, ErrBadComponent
	}
	allowed := map[string]bool{}
	for _, n := range def.SecretFields() {
		allowed[n] = true
	}
	if len(newSecrets) == 0 {
		return nil, nil, vErr("secrets", "secrets_required", "Enter the new credentials.")
	}
	for k := range newSecrets {
		if !allowed[k] {
			return nil, nil, vErr("secrets", "unknown_secret", "That field is not a credential.")
		}
	}
	if !s.conn.allow(id) {
		return nil, nil, ErrRateLimited
	}
	fields, sec := s.storedFields(ctx, c)
	for k, v := range newSecrets {
		fields[k], sec[k] = v, v
	}
	res := s.runProbe(ctx, c, def, fields, sec, false)
	if !res.Passed {
		s.aud.User(ctx, owner, wsID, "connection.rotate_failed", def.ID, map[string]any{"componentId": id, "stage": res.FailedStage})
		return res, nil, nil
	}
	if err := s.conn.Secrets.Put(ctx, activeID(id), sec); err != nil {
		return nil, nil, err
	}
	c2, err := s.st.UpdateComponent(ctx, id, func(x *domain.Component) error {
		x.LastTest, x.Observed, x.Message = res, domain.StateConnected, "Credentials rotated. Last check passed, "+latencyText(res.LatencyMS)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	s.publish(c2)
	s.aud.User(ctx, owner, wsID, "connection.rotate_credentials", def.ID, map[string]any{"componentId": id})
	return res, c2, nil
}

// Disconnect removes the connection and its stored secrets from the
// workspace. It never touches data on the external service (PRD 11.6).
func (s *Service) Disconnect(ctx context.Context, owner, wsID, id string) (*domain.Component, error) {
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
	_ = s.conn.Secrets.Delete(ctx, activeID(id))
	_ = s.conn.Secrets.Delete(ctx, pendingID(id))
	for env := range c.Profiles {
		_ = s.conn.Secrets.Delete(ctx, profileSecretID(id, env))
		_ = s.conn.Secrets.Delete(ctx, profilePendingID(id, env))
		_ = s.st.DeleteDoc(ctx, ptestKind, id+"@"+env)
	}
	name := c.ConnName
	c2, err := s.st.UpdateComponent(ctx, id, func(x *domain.Component) error {
		x.Mode, x.Connection, x.ConnName, x.Environment, x.SecretRef, x.LastTest, x.Profiles = domain.ModeUnset, nil, "", "", "", nil, nil
		x.Desired, x.Observed = domain.StateDisconnect, domain.StateDisconnect
		x.Message = "Disconnected. Data on the external service was not touched."
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish(c2)
	s.aud.User(ctx, owner, wsID, "connection.disconnect", def.ID, map[string]any{"componentId": id, "connName": name})
	return c2, nil
}

// ConnectionEnv returns the environment for every connected component of a
// workspace, secrets included. It is served only to the workspace agent.
func (s *Service) ConnectionEnv(ctx context.Context, wsID string) (map[string]string, error) {
	comps, err := s.st.ListComponents(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range comps {
		if c.Mode != domain.ModeConnected || c.ConnName == "" {
			continue
		}
		ac := s.activeConn(ctx, c)
		if ac == nil {
			continue // nothing for the active environment: never fall back to another one's values
		}
		for k, v := range connenv.Build(c.CatalogID, c.ConnName, ac.Fields) {
			out[k] = v
		}
	}
	return out, nil
}

// ExposedAs lists the variable names (never values) a connection provides.
func (s *Service) ExposedAs(c *domain.Component) []string {
	env, strict := s.activeEnv(context.Background(), c.WorkspaceID)
	if !HasConnectionFor(c, env, strict) {
		return nil
	}
	return connenv.Names(c.CatalogID, c.ConnName)
}

// HealthOnce checks every connected component once.
func (s *Service) HealthOnce(ctx context.Context) {
	if s.conn == nil {
		return
	}
	list, err := s.st.ListWorkspaces(ctx, "")
	if err != nil {
		return
	}
	for _, w := range list {
		s.HealthForWorkspace(ctx, w.ID)
	}
}

// HealthForWorkspace checks the connected components of one workspace. It runs
// when a workspace resumes so cards report observed state, not stale state.
func (s *Service) HealthForWorkspace(ctx context.Context, wsID string) {
	if s.conn == nil {
		return
	}
	comps, err := s.st.ListComponents(ctx, wsID)
	if err != nil {
		return
	}
	for _, c := range comps {
		if c.Mode != domain.ModeConnected {
			continue
		}
		if def, ok := s.cat.Get(c.CatalogID); ok {
			s.applyHealth(ctx, c, def, false)
		}
	}
}

// RunHealth runs HealthOnce on an interval until ctx ends.
func (s *Service) RunHealth(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.HealthOnce(ctx)
		}
	}
}

var _ = strings.TrimSpace
var _ = store.ErrNotFound

// latencyText formats a probe latency; sub-millisecond results read "<1 ms".
func latencyText(ms int64) string {
	if ms < 1 {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", ms)
}
