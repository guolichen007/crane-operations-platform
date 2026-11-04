-- +goose Up
-- R3 — persist the validated instance-level equipment specification instead of
-- validating then discarding it. Additive-only: keeps 00001..00011 immutable.
ALTER TABLE asset_equipment_installations
  ADD COLUMN spec_json jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE asset_equipment_installations
  DROP COLUMN spec_json;
