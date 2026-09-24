-- +goose Up
-- ADR-023: the single "notify and save" completion record for card_only
-- and full_case 112 items, replacing per-service intake_dispatches for
-- items that use it. One row per item; intake_dispatches is untouched
-- and keeps serving the legacy incoming_call route.
CREATE TABLE intake_notifications (
  item_id uuid PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  action_id uuid NOT NULL UNIQUE REFERENCES actions(id),
  services jsonb NOT NULL CHECK (jsonb_typeof(services) = 'array' AND jsonb_array_length(services) > 0),
  reason text NOT NULL DEFAULT '',
  card_snapshot jsonb NOT NULL CHECK (jsonb_typeof(card_snapshot) = 'object'),
  notified_at timestamptz NOT NULL
);
CREATE TRIGGER intake_notifications_immutable BEFORE UPDATE OR DELETE ON intake_notifications
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

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

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM intake_notifications) OR
     EXISTS (SELECT 1 FROM actions WHERE type = 'notify_services') THEN
    RAISE EXCEPTION 'cannot roll back operator112 notifications while data exists';
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
  'remove_incident_type', 'review_service_selection', 'complete_profile_case'
));
DROP TRIGGER intake_notifications_immutable ON intake_notifications;
DROP TABLE intake_notifications;
