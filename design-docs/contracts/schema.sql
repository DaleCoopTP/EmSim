-- EmSim — схема данных v1 (PostgreSQL 16). Контракт к RFC-001 §6.
-- Группы по модулям: auth · content · training · assessment · platform.
-- Соглашения: id uuid (v7), время timestamptz, деньги/баллы numeric, JSON — jsonb с CHECK на тип.

BEGIN;

-- ============================================================ auth

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    login         text NOT NULL UNIQUE,
    password_hash text NOT NULL,                       -- argon2id
    full_name     text NOT NULL,
    role          text NOT NULL CHECK (role IN ('admin', 'instructor', 'trainee')),
    service_code  text,                                -- NULL для admin/instructor и обучаемого 112 без профиля ДДС
    level         text NOT NULL DEFAULT 'easy' CHECK (level IN ('easy', 'medium', 'hard')),
    active        boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_login_shape CHECK (login ~ '^[a-z0-9._-]{3,64}$')
);

CREATE TABLE workstations (
    id         uuid PRIMARY KEY,
    number     integer NOT NULL UNIQUE CHECK (number > 0),
    label      text NOT NULL DEFAULT '',
    ip_address inet,                                   -- опциональная привязка РМ к адресу
    active     boolean NOT NULL DEFAULT true
);

