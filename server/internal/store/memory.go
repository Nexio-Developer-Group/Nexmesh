package store

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Memory is a thread-safe in-memory Store. Values are stored as JSON so that
// callers can never mutate stored state through returned pointers.
type Memory struct {
	mu       sync.Mutex
	ws       map[string][]byte
	comps    map[string][]byte
	ops      map[string][]byte
	agents   map[string][]byte
	audit    []*domain.AuditEvent
	secrets  map[string]*domain.SecretRecord
	users    map[string][]byte
	sessions map[string][]byte
}

func NewMemory() *Memory {
	return &Memory{
		ws: map[string][]byte{}, comps: map[string][]byte{}, ops: map[string][]byte{},
		agents: map[string][]byte{}, users: map[string][]byte{}, sessions: map[string][]byte{}, secrets: map[string]*domain.SecretRecord{},
	}
}

func (m *Memory) Close() {}

func put[T any](tbl map[string][]byte, id string, v *T) {
	b, _ := json.Marshal(v)
	tbl[id] = b
}

func get[T any](tbl map[string][]byte, id string) (*T, error) {
	b, ok := tbl[id]
	if !ok {
		return nil, ErrNotFound
	}
	v := new(T)
	_ = json.Unmarshal(b, v)
	return v, nil
}

func all[T any](tbl map[string][]byte, keep func(*T) bool, less func(a, b *T) bool) []*T {
	out := []*T{}
	for _, b := range tbl {
		v := new(T)
		_ = json.Unmarshal(b, v)
		if keep(v) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func update[T any](tbl map[string][]byte, id string, fn func(*T) error) (*T, error) {
	v, err := get[T](tbl, id)
	if err != nil {
		return nil, err
	}
	if err := fn(v); err != nil {
		return nil, err
	}
	put(tbl, id, v)
	return v, nil
}

// --- workspaces

func (m *Memory) CreateWorkspace(_ context.Context, w *domain.Workspace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.ws {
		var e domain.Workspace
		_ = json.Unmarshal(b, &e)
		if e.OwnerID == w.OwnerID && e.Name == w.Name {
			return ErrConflict
		}
	}
	put(m.ws, w.ID, w)
	return nil
}

func (m *Memory) GetWorkspace(_ context.Context, id string) (*domain.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return get[domain.Workspace](m.ws, id)
}

func (m *Memory) ListWorkspaces(_ context.Context, owner string) ([]*domain.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return all(m.ws, func(w *domain.Workspace) bool { return owner == "" || w.OwnerID == owner },
		func(a, b *domain.Workspace) bool { return a.LastOpened.After(b.LastOpened) }), nil
}

func (m *Memory) UpdateWorkspace(_ context.Context, id string, fn func(*domain.Workspace) error) (*domain.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return update(m.ws, id, fn)
}

func (m *Memory) DeleteWorkspace(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ws[id]; !ok {
		return ErrNotFound
	}
	delete(m.ws, id)
	for k, b := range m.comps {
		var c domain.Component
		_ = json.Unmarshal(b, &c)
		if c.WorkspaceID == id {
			delete(m.comps, k)
		}
	}
	for k, b := range m.agents {
		var c domain.AgentConfig
		_ = json.Unmarshal(b, &c)
		if c.WorkspaceID == id {
			delete(m.agents, k)
		}
	}
	return nil
}

// --- components

func (m *Memory) CreateComponent(_ context.Context, c *domain.Component) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	put(m.comps, c.ID, c)
	return nil
}

func (m *Memory) GetComponent(_ context.Context, id string) (*domain.Component, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return get[domain.Component](m.comps, id)
}

func (m *Memory) ListComponents(_ context.Context, wsID string) ([]*domain.Component, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return all(m.comps, func(c *domain.Component) bool { return c.WorkspaceID == wsID },
		func(a, b *domain.Component) bool { return a.CreatedAt.Before(b.CreatedAt) }), nil
}

func (m *Memory) UpdateComponent(_ context.Context, id string, fn func(*domain.Component) error) (*domain.Component, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return update(m.comps, id, func(c *domain.Component) error {
		if err := fn(c); err != nil {
			return err
		}
		c.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (m *Memory) DeleteComponent(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.comps[id]; !ok {
		return ErrNotFound
	}
	delete(m.comps, id)
	return nil
}

// --- operations

func (m *Memory) CreateOperation(_ context.Context, o *domain.Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	put(m.ops, o.ID, o)
	return nil
}

func (m *Memory) GetOperation(_ context.Context, id string) (*domain.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return get[domain.Operation](m.ops, id)
}

func (m *Memory) ListOperations(_ context.Context, wsID string) ([]*domain.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return all(m.ops, func(o *domain.Operation) bool { return o.WorkspaceID == wsID },
		func(a, b *domain.Operation) bool { return a.CreatedAt.After(b.CreatedAt) }), nil
}

func (m *Memory) ListOperationsByStatus(_ context.Context, status string) ([]*domain.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return all(m.ops, func(o *domain.Operation) bool { return o.Status == status },
		func(a, b *domain.Operation) bool { return a.CreatedAt.Before(b.CreatedAt) }), nil
}

func (m *Memory) UpdateOperation(_ context.Context, id string, fn func(*domain.Operation) error) (*domain.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return update(m.ops, id, func(o *domain.Operation) error {
		if err := fn(o); err != nil {
			return err
		}
		o.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// --- agent configs

func (m *Memory) CreateAgentConfig(_ context.Context, c *domain.AgentConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	put(m.agents, c.ID, c)
	return nil
}

func (m *Memory) GetAgentConfig(_ context.Context, id string) (*domain.AgentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return get[domain.AgentConfig](m.agents, id)
}

func (m *Memory) ListAgentConfigs(_ context.Context, wsID string) ([]*domain.AgentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return all(m.agents, func(c *domain.AgentConfig) bool { return c.WorkspaceID == wsID },
		func(a, b *domain.AgentConfig) bool { return a.CreatedAt.Before(b.CreatedAt) }), nil
}

func (m *Memory) UpdateAgentConfig(_ context.Context, id string, fn func(*domain.AgentConfig) error) (*domain.AgentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return update(m.agents, id, fn)
}

func (m *Memory) DeleteAgentConfig(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[id]; !ok {
		return ErrNotFound
	}
	delete(m.agents, id)
	return nil
}

// --- audit and secrets

func (m *Memory) AppendAudit(_ context.Context, e *domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *e
	m.audit = append(m.audit, &cp)
	return nil
}

func (m *Memory) ListAudit(_ context.Context, wsID string, limit int) ([]*domain.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*domain.AuditEvent{}
	for i := len(m.audit) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if wsID == "" || m.audit[i].WorkspaceID == wsID {
			cp := *m.audit[i]
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *Memory) PutSecret(_ context.Context, s *domain.SecretRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	m.secrets[s.ID] = &cp
	return nil
}

func (m *Memory) GetSecret(_ context.Context, id string) (*domain.SecretRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.secrets[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *Memory) DeleteSecret(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, id)
	return nil
}

func (m *Memory) CreateUser(_ context.Context, u *domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.users {
		var o domain.User
		_ = json.Unmarshal(b, &o)
		if o.Email == u.Email {
			return ErrConflict
		}
	}
	put(m.users, u.ID, u)
	return nil
}

func (m *Memory) GetUser(_ context.Context, id string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return get[domain.User](m.users, id)
}

func (m *Memory) GetUserByEmail(_ context.Context, email string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.users {
		u := new(domain.User)
		_ = json.Unmarshal(b, u)
		if u.Email == email {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) CountUsers(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *Memory) PutSession(_ context.Context, s *domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	put(m.sessions, s.ID, s)
	return nil
}

func (m *Memory) GetSession(_ context.Context, id string) (*domain.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return get[domain.Session](m.sessions, id)
}

func (m *Memory) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}
