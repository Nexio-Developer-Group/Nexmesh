-- +goose Up
CREATE TABLE workspaces (
    id         text PRIMARY KEY,
    owner_id   text NOT NULL,
    name       text NOT NULL,
    data       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

CREATE TABLE components (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    data         jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX components_workspace_idx ON components (workspace_id);

CREATE TABLE operations (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    status       text NOT NULL,
    data         jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX operations_status_idx ON operations (status, created_at);
CREATE INDEX operations_workspace_idx ON operations (workspace_id);

CREATE TABLE agent_configs (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL,
    data         jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_configs_workspace_idx ON agent_configs (workspace_id);

CREATE TABLE audit_events (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL DEFAULT '',
    data         jsonb NOT NULL,
    at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_workspace_idx ON audit_events (workspace_id, at DESC);

CREATE TABLE secrets (
    id          text PRIMARY KEY,
    ciphertext  bytea NOT NULL,
    wrapped_key bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE secrets;
DROP TABLE audit_events;
DROP TABLE agent_configs;
DROP TABLE operations;
DROP TABLE components;
DROP TABLE workspaces;
