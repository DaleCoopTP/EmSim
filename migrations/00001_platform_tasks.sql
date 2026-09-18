-- Platform task queue: the durable, polymorphic-scope job table the worker
-- claims from (design-docs/contracts/schema.sql, tasks.schema.json). Ported
-- by pattern from orchestration-core's tasks table (see ADR-010): the claim/
-- heartbeat/terminal SQL and the CHECK-based state machine are the same
-- shape, but there is no FK to a domain table — tasks reference their owner
-- by scope_type/scope_id, and a task's own failure never cascades into
-- domain state (docs/technical-discovery.md §3.1, §6).

-- +goose Up
CREATE TABLE tasks (
    id               uuid PRIMARY KEY,
    priority         smallint NOT NULL DEFAULT 50 CHECK (priority >= 0), -- assessment 100 / generation 50 / advice 10
    dependency_task_ids uuid[] NOT NULL DEFAULT '{}', -- удерживать STT result до sealed input/отмены
    kind             text NOT NULL,                    -- реестр в коде: scenario.generate, voice.render, ...
    scope_type       text NOT NULL,                    -- scenario | item | lesson | call | user | system
    scope_id         uuid,
    dedup_key        text NOT NULL UNIQUE,             -- kind:scope_id[:suffix]
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    status           text NOT NULL CHECK (status IN ('waiting', 'pending', 'leased', 'done', 'failed', 'dead_letter', 'cancelled')),
    attempts         integer NOT NULL DEFAULT 0,
    max_attempts     integer NOT NULL,
    lease_token      bigint NOT NULL DEFAULT 0,
    leased_worker    text,
    lease_started_at timestamptz,
    lease_expires_at timestamptz,
    terminal_worker  text,
    next_attempt_at  timestamptz,
    wait_until       timestamptz,                      -- следующая проверка зависимостей; не общий timeout STT
    wait_reason      text,
    last_error_code  text,
    result           jsonb,
    terminal_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tasks_kind_shape CHECK (kind ~ '^[a-z]+(\.[a-z_]+)+$'),
    CONSTRAINT tasks_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT tasks_error_shape CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z0-9_]{1,64}$'),
    CONSTRAINT tasks_attempt_budget CHECK (attempts >= 0 AND max_attempts > 0 AND attempts <= max_attempts AND lease_token = attempts::bigint),
    CONSTRAINT tasks_lease_shape CHECK (
        (leased_worker IS NULL AND lease_started_at IS NULL AND lease_expires_at IS NULL) OR
        (leased_worker IS NOT NULL AND lease_started_at IS NOT NULL AND lease_expires_at IS NOT NULL AND lease_expires_at > lease_started_at)
    ),
    CONSTRAINT tasks_state_shape CHECK (
        (status = 'waiting'     AND attempts = 0 AND leased_worker IS NULL AND terminal_worker IS NULL AND next_attempt_at IS NULL AND terminal_at IS NULL AND wait_until IS NOT NULL AND wait_reason IS NOT NULL) OR
        (status = 'pending'     AND attempts < max_attempts AND leased_worker IS NULL AND terminal_worker IS NULL AND next_attempt_at IS NOT NULL AND terminal_at IS NULL) OR
        (status = 'leased'      AND attempts > 0 AND leased_worker IS NOT NULL AND terminal_worker IS NULL AND next_attempt_at IS NULL AND terminal_at IS NULL) OR
        (status = 'done'        AND attempts > 0 AND leased_worker IS NULL AND terminal_worker IS NOT NULL AND next_attempt_at IS NULL AND last_error_code IS NULL AND terminal_at IS NOT NULL) OR
        (status = 'failed'      AND attempts >= 0 AND leased_worker IS NULL AND terminal_worker IS NOT NULL AND next_attempt_at IS NULL AND last_error_code IS NOT NULL AND terminal_at IS NOT NULL) OR
        (status = 'cancelled' AND leased_worker IS NULL AND terminal_worker IS NULL AND next_attempt_at IS NULL AND terminal_at IS NOT NULL) OR
        (status = 'dead_letter' AND attempts = max_attempts AND leased_worker IS NULL AND terminal_worker IS NOT NULL AND next_attempt_at IS NULL AND last_error_code IS NOT NULL AND terminal_at IS NOT NULL)
    )
);
CREATE INDEX tasks_pending_claim_idx ON tasks (priority DESC, next_attempt_at, created_at, id) WHERE status = 'pending';
CREATE INDEX tasks_leased_expiry_idx ON tasks (lease_expires_at, id) WHERE status = 'leased';
CREATE INDEX tasks_waiting_idx ON tasks (wait_until, id) WHERE status = 'waiting';
CREATE INDEX tasks_scope_idx ON tasks (scope_type, scope_id);

-- +goose Down
DROP TABLE tasks;
