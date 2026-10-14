-- +goose Up
CREATE TABLE templates (
    id         text PRIMARY KEY,
    owner_id   text NOT NULL,
    name       text NOT NULL,
    data       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

-- +goose Down
DROP TABLE templates;
