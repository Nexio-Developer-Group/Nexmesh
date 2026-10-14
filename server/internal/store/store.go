// Package store persists Nexmesh entities. Two implementations exist: an
// in-memory store (tests, local UI development) and PostgreSQL.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// MaxHealthSamples bounds stored health history per component.
const MaxHealthSamples = 500

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Store is the persistence boundary. Update methods take a mutator that runs
// atomically against the latest stored value.
type Store interface {
	CreateWorkspace(ctx context.Context, w *domain.Workspace) error
	GetWorkspace(ctx context.Context, id string) (*domain.Workspace, error)
	ListWorkspaces(ctx context.Context, ownerID string) ([]*domain.Workspace, error)
	UpdateWorkspace(ctx context.Context, id string, fn func(*domain.Workspace) error) (*domain.Workspace, error)
	DeleteWorkspace(ctx context.Context, id string) error

	CreateComponent(ctx context.Context, c *domain.Component) error
	GetComponent(ctx context.Context, id string) (*domain.Component, error)
	ListComponents(ctx context.Context, workspaceID string) ([]*domain.Component, error)
	UpdateComponent(ctx context.Context, id string, fn func(*domain.Component) error) (*domain.Component, error)
	DeleteComponent(ctx context.Context, id string) error

	CreateOperation(ctx context.Context, o *domain.Operation) error
	GetOperation(ctx context.Context, id string) (*domain.Operation, error)
	ListOperations(ctx context.Context, workspaceID string) ([]*domain.Operation, error)
	ListOperationsByStatus(ctx context.Context, status string) ([]*domain.Operation, error)
	UpdateOperation(ctx context.Context, id string, fn func(*domain.Operation) error) (*domain.Operation, error)

	CreateAgentConfig(ctx context.Context, c *domain.AgentConfig) error
	GetAgentConfig(ctx context.Context, id string) (*domain.AgentConfig, error)
	ListAgentConfigs(ctx context.Context, workspaceID string) ([]*domain.AgentConfig, error)
	UpdateAgentConfig(ctx context.Context, id string, fn func(*domain.AgentConfig) error) (*domain.AgentConfig, error)
	DeleteAgentConfig(ctx context.Context, id string) error

	AppendAudit(ctx context.Context, e *domain.AuditEvent) error
	ListAudit(ctx context.Context, workspaceID string, limit int) ([]*domain.AuditEvent, error)

	PutSecret(ctx context.Context, s *domain.SecretRecord) error
	GetSecret(ctx context.Context, id string) (*domain.SecretRecord, error)
	DeleteSecret(ctx context.Context, id string) error

	CreateUser(ctx context.Context, u *domain.User) error // ErrConflict on duplicate email
	GetUser(ctx context.Context, id string) (*domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	CountUsers(ctx context.Context) (int, error)

	// AppendHealth records a health sample; the store keeps the newest MaxHealthSamples per component.
	AppendHealth(ctx context.Context, componentID string, h *domain.HealthSample) error
	// ListHealth returns samples newest first.
	ListHealth(ctx context.Context, componentID string, limit int) ([]*domain.HealthSample, error)

	PutTemplate(ctx context.Context, t *domain.Template) error // ErrConflict when the owner already has that name
	GetTemplate(ctx context.Context, id string) (*domain.Template, error)
	ListTemplates(ctx context.Context, ownerID string) ([]*domain.Template, error)
	DeleteTemplate(ctx context.Context, id string) error

	PutSession(ctx context.Context, s *domain.Session) error
	GetSession(ctx context.Context, id string) (*domain.Session, error)
	DeleteSession(ctx context.Context, id string) error

	// Documents are small JSON records grouped by kind and workspace, used by
	// features that do not need their own table (connections, change sets, ...).
	// PutDoc inserts or replaces. ListDocs returns oldest first. Deleting a
	// workspace deletes its documents.
	PutDoc(ctx context.Context, kind, workspaceID, id string, data []byte) error
	GetDoc(ctx context.Context, kind, id string) ([]byte, error)
	ListDocs(ctx context.Context, kind, workspaceID string) ([][]byte, error)
	DeleteDoc(ctx context.Context, kind, id string) error

	Close()
}

// PutJSON stores v as a document.
func PutJSON(ctx context.Context, s Store, kind, workspaceID, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.PutDoc(ctx, kind, workspaceID, id, b)
}

// GetJSON loads a document into a new T.
func GetJSON[T any](ctx context.Context, s Store, kind, id string) (*T, error) {
	b, err := s.GetDoc(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	v := new(T)
	if err := json.Unmarshal(b, v); err != nil {
		return nil, err
	}
	return v, nil
}

// ListJSON loads every document of a kind in a workspace.
func ListJSON[T any](ctx context.Context, s Store, kind, workspaceID string) ([]*T, error) {
	raw, err := s.ListDocs(ctx, kind, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]*T, 0, len(raw))
	for _, b := range raw {
		v := new(T)
		if json.Unmarshal(b, v) == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// NewID returns a random 16-byte hex identifier with a type prefix.
func NewID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