CREATE TABLE sessions (
    id             bytea PRIMARY KEY,                  -- 32 случайных байта
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workstation_id uuid REFERENCES workstations(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    CONSTRAINT sessions_id_length CHECK (octet_length(id) = 32)
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);

-- ============================================================ content

CREATE TABLE services (
    code       text PRIMARY KEY,                       -- dds_district, uk, mosvodokanal, moslift, 103 ...
    name       text NOT NULL,
    workflow   jsonb NOT NULL,                         -- допустимые переходы, обязательность комментария, исключения
    active     boolean NOT NULL DEFAULT true,
    CONSTRAINT services_workflow_object CHECK (jsonb_typeof(workflow) = 'object')
);

-- auth.users.service_code существовал до этого модуля (slice 1) без FK — служба
-- как содержимое появляется только здесь. Оператор обновления должен провести
-- существующие профили через NOT VALID → устранение расхождений → VALIDATE
-- CONSTRAINT (мастер-последовательность — в migrations/00004, не здесь).
ALTER TABLE users ADD CONSTRAINT users_service_code_fkey FOREIGN KEY (service_code) REFERENCES services(code);

CREATE TABLE classifier_types (
    id            uuid PRIMARY KEY,
    code          text NOT NULL UNIQUE,                -- из XLSX
    name          text NOT NULL,
    features      jsonb NOT NULL,                      -- формализованные признаки
    notify        text[] NOT NULL,                     -- коды служб для оповещения
    source_row    integer,
    imported_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT classifier_features_object CHECK (jsonb_typeof(features) = 'object')
);
CREATE INDEX classifier_notify_idx ON classifier_types USING gin (notify);

CREATE TABLE tickets (
    id          uuid PRIMARY KEY,
    number      text NOT NULL UNIQUE,
    body        text NOT NULL,
    answer      text,                                  -- эталонный ответ, если был в билете
    imported_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scenarios (
    id             uuid PRIMARY KEY,
    title          text NOT NULL,
    target_service text REFERENCES services(code), -- только ДДС; для 112 NULL
    difficulty     smallint NOT NULL CHECK (difficulty BETWEEN 1 AND 10),
    origin         text NOT NULL CHECK (origin IN ('manual', 'ticket', 'generated')),
    ticket_id      uuid REFERENCES tickets(id),
    status         text NOT NULL CHECK (status IN ('draft', 'approved', 'archived')),
    source_key     text UNIQUE,                         -- ключ файла подготовленного сценария (эталонный импорт, slice 2); NULL для будущего авторства без файлов (редактор/генерация, срез 11)
    created_by     uuid NOT NULL REFERENCES users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scenarios_service_status_idx ON scenarios (target_service, status, difficulty);

-- Неизменяемая версия: body по contracts/scenario.schema.json.
CREATE TABLE scenario_versions (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type IN ('dds_processing', 'operator112_intake')),
    id            uuid PRIMARY KEY,
    scenario_id   uuid NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
    version       integer NOT NULL CHECK (version > 0),
    status        text NOT NULL CHECK (status IN ('draft', 'approved', 'superseded')),
    body          jsonb NOT NULL,
    digest        bytea NOT NULL,                      -- sha256(canonical body)
    difficulty    smallint NOT NULL CHECK (difficulty BETWEEN 1 AND 10), -- сложность этой версии
    source_task_id uuid UNIQUE,                         -- provenance; без FK: tasks очищаются
    prompt_ref    text,                                -- версия промпта генерации, если generated
    created_by    uuid NOT NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    approved_by   uuid REFERENCES users(id),
    approved_at   timestamptz,
    UNIQUE (scenario_id, version),
    CONSTRAINT scenario_versions_body_object CHECK (jsonb_typeof(body) = 'object'),
    CONSTRAINT scenario_versions_digest_length CHECK (octet_length(digest) = 32),
    -- draft — ещё не утверждена, оба поля пусты; approved — обе заполнены;
    -- superseded сохраняет сведения об утверждении версии, которая была
    -- approved (обе заполнены), но версия могла устареть и не будучи
    -- approved (например, при будущей отмене черновика) — тогда обе пусты.
    CONSTRAINT scenario_versions_approval_shape CHECK (
        (status = 'draft' AND approved_by IS NULL AND approved_at IS NULL) OR
        (status = 'approved' AND approved_by IS NOT NULL AND approved_at IS NOT NULL) OR
        (status = 'superseded' AND (approved_by IS NULL) = (approved_at IS NULL))
    )
);
-- Не более одной approved-версии одновременно: approve/regenerate-переходы
-- (срез 11) переводят предыдущую в superseded в той же транзакции.
CREATE UNIQUE INDEX scenario_versions_one_approved_idx ON scenario_versions (scenario_id) WHERE status = 'approved';

-- ============================================================ platform (объявляем раньше — на blobs ссылаются)

CREATE TABLE blobs (
    id         uuid PRIMARY KEY,
    sha256     bytea NOT NULL UNIQUE,
    mime       text NOT NULL,
    size       bigint NOT NULL CHECK (size >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT blobs_sha_length CHECK (octet_length(sha256) = 32)
);

CREATE TABLE voice_assets (
    id                  uuid PRIMARY KEY,
    scenario_version_id uuid NOT NULL REFERENCES scenario_versions(id) ON DELETE CASCADE,
    key                 text NOT NULL,                 -- contact:crew_leader:greeting | event:e1
    voice               text NOT NULL,                 -- ru_RU-irina-medium ...
    blob_id             uuid NOT NULL REFERENCES blobs(id),
    UNIQUE (scenario_version_id, key)
);

-- Очередь задач: по образцу orchestration-core (ADR-010), без FK на домен.
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
CREATE INDEX audit_log_at_brin_idx ON audit_log USING brin (at);  -- audit.prune (ADR-033)

-- ADR-033: last state a worker observed for a component the api cannot
-- reach itself (model health, backup directory free space).
CREATE TABLE platform_heartbeats (
    component  text PRIMARY KEY CHECK (component ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    status     text NOT NULL CHECK (status IN ('ok', 'unavailable')),
    detail     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object'),
    checked_at timestamptz NOT NULL
);

-- ============================================================ training

CREATE TABLE lessons (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type IN ('dds_processing', 'operator112_intake')),
    id            uuid PRIMARY KEY,
    instructor_id uuid NOT NULL REFERENCES users(id),
    title         text NOT NULL,
    mode          text NOT NULL CHECK (mode IN ('intro', 'training')),
    level         text NOT NULL CHECK (level IN ('easy', 'medium', 'hard')),
    state         text NOT NULL CHECK (state IN ('draft', 'running', 'stopped', 'finished')),
    epoch         bigint NOT NULL DEFAULT 0,           -- растёт при stop; барьер для поздних команд
    timing        jsonb NOT NULL,                      -- {open_s:30, primary_s:30, complete_s:180, spawn_every_s:150?}
    rubric_version text NOT NULL,                      -- фиксируется на старте
    scoring       jsonb,                               -- ДДС-6/ADR-035: {weights:{id:w}, pass_threshold}; NULL — значения рубрики
    recording_grace_s integer NOT NULL DEFAULT 120 CHECK (recording_grace_s >= 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    stopped_at    timestamptz,
    stop_reason   text CHECK (stop_reason IS NULL OR length(stop_reason) <= 500),
    finished_at   timestamptz,
    CONSTRAINT lessons_timing_object CHECK (jsonb_typeof(timing) = 'object'),
    CONSTRAINT lessons_scoring_object CHECK (scoring IS NULL OR jsonb_typeof(scoring) = 'object'),
    CONSTRAINT lessons_state_shape CHECK (
        (state = 'draft'    AND started_at IS NULL) OR
        (state = 'running'  AND started_at IS NOT NULL AND stopped_at IS NULL) OR
        (state = 'stopped'  AND started_at IS NOT NULL AND stopped_at IS NOT NULL AND finished_at IS NULL) OR
        (state = 'finished' AND started_at IS NOT NULL AND finished_at IS NOT NULL)
    )
);
CREATE INDEX lessons_instructor_idx ON lessons (instructor_id, created_at DESC);

-- Назначение на рабочее место: кто и какие версии сценариев в каком порядке.
CREATE TABLE assignments (
    lesson_id           uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    workstation_id      uuid NOT NULL REFERENCES workstations(id),
    user_id             uuid NOT NULL REFERENCES users(id), -- срез 3/ADR-017: обязателен; "кто залогинится на РМ" (NULL) в MVP не поддерживается
    scenario_version_ids uuid[] NOT NULL CHECK (cardinality(scenario_version_ids) > 0),
    PRIMARY KEY (lesson_id, workstation_id),
    UNIQUE (lesson_id, user_id)
);

-- Прогон одного обучаемого в занятии.
CREATE TABLE runs (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type IN ('dds_processing', 'operator112_intake')),
    id             uuid PRIMARY KEY,
    lesson_id      uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL REFERENCES users(id),
    workstation_id uuid NOT NULL REFERENCES workstations(id),
    mode           text NOT NULL CHECK (mode IN ('intro', 'training')), -- снимок lessons.mode на старте (срез 3)
    state          text NOT NULL CHECK (state IN ('active', 'finished')),
    level_at_start text NOT NULL CHECK (level_at_start IN ('easy','medium','hard')), -- уровень занятия при старте
    next_offer_at timestamptz, -- hard: следующая выдача, обновляется от фактической выдачи
    queue_cursor   integer NOT NULL DEFAULT 0,         -- сколько назначенных сценариев уже предложено
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    UNIQUE (lesson_id, user_id),
    UNIQUE (lesson_id, workstation_id)
);
-- Один активный прогон на пользователя/РМ одновременно, независимо от занятия — иначе
-- GET /my/run (срез 3) не может однозначно выбрать текущий прогон обучаемого.
CREATE UNIQUE INDEX runs_active_user_idx ON runs (user_id) WHERE state = 'active';
CREATE UNIQUE INDEX runs_active_workstation_idx ON runs (workstation_id) WHERE state = 'active';

-- Карточка у обучаемого. Единственная изменяемая строка карточки.
CREATE TABLE items (
    id                  uuid PRIMARY KEY,
    exercise_type       text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type IN ('dds_processing', 'operator112_intake')),
    run_id              uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    scenario_version_id uuid NOT NULL REFERENCES scenario_versions(id),
    ordinal             integer NOT NULL,              -- порядок в run
    spawned_from        uuid REFERENCES items(id),     -- если создана событием spawn_card
    state               text NOT NULL CHECK (state IN ('offered', 'opened', 'in_progress', 'closed', 'interrupted')),
    reaction            text NOT NULL DEFAULT 'added', -- статус реагирования службы обучаемого
    card                jsonb NOT NULL CHECK (jsonb_typeof(card) = 'object'), -- экземпляр публичной карточки (срез 3); меняется только set_card_field
    intake_state        jsonb CHECK (intake_state IS NULL OR jsonb_typeof(intake_state) = 'object'), -- телефон/реплики 112
    workflow            jsonb NOT NULL CHECK (jsonb_typeof(workflow) = 'object'), -- снимок services.workflow целевой службы на момент выдачи
    pilot_goal          text, -- снимок reference.pilot_goal (ADR-017); NULL/'' = обычные правила завершения
    seq                 bigint NOT NULL DEFAULT 0,     -- номер последнего принятого действия
    stop_cutoff_log_seq bigint CHECK (stop_cutoff_log_seq IS NULL OR stop_cutoff_log_seq >= 0),
    interruptions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(interruptions)='array'),
    log_seq             bigint NOT NULL DEFAULT 0 CHECK (log_seq >= 0), -- все попытки, не только applied
    timing_effective    jsonb NOT NULL CHECK (jsonb_typeof(timing_effective) = 'object'),
    deadlines           jsonb NOT NULL DEFAULT '{}'::jsonb, -- {open_at, primary_at, complete_at} абсолютное время
    offered_at          timestamptz NOT NULL DEFAULT now(),
    opened_at           timestamptz,
    primary_at          timestamptz,                  -- первое применённое первичное решение
    closed_at           timestamptz,
    close_reason        text CHECK (close_reason IN ('completed', 'refused', 'interrupted', 'pilot_completed')), -- pilot_completed: ADR-017, close из accepted
    UNIQUE (run_id, ordinal),
    CONSTRAINT items_reaction_shape CHECK (reaction IN ('added','received','accepted','not_accepted','responding','arrived','working','completed','refused','completed_without_team')),
    CONSTRAINT items_deadlines_object CHECK (jsonb_typeof(deadlines) = 'object'),
    CONSTRAINT items_intake_state_shape CHECK ((exercise_type='dds_processing' AND intake_state IS NULL) OR (exercise_type='operator112_intake' AND intake_state IS NOT NULL)),
    CONSTRAINT items_state_shape CHECK (
        (state = 'offered'     AND opened_at IS NULL AND closed_at IS NULL) OR
        (state IN ('opened','in_progress') AND opened_at IS NOT NULL AND closed_at IS NULL) OR
        (state IN ('closed','interrupted') AND closed_at IS NOT NULL AND close_reason IS NOT NULL)
    )
);
CREATE INDEX items_run_state_idx ON items (run_id, state);
CREATE INDEX items_open_idx ON items (state) WHERE state IN ('offered', 'opened', 'in_progress');

-- Append-only журнал команд. Отклонённые тоже пишутся.
CREATE TABLE actions (
    item_id    uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    seq        bigint NOT NULL,                        -- версия состояния после решения; rejected её не меняет
    log_seq    bigint NOT NULL CHECK (log_seq > 0),      -- общий порядок всех попыток внутри item
    actor_id   uuid NOT NULL REFERENCES users(id),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    id         uuid NOT NULL UNIQUE,                   -- action_id для ссылок; порядок по log_seq
    command_id uuid NOT NULL UNIQUE,                   -- клиентский, идемпотентность
    type       text NOT NULL CHECK (type IN ('open','set_status','add_comment','set_card_field','call_start','call_end','control_report','close','answer_incoming','end_incoming','save_intake_draft','dispatch_intake','complete_intake')),
    payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    effect     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(effect) = 'object'), -- срез 3/ADR-017: серверный факт (например set_card_field {path,old,new}), не участвует в request_digest
    accepted   boolean NOT NULL,
    rejection  text,                                   -- transition_not_allowed | stale_seq | lesson_stopped | comment_required ...
    receipt    jsonb NOT NULL,                         -- квитанция, которую вернули клиенту (для replay)
    http_status integer NOT NULL CHECK (http_status IN (200,409,422)), -- исходный статус для replay
    client_at  timestamptz,
    server_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (item_id, id),
    UNIQUE (item_id, log_seq),
    CONSTRAINT actions_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT actions_rejection_shape CHECK ((accepted AND rejection IS NULL) OR (NOT accepted AND rejection IS NOT NULL))
);
CREATE UNIQUE INDEX actions_item_seq_accepted_idx ON actions (item_id, seq) WHERE accepted;

