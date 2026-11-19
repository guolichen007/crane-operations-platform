-- +goose Up
-- R9 Stage A4/A5 + Stage B — first real asset onboarding (老河口 LHK-CRANE-001), signal points, metering,
-- preventive maintenance, work orders, replacements and wire ropes. Additive over 00001..00014.
--
-- Separation (docs/03-数据模型/Phase1-A1.1-R2-可靠性钢丝绳点检维保数据模型.md, 遥测指标目录):
--   raw signal point (telemetry_points)  ≠ equipment instance (asset_equipment_installations)
--   ≠ meter (meter_definitions, several per point)  ≠ maintenance rule (maintenance_rules).
-- Every runtime-derived row carries source_mode REAL / SIMULATION / TEST; SIMULATION and TEST rows
-- never feed REAL meters, schedules, alerts, work orders or compliance views (CHECKs + keys below).
-- Nothing here is ever deleted by business users; corrections are new rows / versions + audit.

-- ---------------------------------------------------------------------------------------------
-- A5 onboarding: every import apply and every ORIGINAL source row, lossless.
-- ---------------------------------------------------------------------------------------------
CREATE TABLE onboarding_imports (
  id text PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('LHK_FIRST_ASSET')),
  project_id text NOT NULL REFERENCES projects(id),
  asset_id text NOT NULL REFERENCES asset_devices(id),
  source_filename text NOT NULL,
  source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[a-f0-9]{64}$'),
  staging_sha256 text NOT NULL CHECK (staging_sha256 ~ '^[a-f0-9]{64}$'),
  approved_digest text NOT NULL,
  applied_by text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now(),
  result text NOT NULL CHECK (result IN ('success','noop')),
  summary jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX onboarding_imports_asset ON onboarding_imports(asset_id, applied_at DESC);

CREATE TABLE onboarding_source_rows (
  id text PRIMARY KEY,
  project_id text NOT NULL REFERENCES projects(id),
  asset_id text NOT NULL REFERENCES asset_devices(id),
  source_filename text NOT NULL,
  source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[a-f0-9]{64}$'),
  source_sheet text NOT NULL,
  source_row integer NOT NULL CHECK (source_row > 0),
  source_index integer NOT NULL CHECK (source_index > 0),
  row_kind text NOT NULL CHECK (row_kind IN ('EQUIPMENT','OPERATION')),
  group_name text NOT NULL,
  item_name text NOT NULL,
  raw jsonb NOT NULL,             -- exactly the original cells (variable type, PLC address, statistic, brand/model, life, remarks)
  proposed jsonb NOT NULL,        -- the staging proposal; every inferred field stays PENDING / UNVERIFIED
  review_flags text[] NOT NULL DEFAULT '{}',
  equipment_installation_id text, -- the EQUIPMENT rows only (51)
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_sha256, source_sheet, source_row),
  UNIQUE (asset_id, source_index),
  CHECK ((row_kind = 'EQUIPMENT') = (equipment_installation_id IS NOT NULL)),
  FOREIGN KEY (equipment_installation_id, asset_id) REFERENCES asset_equipment_installations (id, asset_id)
);

