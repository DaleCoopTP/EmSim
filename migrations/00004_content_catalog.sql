-- content module tables (RFC-001 §4.2, ADR-015; slice-planning.md §3):
-- services, classifier_types, tickets (empty in this slice — only exists
-- as scenarios.ticket_id's FK target; import.tickets is a later slice),
-- scenarios, scenario_versions. 1:1 with design-docs/contracts/schema.sql.
--
-- scenarios.source_key is the stable identity a prepared scenario file
-- (contracts/scenario-file.schema.json) carries across re-imports,
-- independent of its filename/path; NULL is reserved for future
-- editor/generated scenarios (slice 11) that have no file to key on.
--
-- scenario_versions_approval_shape (originally: any non-approved status
-- must have NULL approval fields) is corrected here before its first
-- migration ever runs: a superseded version was approved before it was
-- superseded, and RFC-001 §6 requires that provenance survive the
-- transition ("Сведения об утверждении сохраняются после superseded").
-- The three-way CHECK below is the schema.sql contract as amended, not a
-- later fix to something already shipped.
--
-- users.service_code (slice 1) has held a plain, FK-less text value since
-- services did not exist yet; this migration cannot silently invalidate
-- an existing installation's users. The FK below is added NOT VALID (new
-- writes are checked immediately; existing rows are not scanned), and
-- cmd/emsim's "import services" step VALIDATEs it once the operator has
-- confirmed every code referenced by users now exists (contracts §content
-- import; README "обновление существующей БД"). NOT VALID is not a
-- migration bug — it is the deliberate boundary between "schema updated"
-- and "existing data confirmed compatible", which an unattended
-- migration must not decide on the operator's behalf.

-- +goose Up
CREATE TABLE services (
    code       text PRIMARY KEY,                       -- dds_district, uk, mosvodokanal, moslift, 103 ...
    name       text NOT NULL,
    workflow   jsonb NOT NULL,                         -- допустимые переходы, обязательность комментария, исключения
    active     boolean NOT NULL DEFAULT true,
    CONSTRAINT services_workflow_object CHECK (jsonb_typeof(workflow) = 'object')
);

-- NOT VALID: see header. VALIDATE CONSTRAINT is a separate, explicit step
-- (cmd/emsim "import services", README) run once services is seeded and
-- every existing users.service_code is confirmed present.
ALTER TABLE users ADD CONSTRAINT users_service_code_fkey
    FOREIGN KEY (service_code) REFERENCES services(code) NOT VALID;

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
    target_service text NOT NULL REFERENCES services(code),
    difficulty     smallint NOT NULL CHECK (difficulty BETWEEN 1 AND 10),
    origin         text NOT NULL CHECK (origin IN ('manual', 'ticket', 'generated')),
    ticket_id      uuid REFERENCES tickets(id),
    status         text NOT NULL CHECK (status IN ('draft', 'approved', 'archived')),
    source_key     text UNIQUE,                         -- ключ файла подготовленного сценария (slice 2); NULL для будущего авторства без файлов (срез 11)
    created_by     uuid NOT NULL REFERENCES users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scenarios_service_status_idx ON scenarios (target_service, status, difficulty);

-- Неизменяемая версия: body по contracts/scenario.schema.json.
CREATE TABLE scenario_versions (
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type = 'dds_processing'), -- ADR-015: первый этап; 112 добавится отдельной миграцией
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

-- У версии меняется только жизненный цикл утверждения, не содержание/сложность.
-- +goose StatementBegin
CREATE FUNCTION protect_scenario_version_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','approved_by','approved_at']) IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','approved_by','approved_at']) THEN
    RAISE EXCEPTION 'immutable scenario version content';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER scenario_version_content_immutable BEFORE UPDATE ON scenario_versions
  FOR EACH ROW EXECUTE FUNCTION protect_scenario_version_content();

-- approved/superseded версия могла быть назначена и пройдена (RFC-001 §6:
-- "одна версия сценария может безопасно использоваться во многих
-- назначениях") — удаление стёрло бы неизменяемую ссылку прогона. Черновик
-- (status='draft', ещё не approved) можно отбросить — он для этого и есть.
-- Триггер срабатывает и на ON DELETE CASCADE со стороны scenarios: Postgres
-- реализует каскад как обычный DELETE над scenario_versions, для которого
-- этот BEFORE DELETE FOR EACH ROW триггер выполняется как обычно.
-- +goose StatementBegin
CREATE FUNCTION reject_scenario_version_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'draft' THEN
    RAISE EXCEPTION 'immutable scenario version: cannot delete a % version', OLD.status;
  END IF;
  RETURN OLD;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER scenario_version_delete_guard BEFORE DELETE ON scenario_versions
  FOR EACH ROW EXECUTE FUNCTION reject_scenario_version_delete();

-- +goose Down
DROP TRIGGER scenario_version_delete_guard ON scenario_versions;
DROP FUNCTION reject_scenario_version_delete();
DROP TRIGGER scenario_version_content_immutable ON scenario_versions;
DROP FUNCTION protect_scenario_version_content();
DROP TABLE scenario_versions;
DROP TABLE scenarios;
DROP TABLE tickets;
DROP TABLE classifier_types;
ALTER TABLE users DROP CONSTRAINT users_service_code_fkey;
DROP TABLE services;
