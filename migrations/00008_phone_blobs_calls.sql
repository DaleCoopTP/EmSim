-- Slice 5: immutable content-addressed media and the durable phone timeline.
-- +goose Up
CREATE TABLE blobs (
    id uuid PRIMARY KEY,
    sha256 bytea NOT NULL UNIQUE CHECK (octet_length(sha256) = 32),
    mime text NOT NULL,
    size bigint NOT NULL CHECK (size >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE voice_assets (
    id uuid PRIMARY KEY,
    scenario_version_id uuid NOT NULL REFERENCES scenario_versions(id) ON DELETE CASCADE,
    key text NOT NULL,
    voice text NOT NULL DEFAULT '',
    blob_id uuid NOT NULL REFERENCES blobs(id),
    UNIQUE (scenario_version_id, key)
);

CREATE TABLE calls (
    id uuid PRIMARY KEY,
    item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    contact_key text NOT NULL,
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    reaction_at_call text NOT NULL,
    blob_id uuid REFERENCES blobs(id),
    accepted_by text,
    summary text,
    recording_sha256 bytea CHECK (recording_sha256 IS NULL OR octet_length(recording_sha256) = 32),
    recording_size bigint CHECK (recording_size IS NULL OR recording_size BETWEEN 1 AND 10485760),
    recording_mime text CHECK (recording_mime IN ('audio/webm', 'audio/ogg', 'audio/wav')),
    recording_state text NOT NULL DEFAULT 'absent' CHECK (recording_state IN ('absent', 'awaiting', 'ready', 'expired')),
    recording_upload_deadline_at timestamptz,
    recording_received_at timestamptz,
    CONSTRAINT calls_recording_manifest CHECK (
      (recording_state = 'absent' AND recording_sha256 IS NULL AND recording_size IS NULL AND recording_mime IS NULL AND blob_id IS NULL AND recording_received_at IS NULL) OR
      (recording_state IN ('awaiting', 'ready', 'expired') AND ended_at IS NOT NULL AND recording_sha256 IS NOT NULL AND recording_size IS NOT NULL AND recording_mime IS NOT NULL AND
       ((recording_state = 'ready' AND blob_id IS NOT NULL AND recording_received_at IS NOT NULL) OR
        (recording_state IN ('awaiting', 'expired') AND blob_id IS NULL AND recording_received_at IS NULL)))
    )
);
CREATE INDEX calls_item_idx ON calls (item_id);
CREATE UNIQUE INDEX calls_one_active_per_item_idx ON calls (item_id) WHERE ended_at IS NULL;

-- +goose Down
DROP INDEX calls_one_active_per_item_idx;
DROP INDEX calls_item_idx;
DROP TABLE calls;
DROP TABLE voice_assets;
DROP TABLE blobs;