CREATE TABLE intake_dispatches (
    item_id       uuid PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    action_id     uuid NOT NULL UNIQUE REFERENCES actions(id),
    service_code  text NOT NULL REFERENCES services(code),
    card_snapshot jsonb NOT NULL CHECK (jsonb_typeof(card_snapshot) = 'object'),
    sent_at       timestamptz NOT NULL
);
CREATE TRIGGER intake_dispatches_immutable BEFORE UPDATE OR DELETE ON intake_dispatches FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

-- Запланированные события сценария для конкретной карточки.
CREATE TABLE item_events (
    id           uuid PRIMARY KEY,
    item_id      uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    event_key    text NOT NULL,                        -- ключ из scenario_versions.body.events[]
    anchor_at    timestamptz NOT NULL,                 -- первое достижение since
    due_at       timestamptz NOT NULL,
    state        text NOT NULL CHECK (state IN ('scheduled', 'delivered', 'skipped')),
    delivered_at timestamptz,
    late         boolean NOT NULL DEFAULT false,       -- доставлено позже due_at более чем на 5 с (простой сервера)
    skip_reason  text,
    UNIQUE (item_id, event_key),
    CONSTRAINT item_events_shape CHECK (
      (state='scheduled' AND delivered_at IS NULL AND skip_reason IS NULL) OR
      (state='delivered' AND delivered_at IS NOT NULL AND skip_reason IS NULL) OR
      (state='skipped' AND delivered_at IS NULL AND skip_reason IS NOT NULL)
    )
);
CREATE INDEX item_events_due_idx ON item_events (due_at) WHERE state = 'scheduled';

