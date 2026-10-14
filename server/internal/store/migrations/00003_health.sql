-- +goose Up
CREATE TABLE health_samples (
    component_id text NOT NULL,
    at           timestamptz NOT NULL,
    data         jsonb NOT NULL
);
CREATE INDEX health_samples_component_idx ON health_samples (component_id, at DESC);

-- +goose Down
DROP TABLE health_samples;
