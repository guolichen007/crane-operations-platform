-- +goose Up
-- R3 — persist the validated instance-level equipment specification instead of
-- validating then discarding it. Additive-only: keeps 00001..00011 immutable.
ALTER TABLE asset_equipment_installations
  ADD COLUMN spec_json jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
-- Forward-only: dropping spec_json would destroy recorded, business-critical
-- instance specs on rollback, and a silent no-op would desynchronize the goose
-- version record from the retained column (making the Up non-rerunnable). Refuse
-- to roll back; a genuine rollback must use a verified backup restore.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'equipment instance spec is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
