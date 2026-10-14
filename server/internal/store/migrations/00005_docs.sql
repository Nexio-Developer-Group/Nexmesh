-- +goose Up
-- Generic per-workspace documents (connections, change sets, rules, gates, ...).
CREATE TABLE docs (
    kind         text NOT NULL,
    id           text NOT NULL,
    workspace_id text NOT NULL DEFAULT '',
    data         jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, id)
);
CREATE INDEX docs_ws_idx ON docs (kind, workspace_id, created_at);

-- +goose Down
DROP TABLE docs;
