-- ДДС-2/ADR-031: a DDS call is either outgoing (call_start, as before) or
-- incoming — the answer to a delivered phone_incoming event, linked by
-- event_key (at most one call per event). Existing rows are outgoing.
-- answer_incoming is already in actions_type_check (00016).
-- +goose Up
ALTER TABLE calls ADD COLUMN direction text NOT NULL DEFAULT 'outgoing' CHECK (direction IN ('outgoing', 'incoming'));
ALTER TABLE calls ADD COLUMN event_key text;
CREATE UNIQUE INDEX calls_event_key_idx ON calls (item_id, event_key) WHERE event_key IS NOT NULL;

-- +goose Down
DROP INDEX calls_event_key_idx;
ALTER TABLE calls DROP COLUMN event_key;
ALTER TABLE calls DROP COLUMN direction;
