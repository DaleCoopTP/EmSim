-- 112-7/ADR-027: a preview lesson lets an instructor run their own draft
-- scenario as its sole participant, with no workstation at all. This adds
-- a third lessons/runs mode and makes workstation_id optional on
-- assignments/runs — the application (not this CHECK) enforces that NULL
-- only ever appears together with mode='preview'.
-- +goose Up
ALTER TABLE lessons DROP CONSTRAINT lessons_mode_check;
ALTER TABLE lessons ADD CONSTRAINT lessons_mode_check CHECK (mode IN ('intro', 'training', 'preview'));

ALTER TABLE runs DROP CONSTRAINT runs_mode_check;
ALTER TABLE runs ADD CONSTRAINT runs_mode_check CHECK (mode IN ('intro', 'training', 'preview'));

ALTER TABLE runs ALTER COLUMN workstation_id DROP NOT NULL;

-- assignments' PK was (lesson_id, workstation_id) — a PK column cannot
-- drop NOT NULL while it is still part of the PK, and the PK itself is
-- unusable once workstation_id can be NULL (a preview assignment has
-- none, and NULL never equals NULL for a PK/uniqueness check anyway).
-- (lesson_id, user_id) is already UNIQUE and is the natural
-- per-participant key, so swap the PK to it before touching NOT NULL.
ALTER TABLE assignments DROP CONSTRAINT assignments_pkey;
ALTER TABLE assignments ADD PRIMARY KEY (lesson_id, user_id);
ALTER TABLE assignments ALTER COLUMN workstation_id DROP NOT NULL;
CREATE UNIQUE INDEX assignments_workstation_idx ON assignments (lesson_id, workstation_id) WHERE workstation_id IS NOT NULL;

-- runs kept UNIQUE(lesson_id, workstation_id) as a real UNIQUE constraint
-- (not an index), which also breaks under NULL workstation_id semantics
-- the moment two preview runs would exist in unrelated lessons — replace
-- it with the same partial-index shape as assignments.
ALTER TABLE runs DROP CONSTRAINT runs_lesson_id_workstation_id_key;
CREATE UNIQUE INDEX runs_lesson_workstation_idx ON runs (lesson_id, workstation_id) WHERE workstation_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM lessons WHERE mode = 'preview') THEN
    RAISE EXCEPTION 'cannot roll back preview lesson mode while data exists';
  END IF;
END $$;
-- +goose StatementEnd
DROP INDEX runs_lesson_workstation_idx;
ALTER TABLE runs ADD CONSTRAINT runs_lesson_id_workstation_id_key UNIQUE (lesson_id, workstation_id);

DROP INDEX assignments_workstation_idx;
ALTER TABLE assignments DROP CONSTRAINT assignments_pkey;
ALTER TABLE assignments ADD PRIMARY KEY (lesson_id, workstation_id);

ALTER TABLE assignments ALTER COLUMN workstation_id SET NOT NULL;
ALTER TABLE runs ALTER COLUMN workstation_id SET NOT NULL;

ALTER TABLE runs DROP CONSTRAINT runs_mode_check;
ALTER TABLE runs ADD CONSTRAINT runs_mode_check CHECK (mode IN ('intro', 'training'));

ALTER TABLE lessons DROP CONSTRAINT lessons_mode_check;
ALTER TABLE lessons ADD CONSTRAINT lessons_mode_check CHECK (mode IN ('intro', 'training'));