-- Inferred / unknown asset facts stay out of the authoritative columns until confirmed: the model
-- text "QZ10t-31m" suggests 10 t / 31 m, but rated_capacity_t / span_m stay NULL; the candidate is
-- shown as 待铭牌确认 from here. Same for rope count, LiDAR dimension, RFID entity, capacity.
CREATE TABLE asset_pending_attributes (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  subject text NOT NULL,                    -- 'asset' or an equipment slot (HOIST-38 …)
  attribute text NOT NULL,                  -- ratedCapacityT, spanM, ropeCount, lidarDimension, rfidEntity …
  candidate jsonb,                          -- NULL = unknown, no candidate
  basis text NOT NULL,                      -- why this candidate exists (e.g. parsed from the model text)
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','CONFIRMED','REJECTED')),
  confirmed_value jsonb,
  decided_by text, decided_at timestamptz,
  source_row_id text REFERENCES onboarding_source_rows(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (asset_id, subject, attribute),
  CHECK (status = 'PENDING' OR (decided_by IS NOT NULL AND decided_at IS NOT NULL))
);

-- ---------------------------------------------------------------------------------------------
-- B2 raw signal points (one per source row) and review issues.
-- ---------------------------------------------------------------------------------------------
CREATE TABLE telemetry_points (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  point_code text NOT NULL CHECK (point_code ~ '^[A-Za-z0-9_.-]{1,80}$'),
  name text NOT NULL,
  mechanism text NOT NULL,                  -- 大车机构 / 小车机构 / ... (source group)
  equipment_slot text,                      -- stable semantic slot (GANTRY-01 …); NULL for operation rows
  plc_address_raw text,                     -- the original string, never rewritten (may be NULL or '/')
  plc_area text, plc_db integer, plc_byte integer, plc_bit integer,  -- parsed only when unambiguous
  address_kind text NOT NULL CHECK (address_kind IN ('BIT','WORD','NONE','UNPARSED')),
  raw_data_type text NOT NULL CHECK (raw_data_type IN ('INT','FLOAT','STRING','BOOL')),
  derived_type text NOT NULL CHECK (derived_type IN ('EDGE_COUNT','ON_DURATION','DAILY_VALUE','EVENT_LIST','STATE')),
  unit_raw text,                            -- the original 统计 cell
  unit text NOT NULL,                       -- count | h | t | none
  active_level text CHECK (active_level IN ('HIGH','LOW')),     -- NULL = not confirmed on site
  edge text CHECK (edge IN ('RISING','FALLING','BOTH')),         -- NULL = not confirmed
  acquisition_source text NOT NULL CHECK (acquisition_source IN ('PLC','LIDAR_DRIVER','EDGE_APP','UNKNOWN')),
  confirmation_status text NOT NULL DEFAULT 'UNCONFIRMED' CHECK (confirmation_status IN ('UNCONFIRMED','CONFIRMED','REJECTED')),
  quality text NOT NULL DEFAULT 'UNVERIFIED' CHECK (quality IN ('UNVERIFIED','VERIFIED','SUSPECT')),
  version bigint NOT NULL DEFAULT 1,
  source_row_id text NOT NULL REFERENCES onboarding_source_rows(id),
  reviewed_by text, reviewed_at timestamptz, review_note text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (asset_id, point_code),
  CHECK (confirmation_status = 'UNCONFIRMED' OR (reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL)),
  -- A DBX (bit) address is boolean on the wire; INT / FLOAT is the published (edge-derived) value.
  CHECK (address_kind <> 'BIT' OR plc_bit IS NOT NULL)
);

CREATE TABLE point_review_issues (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  point_id text REFERENCES telemetry_points(id),
  source_row_id text REFERENCES onboarding_source_rows(id),
  issue_code text NOT NULL,
  detail jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','RESOLVED','ACCEPTED')),
  resolved_by text, resolved_at timestamptz, resolution text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (asset_id, issue_code, source_row_id),
  CHECK (status = 'OPEN' OR (resolved_by IS NOT NULL AND resolution IS NOT NULL))
);

-- ---------------------------------------------------------------------------------------------
-- B2/B3 meters: several per raw point; cumulative state per source mode; reading history.
-- ---------------------------------------------------------------------------------------------
CREATE TABLE meter_definitions (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  point_id text NOT NULL REFERENCES telemetry_points(id),
  meter_code text NOT NULL CHECK (meter_code ~ '^[A-Za-z0-9_.-]{1,120}$'),
  metric text NOT NULL CHECK (metric IN ('cycles','hours','mechanism_running_hours','daily_auto_hours','production_tonnes','daily_job_count','state')),
  meter_kind text NOT NULL CHECK (meter_kind IN ('CUMULATIVE','DAILY','STATE')),
  unit text NOT NULL,
  scope text NOT NULL CHECK (scope IN ('EQUIPMENT','MECHANISM','ASSET')),
  equipment_slot text,                      -- EQUIPMENT scope: the slot whose installed instance it measures
  formula text NOT NULL,                    -- how it is derived from the raw point (documentation, versioned)
  status text NOT NULL DEFAULT 'PENDING_REVIEW' CHECK (status IN ('PENDING_REVIEW','ACTIVE','UNIT_CONFLICT_PENDING','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (asset_id, meter_code),
  CHECK ((scope = 'EQUIPMENT') = (equipment_slot IS NOT NULL))
);

-- Cloud-persisted cumulative value. `accumulated` only ever grows: a device counter reset, a lower
-- value, a gap or a restart never decreases it (anomalies are recorded on the reading).
CREATE TABLE meter_states (
  meter_id text NOT NULL REFERENCES meter_definitions(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  runtime_device_id text NOT NULL,
  last_raw_value numeric,
  last_boot_id text, last_boot_seq bigint, last_sequence bigint,
  last_sampled_at timestamptz,
  accumulated numeric NOT NULL DEFAULT 0 CHECK (accumulated >= 0),
  last_quality text,
  readings bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (meter_id, source_mode)
);

CREATE TABLE meter_readings (
  id bigserial PRIMARY KEY,
  meter_id text NOT NULL REFERENCES meter_definitions(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  runtime_device_id text NOT NULL,
  message_id text NOT NULL,
  boot_id text NOT NULL, boot_seq bigint NOT NULL, sequence bigint NOT NULL,
  sampled_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now(),
  raw_value numeric,                        -- NULL = missing sample (never treated as 0)
  quality text NOT NULL CHECK (quality IN ('GOOD','UNCERTAIN','BAD','MISSING')),
  delta numeric NOT NULL DEFAULT 0 CHECK (delta >= 0),
  accumulated numeric NOT NULL,
  anomaly text CHECK (anomaly IN ('COUNTER_RESET','DECREASE','GAP','OUT_OF_ORDER','QUALITY','FIRST')),
  UNIQUE (meter_id, source_mode, runtime_device_id, boot_id, sequence)
);
CREATE INDEX meter_readings_history ON meter_readings(meter_id, source_mode, sampled_at DESC);

CREATE TABLE daily_metric_values (
  meter_id text NOT NULL REFERENCES meter_definitions(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  day date NOT NULL,
  value numeric NOT NULL CHECK (value >= 0),
  last_boot_id text NOT NULL, last_sequence bigint NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (meter_id, source_mode, day)
);

CREATE TABLE fault_events (
  id bigserial PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  point_id text REFERENCES telemetry_points(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  runtime_device_id text NOT NULL,
  event_id text NOT NULL,
  code text NOT NULL, text text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_mode, runtime_device_id, event_id)
);
CREATE INDEX fault_events_asset ON fault_events(asset_id, source_mode, occurred_at DESC);

-- Usage base of one installed instance on one meter. Equipment usage = accumulated - baseline
-- (+ initial_usage when the instance was already used before metering began). Written once per
-- (installation, meter, mode); a replacement creates a NEW installation and a NEW baseline.
CREATE TABLE equipment_meter_baselines (
  id text PRIMARY KEY,
  equipment_installation_id text NOT NULL REFERENCES asset_equipment_installations(id),
  meter_id text NOT NULL REFERENCES meter_definitions(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  baseline_accumulated numeric NOT NULL CHECK (baseline_accumulated >= 0),
  initial_usage numeric CHECK (initial_usage >= 0),   -- NULL = unknown actual initial counter (数据不足)
  baseline_kind text NOT NULL CHECK (baseline_kind IN ('INSTALL','REPLACEMENT','INITIAL_RECORD')),
  recorded_by text NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  note text NOT NULL DEFAULT '',
  UNIQUE (equipment_installation_id, meter_id, source_mode)
);

-- ---------------------------------------------------------------------------------------------
-- B4 maintenance / calibration / inspection rules, per-mode schedule state and alerts.
-- ---------------------------------------------------------------------------------------------
CREATE TABLE maintenance_rules (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  rule_code text NOT NULL,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  name text NOT NULL,
  rule_kind text NOT NULL CHECK (rule_kind IN ('MAINTENANCE','CALIBRATION','INSPECTION','REPLACEMENT')),
  rule_type text NOT NULL CHECK (rule_type IN ('TIME_BASED','METER_BASED','HYBRID')),
  equipment_slot text,                      -- NULL = whole asset
  meter_id text REFERENCES meter_definitions(id),
  interval_days integer CHECK (interval_days > 0),
  interval_meter numeric CHECK (interval_meter > 0),
  lead_days integer[] NOT NULL DEFAULT '{90,30,7}',
  warn_percents integer[] NOT NULL DEFAULT '{80,90}',
  basis text NOT NULL,                      -- 来源依据 (e.g. the original Excel life text, a standard)
  source_row_id text REFERENCES onboarding_source_rows(id),
  approval_status text NOT NULL DEFAULT 'DRAFT_UNAPPROVED' CHECK (approval_status IN ('DRAFT_UNAPPROVED','APPROVED','RETIRED')),
  approved_by text, approved_at timestamptz,
  effective_from timestamptz,
  test_only boolean NOT NULL DEFAULT false, -- SIMULATION / TEST threshold fixtures; never evaluated for REAL
  test_anchor_at timestamptz,               -- test_only: simulated installation date (injected clock), never a real date
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (asset_id, rule_code, version),
  CHECK (rule_type = 'METER_BASED' OR interval_days IS NOT NULL OR approval_status = 'DRAFT_UNAPPROVED'),
  CHECK (rule_type = 'TIME_BASED' OR (meter_id IS NOT NULL AND (interval_meter IS NOT NULL OR approval_status = 'DRAFT_UNAPPROVED'))),
  CHECK (approval_status <> 'APPROVED' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL AND effective_from IS NOT NULL)),
  CHECK (test_anchor_at IS NULL OR test_only),
  CHECK (NOT (test_only AND approval_status = 'APPROVED'))
);
CREATE UNIQUE INDEX maintenance_rules_one_approved ON maintenance_rules(asset_id, rule_code) WHERE approval_status = 'APPROVED';

CREATE TABLE maintenance_schedules (
  rule_id text NOT NULL REFERENCES maintenance_rules(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  cycle_no integer NOT NULL DEFAULT 1 CHECK (cycle_no > 0),
  anchor_at timestamptz,                    -- installation / last completion; NULL = 数据不足 for the time part
  anchor_meter numeric,                     -- meter accumulated at the anchor; NULL = 数据不足 for the meter part
  next_due_at timestamptz,
  next_due_meter numeric,
  status text NOT NULL DEFAULT 'INSUFFICIENT_DATA' CHECK (status IN ('NORMAL','DUE_SOON','DUE','OVERDUE','INSUFFICIENT_DATA')),
  stage text,                               -- highest stage reached in this cycle (LEAD_90 … OVERDUE, PCT_80 … PCT_100)
  progress_percent numeric,
  days_left integer,
  evaluated_at timestamptz,
  version bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (rule_id, source_mode)
);

CREATE TABLE maintenance_alerts (
  id text PRIMARY KEY,
  rule_id text NOT NULL REFERENCES maintenance_rules(id),
  asset_id text NOT NULL REFERENCES asset_devices(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  cycle_no integer NOT NULL,
  stage text NOT NULL CHECK (stage IN ('LEAD_90','LEAD_30','LEAD_7','DUE','OVERDUE','PCT_80','PCT_90','PCT_100')),
  triggered_at timestamptz NOT NULL,
  detail jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','WORK_ORDER','CLOSED')),
  work_order_id text,
  closed_at timestamptz,
  UNIQUE (rule_id, source_mode, cycle_no, stage)  -- restart / duplicate report / re-evaluation never duplicates
);
CREATE INDEX maintenance_alerts_open ON maintenance_alerts(asset_id, source_mode, status);

-- ---------------------------------------------------------------------------------------------
-- B5 work orders, transitions, replacements; wire ropes.
-- ---------------------------------------------------------------------------------------------
CREATE TABLE maintenance_work_orders (
  id text PRIMARY KEY,
  code text NOT NULL UNIQUE,
  project_id text NOT NULL REFERENCES projects(id),
  asset_id text NOT NULL REFERENCES asset_devices(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  equipment_installation_id text,
  equipment_slot text,
  rule_id text REFERENCES maintenance_rules(id),
  alert_id text REFERENCES maintenance_alerts(id),
  cycle_no integer,
  wo_type text NOT NULL CHECK (wo_type IN ('PREVENTIVE','CALIBRATION','INSPECTION','CORRECTIVE','REPLACEMENT')),
  priority text NOT NULL DEFAULT 'MEDIUM' CHECK (priority IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','ASSIGNED','IN_PROGRESS','COMPLETED','CANCELLED')),
  title text NOT NULL,
  description text NOT NULL DEFAULT '',
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  assigned_to text,
  planned_at timestamptz,
  due_at timestamptz,
  started_at timestamptz,
  completed_at timestamptz,
  completed_by text,
  result text CHECK (result IN ('DONE','DONE_WITH_FINDINGS','NOT_DONE')),
  basis text,
  notes text NOT NULL DEFAULT '',
  version bigint NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (status <> 'COMPLETED' OR (completed_at IS NOT NULL AND completed_by IS NOT NULL AND result IS NOT NULL)),
  CHECK (status NOT IN ('ASSIGNED','IN_PROGRESS','COMPLETED') OR assigned_to IS NOT NULL),
  FOREIGN KEY (equipment_installation_id, asset_id) REFERENCES asset_equipment_installations (id, asset_id)
);
CREATE INDEX maintenance_work_orders_asset ON maintenance_work_orders(asset_id, source_mode, status);
-- One live work order per rule cycle and mode (repeated alerts / retries never open a second one).
CREATE UNIQUE INDEX maintenance_work_orders_one_per_cycle ON maintenance_work_orders(rule_id, source_mode, cycle_no) WHERE rule_id IS NOT NULL AND status <> 'CANCELLED';
ALTER TABLE maintenance_alerts ADD CONSTRAINT maintenance_alerts_work_order_fk FOREIGN KEY (work_order_id) REFERENCES maintenance_work_orders(id);

CREATE TABLE work_order_transitions (
  id bigserial PRIMARY KEY,
  work_order_id text NOT NULL REFERENCES maintenance_work_orders(id),
  from_status text NOT NULL, to_status text NOT NULL,
  actor text NOT NULL, occurred_at timestamptz NOT NULL DEFAULT now(),
  note text NOT NULL DEFAULT '', request_id text NOT NULL DEFAULT ''
);

-- REAL replacements create a new ledger installation; SIMULATION / TEST replacements never touch
-- the real ledger (the simulated instance is described in simulated_instance only).
CREATE TABLE equipment_replacements (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  equipment_slot text NOT NULL,
  old_equipment_installation_id text NOT NULL REFERENCES asset_equipment_installations(id),
  new_equipment_installation_id text REFERENCES asset_equipment_installations(id),
  simulated_instance jsonb,
  work_order_id text NOT NULL REFERENCES maintenance_work_orders(id),
  replaced_at timestamptz NOT NULL,
  replaced_by text NOT NULL,
  reason text NOT NULL,
  UNIQUE (work_order_id),
  CHECK ((source_mode = 'REAL') = (new_equipment_installation_id IS NOT NULL)),
  CHECK ((source_mode = 'REAL') = (simulated_instance IS NULL))
);

CREATE TABLE wire_rope_details (
  equipment_installation_id text PRIMARY KEY REFERENCES asset_equipment_installations(id),
  asset_id text NOT NULL REFERENCES asset_devices(id),
  construction_raw text NOT NULL,           -- the original specification text
  nominal_diameter_mm numeric CHECK (nominal_diameter_mm > 0),
  tensile_grade_mpa integer,
  standard_reference text NOT NULL DEFAULT 'GB/T 5972',
  rope_count integer CHECK (rope_count > 0),           -- NULL = 待确认
  rope_count_status text NOT NULL DEFAULT 'UNCONFIRMED' CHECK (rope_count_status IN ('UNCONFIRMED','CONFIRMED')),
  installed_at timestamptz,                 -- NULL = 待确认
  version bigint NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Findings and conclusion are always the inspector's; the system never derives a discard decision.
CREATE TABLE wire_rope_inspections (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  equipment_installation_id text NOT NULL REFERENCES asset_equipment_installations(id),
  source_mode text NOT NULL CHECK (source_mode IN ('REAL','SIMULATION','TEST')),
  work_order_id text REFERENCES maintenance_work_orders(id),
  inspection_type text NOT NULL CHECK (inspection_type IN ('DAILY','VISUAL','PERIODIC','MRT','OTHER')),
  inspected_at timestamptz NOT NULL,
  inspector text NOT NULL,
  measured_diameter_mm numeric CHECK (measured_diameter_mm > 0),
  broken_wires_visible integer CHECK (broken_wires_visible >= 0),
  wear text NOT NULL DEFAULT '', corrosion text NOT NULL DEFAULT '', deformation text NOT NULL DEFAULT '', lubrication text NOT NULL DEFAULT '',
  finding text NOT NULL DEFAULT '',
  conclusion text NOT NULL CHECK (conclusion IN ('CONTINUE','CONTINUE_MONITOR','REPLACE_RECOMMENDED','STOP_PENDING_REVIEW')),
  standard_reference text NOT NULL DEFAULT 'GB/T 5972',
  next_due_at timestamptz,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wire_rope_inspections_rope ON wire_rope_inspections(equipment_installation_id, inspected_at DESC);

-- ---------------------------------------------------------------------------------------------
-- Hard SIM / REAL isolation: a test_only (SIMULATION / TEST fixture) rule never produces a REAL
-- schedule, alert or work order, and a real rule never runs on SIMULATION / TEST data.
-- ---------------------------------------------------------------------------------------------
-- +goose StatementBegin
CREATE FUNCTION maintenance_mode_guard() RETURNS trigger AS $$
DECLARE
  is_test boolean;
BEGIN
  IF NEW.rule_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT test_only INTO is_test FROM maintenance_rules WHERE id = NEW.rule_id;
  IF is_test AND NEW.source_mode = 'REAL' THEN
    RAISE EXCEPTION 'test-only maintenance rule cannot act on REAL data';
  END IF;
  IF NOT is_test AND NEW.source_mode <> 'REAL' THEN
    RAISE EXCEPTION 'real maintenance rule cannot act on % data', NEW.source_mode;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER maintenance_schedules_mode_guard BEFORE INSERT OR UPDATE ON maintenance_schedules FOR EACH ROW EXECUTE FUNCTION maintenance_mode_guard();
CREATE TRIGGER maintenance_alerts_mode_guard BEFORE INSERT OR UPDATE ON maintenance_alerts FOR EACH ROW EXECUTE FUNCTION maintenance_mode_guard();
CREATE TRIGGER maintenance_work_orders_mode_guard BEFORE INSERT OR UPDATE ON maintenance_work_orders FOR EACH ROW EXECUTE FUNCTION maintenance_mode_guard();

-- +goose Down
-- Forward-only: metering history, maintenance records and the onboarding provenance are evidence.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'metering / maintenance migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
