-- Slice 7: reporting projections and immutable PDF artifact requests.
-- +goose Up
CREATE TABLE report_files (
    id            uuid PRIMARY KEY,
    kind          text NOT NULL CHECK (kind IN ('lesson_pdf', 'lesson_csv', 'user_pdf', 'group_pdf')),
    lesson_id     uuid NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    task_id       uuid NOT NULL UNIQUE, -- tasks are intentionally prunable
    requested_by  uuid NOT NULL REFERENCES users(id),
    basis         jsonb NOT NULL CHECK (jsonb_typeof(basis) = 'object'),
    basis_digest  bytea NOT NULL CHECK (octet_length(basis_digest) = 32),
    blob_id       uuid REFERENCES blobs(id),
    requested_at  timestamptz NOT NULL DEFAULT now(),
    generated_at  timestamptz,
    CONSTRAINT report_files_ready_shape CHECK (
        (blob_id IS NULL AND generated_at IS NULL) OR
        (blob_id IS NOT NULL AND generated_at IS NOT NULL)
    )
);
CREATE INDEX report_files_lesson_idx ON report_files (lesson_id, requested_at DESC);

-- Basis is immutable immediately. A pending request may make exactly one
-- transition to a ready artifact; a ready artifact can never be replaced.
-- +goose StatementBegin
CREATE FUNCTION guard_report_file_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id <> OLD.id OR NEW.kind <> OLD.kind OR NEW.lesson_id <> OLD.lesson_id OR
     NEW.task_id <> OLD.task_id OR NEW.requested_by <> OLD.requested_by OR
     NEW.basis IS DISTINCT FROM OLD.basis OR NEW.basis_digest <> OLD.basis_digest OR
     NEW.requested_at <> OLD.requested_at THEN
    RAISE EXCEPTION 'immutable report file basis';
  END IF;
  IF OLD.blob_id IS NOT NULL OR NEW.blob_id IS NULL OR NEW.generated_at IS NULL THEN
    RAISE EXCEPTION 'immutable report file';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER report_files_immutable BEFORE UPDATE ON report_files
  FOR EACH ROW EXECUTE FUNCTION guard_report_file_update();

CREATE VIEW lesson_report_rows AS
SELECT
  l.id AS lesson_id, l.title AS lesson_title, l.mode AS lesson_mode, l.state AS lesson_state,
  r.user_id, u.full_name, w.number AS workstation_no, r.level_at_start AS level, r.exercise_type,
  i.id AS item_id, i.ordinal, i.state AS item_state, i.close_reason,
  i.offered_at, i.opened_at, i.closed_at, i.interruptions,
  i.card->>'number' AS card_number,
  sv.scenario_id, s.title AS scenario_title, sv.version AS scenario_version, sv.difficulty,
  (ev.body->'derived'->>'open_seconds')::numeric AS open_seconds,
  (ev.body->'derived'->>'primary_seconds')::numeric AS primary_seconds,
  (ev.body->'derived'->>'work_seconds')::numeric AS work_seconds,
  (ev.body->'derived'->>'total_seconds')::numeric AS total_seconds,
  fa.assessment_id, fa.revision AS assessment_revision, fa.kind AS assessment_kind,
  fa.status AS assessment_status, fa.score, fa.passed, fa.critical_errors,
  a.criteria, a.feedback
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

-- +goose Down
DROP VIEW lesson_report_rows;
DROP TRIGGER report_files_immutable ON report_files;
DROP FUNCTION guard_report_file_update();
DROP TABLE report_files;
