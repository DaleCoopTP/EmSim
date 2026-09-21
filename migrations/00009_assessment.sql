-- assessment module tables (RFC-001 §4.2/§7.4, slice-planning.md §7,
-- ADR-006/013/016/019): trainee_assessment_state, assessment_inputs,
-- assessments, training_examples, plus the item_final_assessment view
-- guard_assessment_revision needs to find "the current final revision".
-- 1:1 with design-docs/contracts/schema.sql's assessment section,
-- restricted to what slice 6 actually needs: recommendations, advice
-- and report_files (and lesson_report_rows) are later slices (7/10) and
-- are not created here.
--
-- reject_immutable_change() already exists (migrations/00006) and is
-- reused verbatim for assessment_inputs/assessments — no new trigger
-- function needed for that half.

-- +goose Up
CREATE TABLE trainee_assessment_state (
    user_id uuid NOT NULL REFERENCES users(id),
    exercise_type text NOT NULL DEFAULT 'dds_processing' CHECK (exercise_type = 'dds_processing'),
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    advice_due_at timestamptz,
    PRIMARY KEY (user_id, exercise_type)
);
CREATE INDEX assessment_advice_due_idx ON trainee_assessment_state (advice_due_at) WHERE advice_due_at IS NOT NULL;

-- Sealed input created once, before any inference; retries read the same body.
CREATE TABLE assessment_inputs (
    id uuid PRIMARY KEY,
    item_id uuid NOT NULL UNIQUE REFERENCES evidence(item_id),
    body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'), -- contracts/assessment-inputs.schema.json
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE assessments (
    id               uuid PRIMARY KEY,
    item_id          uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    revision         integer NOT NULL CHECK (revision > 0),
    kind             text NOT NULL CHECK (kind IN ('auto', 'expert')),
    status           text NOT NULL CHECK (status IN ('ready', 'needs_review', 'unavailable')),
    evidence_digest  bytea NOT NULL,
    input_id         uuid REFERENCES assessment_inputs (id), -- expert without auto/input is allowed
    source_task_id   uuid UNIQUE,                            -- auto: exactly one task result; no FK on the prunable queue
    base_revision    integer,                                -- expert: which revision it corrected
    rubric_version   text NOT NULL,
    rubric_effective jsonb NOT NULL,                          -- merged rubric: default + reference.scoring (ADR-013)
    score            numeric(5,2) CHECK (score IS NULL OR (score >= 0 AND score <= 100)),
    passed           boolean,
    criteria         jsonb NOT NULL,                          -- [{id, status, score, weight, critical, evidence_refs[], explanation}]
    critical_errors  text[] NOT NULL DEFAULT '{}',
    feedback         jsonb NOT NULL DEFAULT '[]'::jsonb,
    model            text,                                    -- auto's LLM name (unset until slice 9)
    created_by       uuid REFERENCES users(id),               -- expert
    reason           text,                                    -- expert: why they changed it
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (item_id, revision),
    CONSTRAINT assessments_digest_length CHECK (octet_length(evidence_digest) = 32),
    CONSTRAINT assessments_criteria_array CHECK (jsonb_typeof(criteria) = 'array'),
    CONSTRAINT assessments_rubric_object CHECK (jsonb_typeof(rubric_effective) = 'object'),
    CONSTRAINT assessments_expert_shape CHECK (
        (kind = 'auto' AND revision = 1 AND input_id IS NOT NULL AND created_by IS NULL AND source_task_id IS NOT NULL AND base_revision IS NULL) OR
        (kind = 'expert' AND created_by IS NOT NULL AND reason IS NOT NULL AND length(btrim(reason)) > 0 AND status = 'ready' AND source_task_id IS NULL AND base_revision IS NOT NULL AND base_revision >= 0 AND revision = CASE WHEN base_revision = 0 THEN 2 ELSE base_revision + 1 END)
    ),
    CONSTRAINT assessments_score_shape CHECK ((status = 'ready' AND score IS NOT NULL AND passed IS NOT NULL) OR (status IN ('needs_review', 'unavailable') AND score IS NULL AND passed IS NULL))
);
CREATE UNIQUE INDEX assessments_one_auto_idx ON assessments (item_id) WHERE kind = 'auto';
CREATE INDEX assessments_item_latest_idx ON assessments (item_id, revision DESC);

-- "AI said / instructor said" pairs per criterion — fine-tuning/few-shot data.
CREATE TABLE training_examples (
    id            uuid PRIMARY KEY,
    assessment_id uuid NOT NULL REFERENCES assessments (id) ON DELETE CASCADE,
    criterion_id  text NOT NULL,
    auto_result   jsonb NOT NULL,
    expert_result jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Final assessment: the latest instructor revision, else the sole auto.
CREATE VIEW item_final_assessment AS
SELECT DISTINCT ON (item_id) item_id, id AS assessment_id, revision, kind, status, score, passed, critical_errors, rubric_version, created_at
FROM assessments
ORDER BY item_id, (kind = 'expert') DESC, revision DESC;

CREATE TRIGGER assessment_inputs_immutable BEFORE UPDATE OR DELETE ON assessment_inputs FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER assessments_immutable BEFORE UPDATE OR DELETE ON assessments FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

-- +goose StatementBegin
CREATE FUNCTION guard_assessment_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_revision integer;
BEGIN
  PERFORM 1 FROM items WHERE id = NEW.item_id FOR UPDATE;
  SELECT revision INTO current_revision FROM item_final_assessment WHERE item_id = NEW.item_id;
  IF NEW.kind = 'auto' AND EXISTS (SELECT 1 FROM assessments WHERE item_id = NEW.item_id AND kind = 'expert') THEN
    RAISE EXCEPTION 'auto assessment forbidden after expert';
  END IF;
  IF NEW.kind = 'expert' AND NEW.base_revision <> COALESCE(current_revision, 0) THEN
    RAISE EXCEPTION 'stale assessment revision';
  END IF;
  IF NEW.input_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM assessment_inputs WHERE id = NEW.input_id AND item_id = NEW.item_id) THEN
    RAISE EXCEPTION 'assessment input belongs to another item';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER assessment_revision_guard BEFORE INSERT ON assessments FOR EACH ROW EXECUTE FUNCTION guard_assessment_revision();

-- +goose Down
DROP TRIGGER assessment_revision_guard ON assessments;
DROP FUNCTION guard_assessment_revision();
DROP TRIGGER assessments_immutable ON assessments;
DROP TRIGGER assessment_inputs_immutable ON assessment_inputs;
DROP VIEW item_final_assessment;
DROP TABLE training_examples;
DROP TABLE assessments;
DROP TABLE assessment_inputs;
DROP TABLE trainee_assessment_state;
