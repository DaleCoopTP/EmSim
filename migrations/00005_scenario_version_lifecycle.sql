-- Tighten the scenario-version lifecycle for databases that already applied
-- 00004 before the one-way transition and approval-attribution checks were
-- added there. Fresh databases create the base trigger in 00004 and tighten
-- it here; existing databases replace the function without rebuilding data.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_scenario_version_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','approved_by','approved_at']) IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','approved_by','approved_at']) THEN
    RAISE EXCEPTION 'immutable scenario version content';
  END IF;
  IF OLD.status <> NEW.status AND NOT (
       (OLD.status = 'draft' AND NEW.status IN ('approved', 'superseded')) OR
       (OLD.status = 'approved' AND NEW.status = 'superseded')
     ) THEN
    RAISE EXCEPTION 'invalid scenario version status transition: % -> %', OLD.status, NEW.status;
  END IF;
  IF NOT (OLD.status = 'draft' AND NEW.status = 'approved') AND
     (NEW.approved_by IS DISTINCT FROM OLD.approved_by OR
      NEW.approved_at IS DISTINCT FROM OLD.approved_at) THEN
    RAISE EXCEPTION 'immutable scenario version approval attribution';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Restore the 00004 definition so rolling this migration back leaves the
-- database exactly at the preceding schema version.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_scenario_version_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','approved_by','approved_at']) IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','approved_by','approved_at']) THEN
    RAISE EXCEPTION 'immutable scenario version content';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
