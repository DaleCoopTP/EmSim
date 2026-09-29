-- ADR-038: maintenance mode. One row; while `enabled`, a new lesson (or a
-- 112 scenario preview) cannot be started, so the administrator can
-- update or restore the installation without new work arriving.
-- Lessons already running, their commands and the trainees' screens are
-- untouched. Owned by internal/platform/maintenance.
--
-- No foreign key on set_by: like audit_log.actor_id, it is only a record
-- of who, and must not stop a user from ever being removed.

-- +goose Up
CREATE TABLE platform_maintenance (
    id      boolean PRIMARY KEY DEFAULT true CHECK (id),
    enabled boolean NOT NULL DEFAULT false,
    reason  text NOT NULL DEFAULT '' CHECK (char_length(reason) <= 200),
    set_by  uuid,
    set_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO platform_maintenance (id) VALUES (true);

-- +goose Down
DROP TABLE platform_maintenance;
