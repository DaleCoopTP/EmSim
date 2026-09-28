-- DDS-10 part 1 (ADR-033): platform maintenance.
--
-- audit_log_at_brin_idx serves audit.prune's "older than N days" delete
-- (RFC-001 §9: audit is kept 6 months). audit_log rows arrive in `at`
-- order, so a BRIN index is small and enough for a range scan.
--
-- platform_heartbeats is the last state a worker observed for a component
-- the api cannot see itself (the api has no route to the inference
-- network): the model's health and free space in the backup directory.
-- One row per component, overwritten on every check; the admin status
-- screen reads it. Owned by internal/platform.

-- +goose Up
CREATE INDEX audit_log_at_brin_idx ON audit_log USING brin (at);

CREATE TABLE platform_heartbeats (
    component  text PRIMARY KEY CHECK (component ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    status     text NOT NULL CHECK (status IN ('ok', 'unavailable')),
    detail     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object'),
    checked_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE platform_heartbeats;
DROP INDEX audit_log_at_brin_idx;
