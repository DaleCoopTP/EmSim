-- ADR-038: login policy. A run of wrong passwords locks the account for a
-- while (failed_logins counts the run, locked_until ends the lock), and an
-- administrator-set password can be marked "change on next login".
-- Existing accounts are neither locked nor forced to change anything.

-- +goose Up
ALTER TABLE users
    ADD COLUMN failed_logins       integer NOT NULL DEFAULT 0 CHECK (failed_logins >= 0),
    ADD COLUMN locked_until        timestamptz,
    ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users
    DROP COLUMN must_change_password,
    DROP COLUMN locked_until,
    DROP COLUMN failed_logins;
