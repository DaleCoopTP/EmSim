-- +goose Up
-- 112-5a/ADR-024: send_caller_message starts one round of the free-text
-- caller-chat window. Its own effect (the operator's transcript line and
-- a new pending IntakeCallerTurn) lives inside items.intake_state jsonb,
-- already unconstrained beyond items_intake_state_shape/_object
-- (migration 00011) — this migration only extends actions_type_check,
-- the same way 00012-00015 each added their own new command types.
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped', 'ask_intake_question',
  'hold_incoming', 'resume_incoming', 'add_incident_type',
  'remove_incident_type', 'review_service_selection', 'complete_profile_case',
  'notify_services', 'send_caller_message'
));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM actions WHERE type = 'send_caller_message') THEN
    RAISE EXCEPTION 'cannot roll back send_caller_message while data exists';
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped', 'ask_intake_question',
  'hold_incoming', 'resume_incoming', 'add_incident_type',
  'remove_incident_type', 'review_service_selection', 'complete_profile_case',
  'notify_services'
));
