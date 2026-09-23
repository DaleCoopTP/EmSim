-- +goose Up
CREATE TABLE intake_catalog_versions (
  version integer PRIMARY KEY CHECK (version > 0),
  definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER intake_catalog_versions_immutable BEFORE UPDATE OR DELETE ON intake_catalog_versions
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

ALTER TABLE lessons ADD COLUMN intake_catalog_version integer REFERENCES intake_catalog_versions(version);

ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped', 'ask_intake_question',
  'hold_incoming', 'resume_incoming', 'add_incident_type',
  'remove_incident_type', 'review_service_selection', 'complete_profile_case'
));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM intake_catalog_versions) OR
     EXISTS (SELECT 1 FROM actions WHERE type IN (
       'add_incident_type', 'remove_incident_type',
       'review_service_selection', 'complete_profile_case')) THEN
    RAISE EXCEPTION 'cannot roll back operator112 profiles while data exists';
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped', 'ask_intake_question',
  'hold_incoming', 'resume_incoming'
));
DROP TRIGGER intake_catalog_versions_immutable ON intake_catalog_versions;
ALTER TABLE lessons DROP COLUMN intake_catalog_version;
DROP TABLE intake_catalog_versions;
