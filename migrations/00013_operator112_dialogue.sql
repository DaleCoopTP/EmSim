-- Prepared applicant dialogue uses the existing items.intake_state JSONB;
-- the actions type constraint is the only storage shape change.
-- +goose Up
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped', 'ask_intake_question',
  'hold_incoming', 'resume_incoming'
));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM actions WHERE type IN (
    'ask_intake_question', 'hold_incoming', 'resume_incoming'
  )) THEN
    RAISE EXCEPTION 'cannot roll back operator112 dialogue while dialogue actions exist';
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped'
));
