-- Platform audit log: one row per domain effect, written in the same
-- transaction as that effect (design-docs/contracts/schema.sql, RFC-001
-- §9: "Audit: каждый эффект — строка в audit_log в той же транзакции").
-- Owned by internal/platform/audit; every product module writes through
-- it instead of its own table (CLAUDE.md: "A module writes only its own
-- tables" — audit_log belongs to platform, not to auth/content/...).

-- +goose Up
CREATE TABLE audit_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz NOT NULL DEFAULT now(),
    actor_id      uuid,                                -- NULL для системных действий
    actor_role    text,
    action        text NOT NULL,                       -- lesson.start, item.action, assessment.revise ...
    resource_type text NOT NULL,
    resource_id   uuid,
    outcome       text NOT NULL CHECK (outcome IN ('ok', 'rejected', 'error')),
    request_id    text,
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,  -- никогда: пароли, тексты обучаемых, ФИО
    CONSTRAINT audit_details_object CHECK (jsonb_typeof(details) = 'object')
);
CREATE INDEX audit_log_resource_idx ON audit_log (resource_type, resource_id, at);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id, at);

-- +goose Down
DROP TABLE audit_log;
