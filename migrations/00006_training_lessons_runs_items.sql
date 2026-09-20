-- training module tables (RFC-001 §4.2/§6, slice-planning.md §4, ADR-004/
-- 011/015/017): lessons, assignments, runs, items, actions, evidence.
-- 1:1 with design-docs/contracts/schema.sql's training section, restricted
-- to what slice 3 actually needs: item_events, calls, control_reports and
-- every assessment table are later slices (4-6) and are not created here.
--
-- Slice 3 narrows two columns beyond what a later, more general slice
-- will eventually need, per ADR-017:
--   - assignments.user_id is NOT NULL (the "whoever logs into the
--     workstation" NULL variant is out of scope for the MVP);
--   - runs gets one partial-unique index per user and per workstation
--     (state='active') on top of schema.sql's per-lesson UNIQUEs, so
--     GET /my/run has exactly one candidate row across all lessons, not
--     just within one lesson.
-- items.card/workflow/pilot_goal and actions.effect are new columns
-- introduced in this slice (ADR-017) that schema.sql already documents
-- as part of the same target design, not a later addition on top of it.
--
-- reject_immutable_change() is the generic append-only/immutability
-- guard schema.sql specifies for evidence and actions (distinct from
-- 00004's scenario_versions-specific protect_scenario_version_content);
-- first created here since nothing before this migration needed it.

-- +goose Up
CREATE TABLE lessons (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type = 'dds_processing'),
    id            uuid PRIMARY KEY,
    instructor_id uuid NOT NULL REFERENCES users(id),
    title         text NOT NULL,
    mode          text NOT NULL CHECK (mode IN ('intro', 'training')),
    level         text NOT NULL CHECK (level IN ('easy', 'medium', 'hard')),
    state         text NOT NULL CHECK (state IN ('draft', 'running', 'stopped', 'finished')),
    epoch         bigint NOT NULL DEFAULT 0,
    timing        jsonb NOT NULL,
    rubric_version text NOT NULL,
    recording_grace_s integer NOT NULL DEFAULT 120 CHECK (recording_grace_s >= 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    stopped_at    timestamptz,
    finished_at   timestamptz,
    CONSTRAINT lessons_timing_object CHECK (jsonb_typeof(timing) = 'object'),
    CONSTRAINT lessons_state_shape CHECK (
        (state = 'draft'    AND started_at IS NULL) OR
        (state = 'running'  AND started_at IS NOT NULL AND stopped_at IS NULL) OR
        (state = 'stopped'  AND started_at IS NOT NULL AND stopped_at IS NOT NULL AND finished_at IS NULL) OR
        (state = 'finished' AND started_at IS NOT NULL AND finished_at IS NOT NULL)
    )
);
CREATE INDEX lessons_instructor_idx ON lessons (instructor_id, created_at DESC);

-- Назначение на рабочее место: кто и какие версии сценариев в каком
-- порядке. user_id NOT NULL — срез 3/ADR-017, см. заголовок файла.
CREATE TABLE assignments (
    lesson_id            uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    workstation_id       uuid NOT NULL REFERENCES workstations(id),
    user_id              uuid NOT NULL REFERENCES users(id),
    scenario_version_ids uuid[] NOT NULL CHECK (cardinality(scenario_version_ids) > 0),
    PRIMARY KEY (lesson_id, workstation_id),
    UNIQUE (lesson_id, user_id)
);

-- Прогон одного обучаемого в занятии.
CREATE TABLE runs (
    exercise_type  text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type = 'dds_processing'),
    id             uuid PRIMARY KEY,
    lesson_id      uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL REFERENCES users(id),
    workstation_id uuid NOT NULL REFERENCES workstations(id),
    mode           text NOT NULL CHECK (mode IN ('intro', 'training')),
    state          text NOT NULL CHECK (state IN ('active', 'finished')),
    level_at_start text NOT NULL CHECK (level_at_start IN ('easy','medium','hard')),
    next_offer_at  timestamptz,
    queue_cursor   integer NOT NULL DEFAULT 0,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    UNIQUE (lesson_id, user_id),
    UNIQUE (lesson_id, workstation_id)
);
-- Один активный прогон на пользователя/РМ одновременно, независимо от
-- занятия (срез 3, ADR-017) — иначе GET /my/run не может однозначно
-- выбрать текущий прогон обучаемого среди нескольких занятий.
CREATE UNIQUE INDEX runs_active_user_idx ON runs (user_id) WHERE state = 'active';
CREATE UNIQUE INDEX runs_active_workstation_idx ON runs (workstation_id) WHERE state = 'active';

-- Карточка у обучаемого. Единственная изменяемая строка карточки.
-- card/workflow/pilot_goal — срез 3/ADR-017, см. заголовок файла.
CREATE TABLE items (
    id                  uuid PRIMARY KEY,
    run_id              uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    scenario_version_id uuid NOT NULL REFERENCES scenario_versions(id),
    ordinal             integer NOT NULL,
    spawned_from        uuid REFERENCES items(id),
    state               text NOT NULL CHECK (state IN ('offered', 'opened', 'in_progress', 'closed', 'interrupted')),
    reaction            text NOT NULL DEFAULT 'added',
    card                jsonb NOT NULL CHECK (jsonb_typeof(card) = 'object'),
    workflow            jsonb NOT NULL CHECK (jsonb_typeof(workflow) = 'object'),
    pilot_goal          text,
    seq                 bigint NOT NULL DEFAULT 0,
    stop_cutoff_log_seq bigint CHECK (stop_cutoff_log_seq IS NULL OR stop_cutoff_log_seq >= 0),
    interruptions       jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(interruptions)='array'),
    log_seq             bigint NOT NULL DEFAULT 0 CHECK (log_seq >= 0),
    timing_effective    jsonb NOT NULL CHECK (jsonb_typeof(timing_effective) = 'object'),
    deadlines           jsonb NOT NULL DEFAULT '{}'::jsonb,
    offered_at          timestamptz NOT NULL DEFAULT now(),
    opened_at           timestamptz,
    primary_at          timestamptz,
    closed_at           timestamptz,
    close_reason        text CHECK (close_reason IN ('completed', 'refused', 'interrupted', 'pilot_completed')),
    UNIQUE (run_id, ordinal),
    CONSTRAINT items_reaction_shape CHECK (reaction IN ('added','received','accepted','not_accepted','responding','arrived','working','completed','refused','completed_without_team')),
    CONSTRAINT items_deadlines_object CHECK (jsonb_typeof(deadlines) = 'object'),
    CONSTRAINT items_state_shape CHECK (
        (state = 'offered'     AND opened_at IS NULL AND closed_at IS NULL) OR
        (state IN ('opened','in_progress') AND opened_at IS NOT NULL AND closed_at IS NULL) OR
        (state IN ('closed','interrupted') AND closed_at IS NOT NULL AND close_reason IS NOT NULL)
    )
);
CREATE INDEX items_run_state_idx ON items (run_id, state);
CREATE INDEX items_open_idx ON items (state) WHERE state IN ('offered', 'opened', 'in_progress');

