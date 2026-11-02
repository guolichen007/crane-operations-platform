-- +goose Up
-- R2 B1 — Runtime / History foundation.
-- Additive time-quality persistence on the existing telemetry anchor; sampled_at is never redefined.

ALTER TABLE telemetry_samples
  ADD COLUMN source_sent_at timestamptz,
  ADD COLUMN cloud_received_at timestamptz,
  ADD COLUMN boot_id text,
  ADD COLUMN boot_seq bigint,
  ADD COLUMN sequence bigint,
  ADD COLUMN late_arrival interval,
  ADD COLUMN clock_skew_ms bigint,
  ADD COLUMN quality_flags text[] NOT NULL DEFAULT '{}';
CREATE INDEX telemetry_device_received ON telemetry_samples(device_id, cloud_received_at DESC);

-- ---------------------------------------------------------------------------
-- Business revision anchor (BUSINESS_OWNED safe snapshot only; no secret material)
-- ---------------------------------------------------------------------------
CREATE TABLE entity_revisions (
  id bigserial PRIMARY KEY,
  project_id text NOT NULL REFERENCES projects(id),
  asset_id text REFERENCES asset_devices(id),
  entity_type text NOT NULL,
  entity_id text NOT NULL,
  revision bigint NOT NULL DEFAULT 1,
  recorded_at timestamptz NOT NULL,
  effective_at timestamptz NOT NULL,
  changed_by text,
  reason text,
  request_id text NOT NULL DEFAULT '',
  changed_fields text[] NOT NULL DEFAULT '{}',
  snapshot_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  snapshot_sha256 text
);
CREATE INDEX entity_revisions_entity ON entity_revisions(entity_type, entity_id, revision DESC);
CREATE INDEX entity_revisions_asset ON entity_revisions(asset_id, recorded_at);

-- ---------------------------------------------------------------------------
-- Unified event index; canonical payload stays in the source table.
-- ---------------------------------------------------------------------------
CREATE TABLE asset_timeline_events (
  id bigserial PRIMARY KEY,
  project_id text NOT NULL REFERENCES projects(id),
  asset_id text NOT NULL REFERENCES asset_devices(id),
  category text NOT NULL,
  occurred_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  source text NOT NULL DEFAULT '',
  reference_type text NOT NULL,
  reference_id text NOT NULL,
  summary text NOT NULL DEFAULT '',
  severity text NOT NULL DEFAULT 'INFO' CHECK (severity IN ('INFO','WARNING','CRITICAL'))
);
CREATE INDEX asset_timeline_events_asset ON asset_timeline_events(asset_id, occurred_at DESC);

-- ---------------------------------------------------------------------------
-- Runtime binding anchor: DEPLOYMENT_OWNED, business UI read-only, no credentials.
-- ---------------------------------------------------------------------------
CREATE TABLE equipment_runtime_bindings (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  equipment_installation_id text NOT NULL REFERENCES asset_equipment_installations(id),
  source_kind text NOT NULL,
  source_ref text NOT NULL,
  telemetry_namespace text,
  property_aliases jsonb NOT NULL DEFAULT '{}'::jsonb,
  binding_status text NOT NULL DEFAULT 'ACTIVE' CHECK (binding_status IN ('ACTIVE','INACTIVE')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  created_by_deployment boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (valid_to IS NULL OR valid_to > valid_from)
);
CREATE INDEX equipment_runtime_bindings_asset ON equipment_runtime_bindings(asset_id, valid_from);
CREATE UNIQUE INDEX equipment_runtime_bindings_one_active ON equipment_runtime_bindings(equipment_installation_id) WHERE valid_to IS NULL;

-- +goose Down
-- Forward-only: runtime/history foundation is additive and references the R2 anchors.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'runtime/history foundation migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