CREATE TABLE calls (
    id          uuid PRIMARY KEY,
    item_id     uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    contact_key text NOT NULL,                         -- crew_leader | shift_chief | line_112 | uk_dispatch ...
    direction   text NOT NULL DEFAULT 'outgoing' CHECK (direction IN ('outgoing','incoming')), -- ADR-031
    event_key   text,                                  -- ADR-031: phone_incoming, на который ответил входящий звонок
    started_at  timestamptz NOT NULL,
    ended_at    timestamptz,
    reaction_at_call text NOT NULL,
    blob_id     uuid REFERENCES blobs(id),
    accepted_by text,                                  -- «Кто принял» (отработка, как в 112)
    summary     text,                                  -- «Суть сообщения» (отработка)
    recording_sha256 bytea CHECK (recording_sha256 IS NULL OR octet_length(recording_sha256)=32),
    recording_size bigint CHECK (recording_size IS NULL OR recording_size BETWEEN 1 AND 10485760),
    recording_mime text CHECK (recording_mime IN ('audio/webm','audio/ogg','audio/wav')),
    recording_state text NOT NULL DEFAULT 'absent' CHECK (recording_state IN ('absent','awaiting','ready','expired')),
    recording_upload_deadline_at timestamptz,
    recording_received_at timestamptz,
    CONSTRAINT calls_recording_manifest CHECK (
      (recording_state='absent' AND recording_sha256 IS NULL AND recording_size IS NULL AND recording_mime IS NULL AND blob_id IS NULL AND recording_received_at IS NULL) OR
      (recording_state IN ('awaiting','ready','expired') AND ended_at IS NOT NULL AND recording_sha256 IS NOT NULL AND recording_size IS NOT NULL AND recording_mime IS NOT NULL AND
        ((recording_state='ready' AND blob_id IS NOT NULL AND recording_received_at IS NOT NULL) OR
         (recording_state IN ('awaiting','expired') AND blob_id IS NULL AND recording_received_at IS NULL)))
    )
);
CREATE INDEX calls_item_idx ON calls (item_id);
CREATE UNIQUE INDEX calls_one_active_per_item_idx ON calls (item_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX calls_event_key_idx ON calls (item_id, event_key) WHERE event_key IS NOT NULL;

-- Снимок карточки при закрытии. Основной неизменяемый вход; правила и STT фиксируются в одном assessment_inputs (ADR-006/016). Формат: contracts/evidence.schema.json.
CREATE TABLE evidence (
    item_id    uuid PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    body       jsonb NOT NULL,
    digest     bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT evidence_body_object CHECK (jsonb_typeof(body) = 'object'),
    CONSTRAINT evidence_digest_length CHECK (octet_length(digest) = 32)
);

-- Сообщение после закрытия не меняет evidence/closed_at и не запускает пересчёт.
CREATE TABLE control_reports (
    id uuid PRIMARY KEY,
    item_id uuid NOT NULL REFERENCES items(id),
    action_id uuid NOT NULL UNIQUE REFERENCES actions(id),
    text text NOT NULL CHECK (length(btrim(text)) > 0),
    created_at timestamptz NOT NULL
);

-- ============================================================ assessment

-- Assessment владеет версиями основания; auth не хранит эти данные.
CREATE TABLE trainee_assessment_state (
    user_id uuid NOT NULL REFERENCES users(id),
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type IN ('dds_processing', 'operator112_intake')),
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    advice_due_at timestamptz,
    PRIMARY KEY(user_id, exercise_type)
);
CREATE INDEX assessment_advice_due_idx ON trainee_assessment_state(advice_due_at) WHERE advice_due_at IS NOT NULL;

