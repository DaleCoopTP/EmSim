-- Slice 112-1: widen the exercise discriminator without changing existing
-- DDS rows, and persist the one immutable dispatch snapshot of an intake.
-- +goose Up
ALTER TABLE scenarios ALTER COLUMN target_service DROP NOT NULL;

ALTER TABLE scenario_versions DROP CONSTRAINT scenario_versions_exercise_type_check;
ALTER TABLE scenario_versions ADD CONSTRAINT scenario_versions_exercise_type_check
  CHECK (exercise_type IN ('dds_processing', 'operator112_intake'));

ALTER TABLE lessons DROP CONSTRAINT lessons_exercise_type_check;
ALTER TABLE lessons ADD CONSTRAINT lessons_exercise_type_check
  CHECK (exercise_type IN ('dds_processing', 'operator112_intake'));

ALTER TABLE runs DROP CONSTRAINT runs_exercise_type_check;
ALTER TABLE runs ADD CONSTRAINT runs_exercise_type_check
  CHECK (exercise_type IN ('dds_processing', 'operator112_intake'));

ALTER TABLE trainee_assessment_state DROP CONSTRAINT trainee_assessment_state_exercise_type_check;
ALTER TABLE trainee_assessment_state ADD CONSTRAINT trainee_assessment_state_exercise_type_check
  CHECK (exercise_type IN ('dds_processing', 'operator112_intake'));

ALTER TABLE items ADD COLUMN exercise_type text NOT NULL DEFAULT 'dds_processing'
  CHECK (exercise_type IN ('dds_processing', 'operator112_intake'));
ALTER TABLE items ADD COLUMN intake_state jsonb
  CONSTRAINT items_intake_state_object CHECK (intake_state IS NULL OR jsonb_typeof(intake_state) = 'object');
ALTER TABLE items ADD CONSTRAINT items_intake_state_shape CHECK (
  (exercise_type = 'dds_processing' AND intake_state IS NULL) OR
  (exercise_type = 'operator112_intake' AND intake_state IS NOT NULL)
);

ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake'
));

CREATE TABLE intake_dispatches (
  item_id uuid PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  action_id uuid NOT NULL UNIQUE REFERENCES actions(id),
  service_code text NOT NULL REFERENCES services(code),
  card_snapshot jsonb NOT NULL CHECK (jsonb_typeof(card_snapshot) = 'object'),
  sent_at timestamptz NOT NULL
);
CREATE TRIGGER intake_dispatches_immutable BEFORE UPDATE OR DELETE ON intake_dispatches
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

-- +goose Down
-- A rollback must not silently discard 112 cards or immutable dispatches.
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM items WHERE exercise_type = 'operator112_intake') OR
     EXISTS (SELECT 1 FROM lessons WHERE exercise_type = 'operator112_intake') OR
     EXISTS (SELECT 1 FROM scenario_versions WHERE exercise_type = 'operator112_intake') THEN
    RAISE EXCEPTION 'cannot roll back operator112 intake while 112 data exists';
  END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER intake_dispatches_immutable ON intake_dispatches;
DROP TABLE intake_dispatches;
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close'
));
ALTER TABLE items DROP CONSTRAINT items_intake_state_shape;
ALTER TABLE items DROP COLUMN intake_state;
ALTER TABLE items DROP COLUMN exercise_type;
ALTER TABLE trainee_assessment_state DROP CONSTRAINT trainee_assessment_state_exercise_type_check;
ALTER TABLE trainee_assessment_state ADD CONSTRAINT trainee_assessment_state_exercise_type_check
  CHECK (exercise_type = 'dds_processing');
ALTER TABLE runs DROP CONSTRAINT runs_exercise_type_check;
ALTER TABLE runs ADD CONSTRAINT runs_exercise_type_check CHECK (exercise_type = 'dds_processing');
ALTER TABLE lessons DROP CONSTRAINT lessons_exercise_type_check;
ALTER TABLE lessons ADD CONSTRAINT lessons_exercise_type_check CHECK (exercise_type = 'dds_processing');
ALTER TABLE scenario_versions DROP CONSTRAINT scenario_versions_exercise_type_check;
ALTER TABLE scenario_versions ADD CONSTRAINT scenario_versions_exercise_type_check CHECK (exercise_type = 'dds_processing');
ALTER TABLE scenarios ALTER COLUMN target_service SET NOT NULL;
