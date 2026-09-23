-- The operator's no-contact and dropped-call controls close an intake
-- without dispatch. The expanded card itself remains in items.card JSONB.
-- +goose Up
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake',
  'mark_no_contact', 'mark_call_dropped'
));

ALTER TABLE items DROP CONSTRAINT items_close_reason_check;
ALTER TABLE items ADD CONSTRAINT items_close_reason_check CHECK (close_reason IN (
  'completed', 'refused', 'interrupted', 'pilot_completed', 'no_contact', 'call_dropped'
));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM actions WHERE type IN ('mark_no_contact', 'mark_call_dropped')) OR
     EXISTS (SELECT 1 FROM items WHERE close_reason IN ('no_contact', 'call_dropped')) THEN
    RAISE EXCEPTION 'cannot roll back operator112 card surface while exceptional calls exist';
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE items DROP CONSTRAINT items_close_reason_check;
ALTER TABLE items ADD CONSTRAINT items_close_reason_check CHECK (close_reason IN (
  'completed', 'refused', 'interrupted', 'pilot_completed'
));
ALTER TABLE actions DROP CONSTRAINT actions_type_check;
ALTER TABLE actions ADD CONSTRAINT actions_type_check CHECK (type IN (
  'open', 'set_status', 'add_comment', 'set_card_field', 'call_start', 'call_end',
  'control_report', 'close', 'answer_incoming', 'end_incoming',
  'save_intake_draft', 'dispatch_intake', 'complete_intake'
));