-- Sealed input создаётся один раз до inference; дальнейшие retry читают тот же body.
CREATE TABLE assessment_inputs (
    id uuid PRIMARY KEY,
    item_id uuid NOT NULL UNIQUE REFERENCES evidence(item_id),
    body jsonb NOT NULL CHECK (jsonb_typeof(body)='object'), -- contracts/assessment-inputs.schema.json
    digest bytea NOT NULL CHECK (octet_length(digest)=32),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE assessments (
    id                uuid PRIMARY KEY,
    item_id           uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    revision          integer NOT NULL CHECK (revision > 0),
    kind              text NOT NULL CHECK (kind IN ('auto', 'expert')),
    status            text NOT NULL CHECK (status IN ('ready', 'needs_review', 'unavailable')),
    evidence_digest   bytea NOT NULL,
    input_id          uuid REFERENCES assessment_inputs(id), -- expert без auto/input разрешён
    source_task_id    uuid UNIQUE,                     -- auto: ровно один результат задачи; без FK на очищаемую очередь
    base_revision    integer,                         -- expert: ревизия, которую правили
    rubric_version    text NOT NULL,
    rubric_effective  jsonb NOT NULL,                  -- слитая рубрика: дефолт + reference.scoring (ADR-013)
    score             numeric(5,2) CHECK (score IS NULL OR (score >= 0 AND score <= 100)),
    passed            boolean,
    criteria          jsonb NOT NULL,                  -- [{id, status, score, weight, evidence_refs[], explanation}]
    critical_errors   text[] NOT NULL DEFAULT '{}',
    feedback          jsonb NOT NULL DEFAULT '[]'::jsonb,
    model             text,                            -- имя LLM для auto
    created_by        uuid REFERENCES users(id),       -- expert
    reason            text,                            -- expert: почему изменил
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (item_id, revision),
    CONSTRAINT assessments_digest_length CHECK (octet_length(evidence_digest) = 32),
    CONSTRAINT assessments_criteria_array CHECK (jsonb_typeof(criteria) = 'array'),
    CONSTRAINT assessments_rubric_object CHECK (jsonb_typeof(rubric_effective) = 'object'),
    CONSTRAINT assessments_expert_shape CHECK (
        (kind = 'auto' AND revision=1 AND input_id IS NOT NULL AND created_by IS NULL AND source_task_id IS NOT NULL AND base_revision IS NULL) OR
        (kind = 'expert' AND created_by IS NOT NULL AND reason IS NOT NULL AND length(btrim(reason)) > 0 AND status = 'ready' AND source_task_id IS NULL AND base_revision IS NOT NULL AND base_revision >= 0 AND revision=CASE WHEN base_revision=0 THEN 2 ELSE base_revision+1 END)
    ),
    CONSTRAINT assessments_score_shape CHECK ((status='ready' AND score IS NOT NULL AND passed IS NOT NULL) OR (status IN ('needs_review','unavailable') AND score IS NULL AND passed IS NULL))
);
CREATE UNIQUE INDEX assessments_one_auto_idx ON assessments(item_id) WHERE kind='auto';
CREATE INDEX assessments_item_latest_idx ON assessments (item_id, revision DESC);

-- Пары «ИИ сказал / преподаватель сказал» по критерию — данные для дообучения и few-shot.
CREATE TABLE training_examples (
    id             uuid PRIMARY KEY,
    assessment_id  uuid NOT NULL REFERENCES assessments(id) ON DELETE CASCADE,
    criterion_id   text NOT NULL,
    auto_result    jsonb NOT NULL,
    expert_result  jsonb NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE recommendations (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type='dds_processing'),
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    current_level     text NOT NULL,
    recommended_level text NOT NULL,
    basis             jsonb NOT NULL,                  -- {items:[...], avg:..., critical:..., policy:...}
    policy_version    text NOT NULL,
    basis_version     bigint NOT NULL CHECK (basis_version >= 0),
    computed_at       timestamptz NOT NULL DEFAULT now(),
    applied_by        uuid REFERENCES users(id),
    applied_at        timestamptz,
    UNIQUE(user_id, exercise_type, basis_version, policy_version),
    CONSTRAINT recommendations_basis_object CHECK (jsonb_typeof(basis) = 'object')
);
CREATE INDEX recommendations_user_idx ON recommendations (user_id, computed_at DESC);

-- LLM-совет «над чем работать» (RFC §7.4). Хранится текстом; вход задачи — только агрегаты.
CREATE TABLE advice (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type='dds_processing'),
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lesson_id  uuid REFERENCES lessons(id) ON DELETE CASCADE,   -- NULL = по всей истории
    text       text NOT NULL,
    basis      jsonb NOT NULL,                         -- агрегаты, из которых сформирован
    model      text NOT NULL,
    prompt_version text NOT NULL,
    basis_version bigint NOT NULL CHECK (basis_version >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT advice_basis_object CHECK (jsonb_typeof(basis) = 'object')
);
CREATE INDEX advice_user_idx ON advice (user_id, created_at DESC);

-- Сгенерированные файлы отчётов (PDF/CSV) — результат report.build.
CREATE TABLE report_files (
    id           uuid PRIMARY KEY,
    kind         text NOT NULL CHECK (kind IN ('lesson_pdf', 'lesson_csv', 'user_pdf', 'group_pdf')),
    lesson_id    uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    task_id      uuid NOT NULL UNIQUE,
    requested_by uuid NOT NULL REFERENCES users(id),
    basis jsonb NOT NULL CHECK (jsonb_typeof(basis)='object'), -- полный snapshot + assessment_id/revision
    basis_digest bytea NOT NULL CHECK (octet_length(basis_digest)=32),
    blob_id      uuid REFERENCES blobs(id),
    requested_at timestamptz NOT NULL DEFAULT now(),
    generated_at timestamptz,
    CONSTRAINT report_files_ready_shape CHECK ((blob_id IS NULL AND generated_at IS NULL) OR (blob_id IS NOT NULL AND generated_at IS NOT NULL))
);
CREATE INDEX report_files_lesson_idx ON report_files (lesson_id, requested_at DESC);

-- ============================================================ reporting (представления)

-- Итоговая: последняя правка преподавателя; если её нет — единственная auto.
CREATE VIEW item_final_assessment AS
SELECT DISTINCT ON (item_id) item_id, id AS assessment_id, revision, kind, status, score, passed, critical_errors, rubric_version, created_at
FROM assessments
ORDER BY item_id, (kind='expert') DESC, revision DESC;

-- Строка отчёта по занятию: обучаемый × карточка.
CREATE VIEW lesson_report_rows AS
SELECT l.id AS lesson_id, l.title AS lesson_title, l.mode AS lesson_mode, l.state AS lesson_state,
       r.user_id, u.full_name, w.number AS workstation_no, r.level_at_start AS level, r.exercise_type,
       i.id AS item_id, i.ordinal, i.state AS item_state, i.reaction, i.close_reason, i.offered_at, i.opened_at, i.closed_at, i.interruptions,
       i.card->>'number' AS card_number, sv.scenario_id, s.title AS scenario_title, sv.version AS scenario_version, sv.difficulty,
       (ev.body->'derived'->>'open_seconds')::numeric AS open_seconds,
       (ev.body->'derived'->>'primary_seconds')::numeric AS primary_seconds,
       (ev.body->'derived'->>'work_seconds')::numeric AS work_seconds,
       (ev.body->'derived'->>'total_seconds')::numeric AS total_seconds,
       fa.assessment_id, fa.revision AS assessment_revision, fa.kind AS assessment_kind, fa.status AS assessment_status,
       fa.score, fa.passed, fa.critical_errors, a.criteria, a.feedback
FROM lessons l
JOIN runs r ON r.lesson_id = l.id
JOIN users u ON u.id = r.user_id
JOIN workstations w ON w.id = r.workstation_id
JOIN items i ON i.run_id = r.id
JOIN scenario_versions sv ON sv.id = i.scenario_version_id
JOIN scenarios s ON s.id = sv.scenario_id
LEFT JOIN evidence ev ON ev.item_id = i.id
LEFT JOIN item_final_assessment fa ON fa.item_id = i.id
LEFT JOIN assessments a ON a.id = fa.assessment_id;

-- Неизменяемость запечатанных артефактов защищена и от ошибочного UPDATE/DELETE.
CREATE FUNCTION reject_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'immutable artifact: %', TG_TABLE_NAME;
END;
$$;
CREATE TRIGGER evidence_immutable BEFORE UPDATE OR DELETE ON evidence FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER actions_immutable BEFORE UPDATE OR DELETE ON actions FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER assessment_inputs_immutable BEFORE UPDATE OR DELETE ON assessment_inputs FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER assessments_immutable BEFORE UPDATE OR DELETE ON assessments FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE FUNCTION guard_assessment_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_revision integer;
BEGIN
  PERFORM 1 FROM items WHERE id=NEW.item_id FOR UPDATE;
  SELECT revision INTO current_revision FROM item_final_assessment WHERE item_id=NEW.item_id;
  IF NEW.kind='auto' AND EXISTS(SELECT 1 FROM assessments WHERE item_id=NEW.item_id AND kind='expert') THEN
    RAISE EXCEPTION 'auto assessment forbidden after expert';
  END IF;
  IF NEW.kind='expert' AND NEW.base_revision <> COALESCE(current_revision,0) THEN
    RAISE EXCEPTION 'stale assessment revision';
  END IF;
  IF NEW.input_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM assessment_inputs WHERE id=NEW.input_id AND item_id=NEW.item_id) THEN
    RAISE EXCEPTION 'assessment input belongs to another item';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER assessment_revision_guard BEFORE INSERT ON assessments FOR EACH ROW EXECUTE FUNCTION guard_assessment_revision();

