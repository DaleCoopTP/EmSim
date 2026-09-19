-- auth module tables (RFC-001 §4.2, ADR-008): users, workstations,
-- sessions. 1:1 with design-docs/contracts/schema.sql. users.service_code
-- has no FK yet — services (content module) does not exist until slice 2
-- (slice-planning.md §3); it stays a plain text column, validated in Go
-- (internal/auth's ValidateNewUser/ValidateUserPatch), until that FK can
-- be added.
--
-- sessions.id is 32 bytes, but — unlike the "32 random bytes" the schema.sql
-- comment describes for a session id in general — this module stores
-- sha256(token) there, not the token itself: the cookie the client holds
-- is the 32 random bytes, and only their hash ever reaches the database,
-- so a leaked backup or read-only DB access cannot be replayed as a live
-- session. sha256 output is also 32 bytes, so sessions_id_length holds
-- unchanged.

-- +goose Up
CREATE TABLE users (
    id            uuid PRIMARY KEY,
    login         text NOT NULL UNIQUE,
    password_hash text NOT NULL,                       -- argon2id
    full_name     text NOT NULL,
    role          text NOT NULL CHECK (role IN ('admin', 'instructor', 'trainee')),
    service_code  text,                                -- профиль обучаемого (служба); NULL для admin/instructor
    level         text NOT NULL DEFAULT 'easy' CHECK (level IN ('easy', 'medium', 'hard')),
    active        boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_login_shape CHECK (login ~ '^[a-z0-9._-]{3,64}$')
);

CREATE TABLE workstations (
    id         uuid PRIMARY KEY,
    number     integer NOT NULL UNIQUE CHECK (number > 0),
    label      text NOT NULL DEFAULT '',
    ip_address inet,                                   -- опциональная привязка РМ к адресу
    active     boolean NOT NULL DEFAULT true
);

CREATE TABLE sessions (
    id             bytea PRIMARY KEY,                  -- sha256(token), 32 байта
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workstation_id uuid REFERENCES workstations(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    CONSTRAINT sessions_id_length CHECK (octet_length(id) = 32)
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE workstations;
DROP TABLE users;
