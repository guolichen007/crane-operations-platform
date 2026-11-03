-- +goose Up
-- R2 B3 — asset master data CRUD foundation.
-- 00001..00008 are frozen.

-- Location code P2 fix: a project may hold any number of unnumbered locations.
-- blank code is normalized to NULL; the existing UNIQUE(project_id, code) then
-- naturally permits multiple NULLs.
ALTER TABLE asset_locations ALTER COLUMN code DROP NOT NULL;
ALTER TABLE asset_locations ALTER COLUMN code DROP DEFAULT;
UPDATE asset_locations SET code = NULL WHERE code = '';

-- Idempotency-Key persistence (durable, not in-memory): same actor+operation+key
-- with the same payload hash returns the same entity; a different payload hash
-- conflicts.
CREATE TABLE idempotency_keys (
  key text PRIMARY KEY,
  actor text NOT NULL,
  operation text NOT NULL,
  payload_sha256 text NOT NULL,
  result_json jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX idempotency_keys_expiry ON idempotency_keys(expires_at);

-- Manufacturer / Catalog scope freeze: project_id IS NULL means GLOBAL (business
-- API read-only); a non-null project_id is a project-scoped record. The
-- cross-project reference guard is enforced in the service layer; the columns
-- already exist.

-- +goose Down
-- Forward-only: asset master data migration is additive over frozen B2.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'asset master data migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