-- У версии меняется только жизненный цикл утверждения, не содержание/сложность.
-- Допустимые переходы однонаправлены: draft -> approved|superseded и
-- approved -> superseded. Атрибуция может появиться только при утверждении
-- черновика и после этого неизменяема.
CREATE FUNCTION protect_scenario_version_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','approved_by','approved_at']) IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','approved_by','approved_at']) THEN
    RAISE EXCEPTION 'immutable scenario version content';
  END IF;
  IF OLD.status <> NEW.status AND NOT (
       (OLD.status = 'draft' AND NEW.status IN ('approved', 'superseded')) OR
       (OLD.status = 'approved' AND NEW.status = 'superseded')
     ) THEN
    RAISE EXCEPTION 'invalid scenario version status transition: % -> %', OLD.status, NEW.status;
  END IF;
  IF NOT (OLD.status = 'draft' AND NEW.status = 'approved') AND
     (NEW.approved_by IS DISTINCT FROM OLD.approved_by OR
      NEW.approved_at IS DISTINCT FROM OLD.approved_at) THEN
    RAISE EXCEPTION 'immutable scenario version approval attribution';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER scenario_version_content_immutable BEFORE UPDATE ON scenario_versions
  FOR EACH ROW EXECUTE FUNCTION protect_scenario_version_content();

-- approved/superseded версия могла быть назначена и пройдена (RFC-001 §6:
-- "одна версия сценария может безопасно использоваться во многих
-- назначениях") — удаление стёрло бы неизменяемую ссылку прогона. Черновик
-- (status='draft', ещё не approved) можно отбросить — он для этого и есть.
CREATE FUNCTION reject_scenario_version_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'draft' THEN
    RAISE EXCEPTION 'immutable scenario version: cannot delete a % version', OLD.status;
  END IF;
  RETURN OLD;
END;
$$;
CREATE TRIGGER scenario_version_delete_guard BEFORE DELETE ON scenario_versions
  FOR EACH ROW EXECUTE FUNCTION reject_scenario_version_delete();
-- scenarios(ON DELETE CASCADE на scenario_id) не обходит эту защиту: любая
-- approved/superseded дочерняя версия сначала отклонит DELETE-каскад тем же
-- триггером, до того как Postgres удалит родительскую строку scenarios.

COMMIT;
