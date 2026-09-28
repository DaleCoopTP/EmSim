-- ДДС-6/ADR-035: weights and the pass threshold a DDS instructor sets for a
-- draft lesson, {weights: {criterion_id: weight}, pass_threshold}. NULL keeps
-- the frozen rubric version's own values, so every existing lesson scores as
-- before.

-- +goose Up
ALTER TABLE lessons ADD COLUMN scoring jsonb;
ALTER TABLE lessons ADD CONSTRAINT lessons_scoring_object CHECK (scoring IS NULL OR jsonb_typeof(scoring) = 'object');

-- +goose Down
ALTER TABLE lessons DROP CONSTRAINT lessons_scoring_object;
ALTER TABLE lessons DROP COLUMN scoring;
