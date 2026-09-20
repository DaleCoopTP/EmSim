-- Slice 4's durable training timeline: scenario events and post-close
-- control reports.  items already acquired the ancestry/interruption
-- columns in 00006; this migration makes the remaining target-schema
-- storage available without changing the existing command protocol.

-- +goose Up
ALTER TABLE lessons
    ADD COLUMN stop_reason text CHECK (stop_reason IS NULL OR length(stop_reason) <= 500);

CREATE TABLE item_events (
    id           uuid PRIMARY KEY,
    item_id      uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    event_key    text NOT NULL,
    anchor_at    timestamptz NOT NULL,
    due_at       timestamptz NOT NULL,
    state        text NOT NULL CHECK (state IN ('scheduled', 'delivered', 'skipped')),
    delivered_at timestamptz,
    late         boolean NOT NULL DEFAULT false,
    skip_reason  text,
    UNIQUE (item_id, event_key),
    CONSTRAINT item_events_shape CHECK (
      (state='scheduled' AND delivered_at IS NULL AND skip_reason IS NULL) OR
      (state='delivered' AND delivered_at IS NOT NULL AND skip_reason IS NULL) OR
      (state='skipped' AND delivered_at IS NULL AND skip_reason IS NOT NULL)
    )
);
CREATE INDEX item_events_due_idx ON item_events (due_at) WHERE state = 'scheduled';

CREATE TABLE control_reports (
    id         uuid PRIMARY KEY,
    item_id    uuid NOT NULL REFERENCES items(id),
    action_id  uuid NOT NULL UNIQUE REFERENCES actions(id),
    text       text NOT NULL CHECK (length(btrim(text)) > 0),
    created_at timestamptz NOT NULL
);
CREATE TRIGGER control_reports_immutable
    BEFORE UPDATE OR DELETE ON control_reports
    FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

-- +goose Down
DROP TRIGGER control_reports_immutable ON control_reports;
DROP TABLE control_reports;
DROP INDEX item_events_due_idx;
DROP TABLE item_events;
ALTER TABLE lessons DROP COLUMN stop_reason;
