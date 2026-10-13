// Package store persists Nexmesh entities. Two implementations exist: an
// in-memory store (tests, local UI development) and PostgreSQL.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

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

	Close()
}

// NewID returns a random 16-byte hex identifier with a type prefix.
func NewID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
