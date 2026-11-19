-- +goose Up
-- R9 收口 — metering trust, real-asset readiness records, explicit test accounts. Additive over
-- 00001..00015 (00013 / 00015 are not rewritten; no existing row changes meaning).
--
--   * meter_states.trust_state: a cumulative value BELOW the trusted high-water mark (same boot or
--     after an edge restart) is never counted and never moves the mark; the meter is held
--     DECREASE_PENDING until the value returns to the mark (a glitch) or an administrator confirms
--     a real PLC counter reset (meter_counter_events). A process restart is never taken as a reset.
--   * maintenance_schedules.data_gaps / reason: which condition of a rule could not be computed
--     (TIME / METER) and why — a partial result is shown as such, never as a silent "normal".
--   * point_config_changes: every confirmation / change of a raw point (address, active level, edge,
--     data type, unit) with before / after; a review issue can name the change that resolved it.
--   * asset_pending_attributes.version / decision_basis: nameplate / location decisions are
--     optimistic-locked and carry their evidence.
--   * users.test_account: only an explicitly marked test account can ever be purged permanently;
--     OWNER / service identities (protected_reason) can never be marked.
ALTER TABLE meter_states
  ADD COLUMN trust_state text NOT NULL DEFAULT 'OK' CHECK (trust_state IN ('OK','DECREASE_PENDING')),
  ADD COLUMN pending_since timestamptz,
  ADD COLUMN pending_first_raw numeric,
  ADD COLUMN pending_last_raw numeric;
ALTER TABLE meter_states ADD CONSTRAINT meter_states_pending_consistent
  CHECK ((trust_state = 'OK') = (pending_since IS NULL) AND (pending_since IS NULL) = (pending_first_raw IS NULL) AND (pending_since IS NULL) = (pending_last_raw IS NULL));

CREATE TABLE meter_counter_events (
  id text PRIMARY KEY,
  meter_id text NOT NULL REFERENCES meter_definitions(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  event_kind text NOT NULL CHECK (event_kind IN ('RESET_CONFIRMED')),
  pending_since timestamptz NOT NULL,
  pending_first_raw numeric NOT NULL,
  pending_last_raw numeric NOT NULL,
  accumulated_before numeric NOT NULL,
  accumulated_after numeric NOT NULL CHECK (accumulated_after >= accumulated_before),
  reason text NOT NULL CHECK (length(reason) BETWEEN 2 AND 200),
  actor text NOT NULL,
  request_id text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX meter_counter_events_meter ON meter_counter_events(meter_id, source_mode, occurred_at DESC);

ALTER TABLE maintenance_schedules
  ADD COLUMN data_gaps text[] NOT NULL DEFAULT '{}',
  ADD COLUMN reason text;

CREATE TABLE point_config_changes (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  point_id text NOT NULL REFERENCES telemetry_points(id),
  from_version bigint NOT NULL,
  to_version bigint NOT NULL CHECK (to_version = from_version + 1),
  before jsonb NOT NULL,
  after jsonb NOT NULL,
  basis text NOT NULL CHECK (length(basis) BETWEEN 2 AND 300),
  actor text NOT NULL,
  request_id text NOT NULL DEFAULT '',
  changed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (point_id, to_version)
);
CREATE INDEX point_config_changes_asset ON point_config_changes(asset_id, changed_at DESC);
ALTER TABLE point_review_issues ADD COLUMN config_change_id text REFERENCES point_config_changes(id);

ALTER TABLE asset_pending_attributes
  ADD COLUMN version bigint NOT NULL DEFAULT 1,
  ADD COLUMN decision_basis text;

ALTER TABLE users ADD COLUMN test_account boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD CONSTRAINT users_test_account_unprotected CHECK (NOT test_account OR protected_reason IS NULL);

-- +goose Down
-- Forward-only: trust decisions, configuration history and account markers are evidence.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'metering trust / readiness migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