-- Append-only журнал команд. Отклонённые тоже пишутся. effect — срез
-- 3/ADR-017, см. заголовок файла.
CREATE TABLE actions (
    item_id        uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    seq            bigint NOT NULL,
    log_seq        bigint NOT NULL CHECK (log_seq > 0),
    actor_id       uuid NOT NULL REFERENCES users(id),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    id             uuid NOT NULL UNIQUE,
    command_id     uuid NOT NULL UNIQUE,
    type           text NOT NULL CHECK (type IN ('open','set_status','add_comment','set_card_field','call_start','call_end','control_report','close')),
    payload        jsonb NOT NULL DEFAULT '{}'::jsonb,
    effect         jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(effect) = 'object'),
    accepted       boolean NOT NULL,
    rejection      text,
    receipt        jsonb NOT NULL,
    http_status    integer NOT NULL CHECK (http_status IN (200,409,422)),
    client_at      timestamptz,
    server_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (item_id, id),
    UNIQUE (item_id, log_seq),
    CONSTRAINT actions_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT actions_rejection_shape CHECK ((accepted AND rejection IS NULL) OR (NOT accepted AND rejection IS NOT NULL))
);
CREATE UNIQUE INDEX actions_item_seq_accepted_idx ON actions (item_id, seq) WHERE accepted;

-- Снимок карточки при закрытии. Неизменяем — см. reject_immutable_change
-- ниже. Формат: contracts/evidence.schema.json.
CREATE TABLE evidence (
    item_id    uuid PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    body       jsonb NOT NULL,
    digest     bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT evidence_body_object CHECK (jsonb_typeof(body) = 'object'),
    CONSTRAINT evidence_digest_length CHECK (octet_length(digest) = 32)
);

-- +goose StatementBegin
CREATE FUNCTION reject_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'immutable artifact: %', TG_TABLE_NAME;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER evidence_immutable BEFORE UPDATE OR DELETE ON evidence FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER actions_immutable BEFORE UPDATE OR DELETE ON actions FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

-- +goose Down
DROP TRIGGER actions_immutable ON actions;
DROP TRIGGER evidence_immutable ON evidence;
DROP FUNCTION reject_immutable_change();
DROP TABLE evidence;
DROP TABLE actions;
DROP TABLE items;
DROP TABLE runs;
DROP TABLE assignments;
DROP TABLE lessons;
