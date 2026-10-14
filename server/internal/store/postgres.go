package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Postgres is the production Store. Entities are stored as JSONB documents
// next to the few columns needed for lookup, which keeps the desired/observed
// state model easy to evolve.
type Postgres struct{ pool *pgxpool.Pool }

func Migrate(ctx context.Context, url string) error {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	return migrate(ctx, db)
}

func migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, "migrations")
}

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	if err := Migrate(ctx, url); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrConflict
	}
	return err
}

// docGet loads one document from table by id.
func docGet[T any](ctx context.Context, p *Postgres, table, id string) (*T, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, "SELECT data FROM "+table+" WHERE id=$1", id).Scan(&raw)
	if err != nil {
		return nil, mapErr(err)
	}
	v := new(T)
	return v, json.Unmarshal(raw, v)
}

// docList loads documents matching a WHERE clause.
func docList[T any](ctx context.Context, p *Postgres, query string, args ...any) ([]*T, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		v := new(T)
		if err := json.Unmarshal(raw, v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// docUpdate applies fn to the row under a row lock and writes the result back.
// extra is an optional SET clause fragment (e.g. ", status=$3") with its args.
func docUpdate[T any](ctx context.Context, p *Postgres, table, id string, fn func(*T) error, extra func(*T) (string, []any)) (*T, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT data FROM "+table+" WHERE id=$1 FOR UPDATE", id).Scan(&raw); err != nil {
		return nil, mapErr(err)
	}
	v := new(T)
	if err := json.Unmarshal(raw, v); err != nil {
		return nil, err
	}
	if err := fn(v); err != nil {
		return nil, err
	}
	nb, _ := json.Marshal(v)
	set, args := "", []any{nb, id}
	if extra != nil {
		frag, more := extra(v)
		set, args = frag, append(args, more...)
	}
	if _, err := tx.Exec(ctx, "UPDATE "+table+" SET data=$1"+set+" WHERE id=$2", args...); err != nil {
		return nil, mapErr(err)
	}
	return v, tx.Commit(ctx)
}

func (p *Postgres) exec(ctx context.Context, q string, args ...any) error {
	_, err := p.pool.Exec(ctx, q, args...)
	return mapErr(err)
}

// --- workspaces

func (p *Postgres) CreateWorkspace(ctx context.Context, w *domain.Workspace) error {
	b, _ := json.Marshal(w)
	return p.exec(ctx, "INSERT INTO workspaces (id, owner_id, name, data) VALUES ($1,$2,$3,$4)", w.ID, w.OwnerID, w.Name, b)
}

func (p *Postgres) GetWorkspace(ctx context.Context, id string) (*domain.Workspace, error) {
	return docGet[domain.Workspace](ctx, p, "workspaces", id)
}

func (p *Postgres) ListWorkspaces(ctx context.Context, owner string) ([]*domain.Workspace, error) {
	return docList[domain.Workspace](ctx, p,
		"SELECT data FROM workspaces WHERE ($1='' OR owner_id=$1) ORDER BY data->>'lastOpenedAt' DESC", owner)
}

func (p *Postgres) UpdateWorkspace(ctx context.Context, id string, fn func(*domain.Workspace) error) (*domain.Workspace, error) {
	return docUpdate(ctx, p, "workspaces", id, fn, func(w *domain.Workspace) (string, []any) {
		return ", name=$3", []any{w.Name}
	})
}

func (p *Postgres) DeleteWorkspace(ctx context.Context, id string) error {
	ct, err := p.pool.Exec(ctx, "DELETE FROM workspaces WHERE id=$1", id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = p.pool.Exec(ctx, "DELETE FROM agent_configs WHERE workspace_id=$1", id)
	return err
}

// --- components

func (p *Postgres) CreateComponent(ctx context.Context, c *domain.Component) error {
	b, _ := json.Marshal(c)
	return p.exec(ctx, "INSERT INTO components (id, workspace_id, data) VALUES ($1,$2,$3)", c.ID, c.WorkspaceID, b)
}

func (p *Postgres) GetComponent(ctx context.Context, id string) (*domain.Component, error) {
	return docGet[domain.Component](ctx, p, "components", id)
}

func (p *Postgres) ListComponents(ctx context.Context, wsID string) ([]*domain.Component, error) {
	return docList[domain.Component](ctx, p, "SELECT data FROM components WHERE workspace_id=$1 ORDER BY created_at", wsID)
}

func (p *Postgres) UpdateComponent(ctx context.Context, id string, fn func(*domain.Component) error) (*domain.Component, error) {
	return docUpdate(ctx, p, "components", id, func(c *domain.Component) error {
		if err := fn(c); err != nil {
			return err
		}
		c.UpdatedAt = time.Now().UTC()
		return nil
	}, nil)
}

func (p *Postgres) DeleteComponent(ctx context.Context, id string) error {
	ct, err := p.pool.Exec(ctx, "DELETE FROM components WHERE id=$1", id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- operations

func (p *Postgres) CreateOperation(ctx context.Context, o *domain.Operation) error {
	b, _ := json.Marshal(o)
	return p.exec(ctx, "INSERT INTO operations (id, workspace_id, status, data) VALUES ($1,$2,$3,$4)", o.ID, o.WorkspaceID, o.Status, b)
}

func (p *Postgres) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	return docGet[domain.Operation](ctx, p, "operations", id)
}

func (p *Postgres) ListOperations(ctx context.Context, wsID string) ([]*domain.Operation, error) {
	return docList[domain.Operation](ctx, p, "SELECT data FROM operations WHERE workspace_id=$1 ORDER BY created_at DESC", wsID)
}

func (p *Postgres) ListOperationsByStatus(ctx context.Context, status string) ([]*domain.Operation, error) {
	return docList[domain.Operation](ctx, p, "SELECT data FROM operations WHERE status=$1 ORDER BY created_at", status)
}

func (p *Postgres) UpdateOperation(ctx context.Context, id string, fn func(*domain.Operation) error) (*domain.Operation, error) {
	return docUpdate(ctx, p, "operations", id, func(o *domain.Operation) error {
		if err := fn(o); err != nil {
			return err
		}
		o.UpdatedAt = time.Now().UTC()
		return nil
	}, func(o *domain.Operation) (string, []any) { return ", status=$3", []any{o.Status} })
}

// --- agent configs

func (p *Postgres) CreateAgentConfig(ctx context.Context, c *domain.AgentConfig) error {
	b, _ := json.Marshal(c)
	return p.exec(ctx, "INSERT INTO agent_configs (id, workspace_id, data) VALUES ($1,$2,$3)", c.ID, c.WorkspaceID, b)
}

func (p *Postgres) GetAgentConfig(ctx context.Context, id string) (*domain.AgentConfig, error) {
	return docGet[domain.AgentConfig](ctx, p, "agent_configs", id)
}

func (p *Postgres) ListAgentConfigs(ctx context.Context, wsID string) ([]*domain.AgentConfig, error) {
	return docList[domain.AgentConfig](ctx, p, "SELECT data FROM agent_configs WHERE workspace_id=$1 ORDER BY created_at", wsID)
}

func (p *Postgres) UpdateAgentConfig(ctx context.Context, id string, fn func(*domain.AgentConfig) error) (*domain.AgentConfig, error) {
	return docUpdate(ctx, p, "agent_configs", id, fn, nil)
}

func (p *Postgres) DeleteAgentConfig(ctx context.Context, id string) error {
	ct, err := p.pool.Exec(ctx, "DELETE FROM agent_configs WHERE id=$1", id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- audit and secrets

func (p *Postgres) AppendAudit(ctx context.Context, e *domain.AuditEvent) error {
	b, _ := json.Marshal(e)
	return p.exec(ctx, "INSERT INTO audit_events (id, workspace_id, data, at) VALUES ($1,$2,$3,$4)", e.ID, e.WorkspaceID, b, e.At)
}

func (p *Postgres) ListAudit(ctx context.Context, wsID string, limit int) ([]*domain.AuditEvent, error) {
	if limit <= 0 {
		limit = 1000
	}
	return docList[domain.AuditEvent](ctx, p,
		"SELECT data FROM audit_events WHERE ($1='' OR workspace_id=$1) ORDER BY at DESC LIMIT $2", wsID, limit)
}

func (p *Postgres) PutSecret(ctx context.Context, s *domain.SecretRecord) error {
	return p.exec(ctx, `INSERT INTO secrets (id, ciphertext, wrapped_key) VALUES ($1,$2,$3)
		ON CONFLICT (id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext, wrapped_key=EXCLUDED.wrapped_key`,
		s.ID, s.Ciphertext, s.WrappedKey)
}

func (p *Postgres) GetSecret(ctx context.Context, id string) (*domain.SecretRecord, error) {
	s := &domain.SecretRecord{ID: id}
	err := p.pool.QueryRow(ctx, "SELECT ciphertext, wrapped_key, created_at FROM secrets WHERE id=$1", id).
		Scan(&s.Ciphertext, &s.WrappedKey, &s.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return s, nil
}

func (p *Postgres) DeleteSecret(ctx context.Context, id string) error {
	return p.exec(ctx, "DELETE FROM secrets WHERE id=$1", id)
}

func (p *Postgres) CreateUser(ctx context.Context, u *domain.User) error {
	b, _ := json.Marshal(u)
	return p.exec(ctx, "INSERT INTO users (id, email, data) VALUES ($1,$2,$3)", u.ID, u.Email, b)
}

func (p *Postgres) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return docGet[domain.User](ctx, p, "users", id)
}

func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var raw []byte
	if err := p.pool.QueryRow(ctx, "SELECT data FROM users WHERE email=$1", email).Scan(&raw); err != nil {
		return nil, mapErr(err)
	}
	u := new(domain.User)
	return u, json.Unmarshal(raw, u)
}

func (p *Postgres) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n, err
}

func (p *Postgres) PutSession(ctx context.Context, s *domain.Session) error {
	b, _ := json.Marshal(s)
	return p.exec(ctx, `INSERT INTO sessions (id, user_id, expires_at, data) VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET data=EXCLUDED.data, expires_at=EXCLUDED.expires_at`, s.ID, s.UserID, s.ExpiresAt, b)
}

func (p *Postgres) GetSession(ctx context.Context, id string) (*domain.Session, error) {
	return docGet[domain.Session](ctx, p, "sessions", id)
}

func (p *Postgres) DeleteSession(ctx context.Context, id string) error {
	return p.exec(ctx, "DELETE FROM sessions WHERE id=$1", id)
}
