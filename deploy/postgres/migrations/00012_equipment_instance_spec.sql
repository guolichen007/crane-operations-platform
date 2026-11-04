-- +goose Up
-- R3 — persist the validated instance-level equipment specification instead of
-- validating then discarding it. Additive-only: keeps 00001..00011 immutable.
ALTER TABLE asset_equipment_installations
  ADD COLUMN spec_json jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
-- Intentionally a no-op: spec_json holds validated, business-critical instance
-- specifications. Dropping the column would silently destroy recorded spec data
-- on rollback — precisely what this column was added to preserve. The column is
-- additive and harmless to keep; a genuine rollback must first export spec_json,
-- then apply a dedicated data-preserving migration. Never DROP here for
-- "rollback convenience".
