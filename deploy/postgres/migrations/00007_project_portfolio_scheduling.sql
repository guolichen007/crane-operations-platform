-- +goose Up
-- R2 B2 — Project Portfolio / Intake / Scheduling / Resource Conflict foundation.
-- 00001..00006 are frozen; all B2 schema lands here. Time range semantics are
-- half-open [start_at, end_at), enforced by CHECK (start_at < end_at).

-- ---------------------------------------------------------------------------
-- Portfolio scope & grant. A narrow grant; it never implies project asset access.
-- ---------------------------------------------------------------------------
CREATE TABLE portfolio_scopes (
  id text PRIMARY KEY,
  tenant_id text NOT NULL REFERENCES tenants(id),
  code text NOT NULL,
  name text NOT NULL,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, code)
);

CREATE TABLE portfolio_memberships (
  user_id text NOT NULL REFERENCES users(id),
  portfolio_scope_id text NOT NULL REFERENCES portfolio_scopes(id),
  role text NOT NULL CHECK (role IN ('portfolio_planner','portfolio_manager')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(user_id, portfolio_scope_id)
);
CREATE INDEX portfolio_memberships_scope ON portfolio_memberships(portfolio_scope_id, user_id);

-- ---------------------------------------------------------------------------
-- Project Intake: a request becomes a Formal Project only after APPROVED.
-- ---------------------------------------------------------------------------
CREATE TABLE project_intakes (
  id text PRIMARY KEY,
  portfolio_scope_id text NOT NULL REFERENCES portfolio_scopes(id),
  tenant_id text NOT NULL REFERENCES tenants(id),
  intake_code text NOT NULL,
  project_name text NOT NULL,
  customer_name text,
  project_type text NOT NULL,
  priority text NOT NULL DEFAULT 'MEDIUM' CHECK (priority IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  risk_level text NOT NULL DEFAULT 'MEDIUM' CHECK (risk_level IN ('LOW','MEDIUM','HIGH')),
  scope_summary text NOT NULL DEFAULT '',
  site_reference text,
  registered_at timestamptz NOT NULL DEFAULT now(),
  registered_by text NOT NULL DEFAULT '',
  requested_start timestamptz,
  requested_handover timestamptz,
  planned_start timestamptz,
  planned_end timestamptz,
  status text NOT NULL DEFAULT 'REGISTERED' CHECK (status IN ('REGISTERED','REVIEW','PLANNING','APPROVED','REJECTED','CANCELLED')),
  project_manager_user_id text,
  technical_lead_user_id text,
  site_lead_user_id text,
  formal_project_id text,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(portfolio_scope_id, intake_code),
  CHECK (planned_end IS NULL OR planned_start IS NULL OR planned_end > planned_start),
  CHECK (requested_handover IS NULL OR requested_start IS NULL OR requested_handover > requested_start)
);
CREATE UNIQUE INDEX project_intakes_formal_project ON project_intakes(formal_project_id) WHERE formal_project_id IS NOT NULL;
CREATE INDEX project_intakes_portfolio_status ON project_intakes(portfolio_scope_id, status);

CREATE TABLE project_intake_transitions (
  id bigserial PRIMARY KEY,
  intake_id text NOT NULL REFERENCES project_intakes(id),
  from_status text NOT NULL,
  to_status text NOT NULL,
  actor text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL DEFAULT now(),
  reason text NOT NULL DEFAULT '',
  request_id text NOT NULL DEFAULT ''
);
CREATE INDEX project_intake_transitions_intake ON project_intake_transitions(intake_id, occurred_at DESC);

-- ---------------------------------------------------------------------------
-- Formal Project lifecycle phase (additive). Legacy projects stay NULL; never
-- fabricate a phase for a pre-existing project.
-- ---------------------------------------------------------------------------
ALTER TABLE projects ADD COLUMN project_phase text CHECK (project_phase IN ('REGISTERED','PLANNING','SURVEY','DESIGN','PREPARATION','READY_FOR_SITE','IN_EXECUTION','COMMISSIONING','TRIAL_RUN','ACCEPTANCE','HANDED_OVER','WARRANTY','CLOSED','ON_HOLD','CANCELLED'));

-- ---------------------------------------------------------------------------
-- Milestones & Activities.
-- ---------------------------------------------------------------------------
CREATE TABLE project_milestones (
  id text PRIMARY KEY,
  project_id text NOT NULL REFERENCES projects(id),
  template_code text,
  name text NOT NULL,
  planned_at timestamptz,
  actual_at timestamptz,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','IN_PROGRESS','COMPLETED','AT_RISK','BLOCKED')),
  blocking boolean NOT NULL DEFAULT false,
  sort_order integer NOT NULL DEFAULT 0,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX project_milestones_project ON project_milestones(project_id, sort_order);

CREATE TABLE project_activities (
  id text PRIMARY KEY,
  project_id text NOT NULL REFERENCES projects(id),
  milestone_id text REFERENCES project_milestones(id),
  name text NOT NULL,
  planned_start timestamptz,
  planned_end timestamptz,
  actual_start timestamptz,
  actual_end timestamptz,
  status text NOT NULL DEFAULT 'PLANNED' CHECK (status IN ('PLANNED','IN_PROGRESS','COMPLETED','BLOCKED','CANCELLED')),
  percent_complete integer CHECK (percent_complete IS NULL OR (percent_complete >= 0 AND percent_complete <= 100)),
  owner_user_id text,
  schedule_item_id text,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (planned_end IS NULL OR planned_start IS NULL OR planned_end > planned_start)
);
CREATE INDEX project_activities_project ON project_activities(project_id);

-- ---------------------------------------------------------------------------
-- Scheduling kernel: unified schedule items.
-- ---------------------------------------------------------------------------
CREATE TABLE schedule_items (
  id text PRIMARY KEY,
  project_id text REFERENCES projects(id),
  source_type text NOT NULL CHECK (source_type IN ('PROJECT_ACTIVITY','WORK_ORDER','INSPECTION','CALIBRATION','COMMISSIONING','SITE_WINDOW','NETWORK_CUTOVER')),
  source_id text NOT NULL,
  name text NOT NULL,
  start_at timestamptz NOT NULL,
  end_at timestamptz NOT NULL,
  timezone text NOT NULL DEFAULT 'UTC',
  status text NOT NULL DEFAULT 'PLANNED' CHECK (status IN ('PLANNED','APPROVED','IN_PROGRESS','COMPLETED','CANCELLED')),
  priority text NOT NULL DEFAULT 'MEDIUM' CHECK (priority IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  exclusive_location_id text,
  baseline_id text,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (start_at < end_at)
);
CREATE INDEX schedule_items_time ON schedule_items(start_at, end_at);
CREATE INDEX schedule_items_project ON schedule_items(project_id);

-- ---------------------------------------------------------------------------
-- Resources. Not an HR system: a person resource references an identity user id
-- only; no sensitive HR data.
-- ---------------------------------------------------------------------------
CREATE TABLE resources (
  id text PRIMARY KEY,
  portfolio_scope_id text NOT NULL REFERENCES portfolio_scopes(id),
  resource_type text NOT NULL CHECK (resource_type IN ('PERSON','TEAM','SITE','WORKSHOP','BAY','RUNWAY','ASSET','TEST_EQUIPMENT','TOOL','VEHICLE','COMMISSIONING_SLOT','NETWORK_CUTOVER_SLOT','OTHER')),
  reference_id text,
  name text NOT NULL,
  capacity integer NOT NULL DEFAULT 1 CHECK (capacity > 0),
  exclusive boolean NOT NULL DEFAULT false,
  override_allowed boolean NOT NULL DEFAULT true,
  home_timezone text NOT NULL DEFAULT 'UTC',
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (NOT exclusive OR capacity = 1)
);
CREATE INDEX resources_portfolio ON resources(portfolio_scope_id);

CREATE TABLE resource_calendars (
  id text PRIMARY KEY,
  resource_id text NOT NULL REFERENCES resources(id),
  timezone text NOT NULL DEFAULT 'UTC',
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX resource_calendars_resource ON resource_calendars(resource_id);

CREATE TABLE resource_calendar_rules (
  id bigserial PRIMARY KEY,
  calendar_id text NOT NULL REFERENCES resource_calendars(id),
  weekday integer NOT NULL CHECK (weekday BETWEEN 0 AND 6),
  start_local time NOT NULL,
  end_local time NOT NULL,
  availability text NOT NULL CHECK (availability IN ('AVAILABLE','UNAVAILABLE')),
  CHECK (end_local > start_local)
);
CREATE INDEX resource_calendar_rules_calendar ON resource_calendar_rules(calendar_id);

CREATE TABLE resource_calendar_exceptions (
  id bigserial PRIMARY KEY,
  calendar_id text NOT NULL REFERENCES resource_calendars(id),
  start_at timestamptz NOT NULL,
  end_at timestamptz NOT NULL,
  kind text NOT NULL CHECK (kind IN ('AVAILABLE','UNAVAILABLE','HOLIDAY','SITE_CLOSED','MAINTENANCE','OTHER')),
  reason text NOT NULL DEFAULT '',
  CHECK (end_at > start_at)
);
CREATE INDEX resource_calendar_exceptions_calendar ON resource_calendar_exceptions(calendar_id, start_at, end_at);

-- ---------------------------------------------------------------------------
-- Reservations. A reservation range must lie within its schedule item range.
-- ---------------------------------------------------------------------------
CREATE TABLE resource_reservations (
  id text PRIMARY KEY,
  schedule_item_id text NOT NULL REFERENCES schedule_items(id),
  resource_id text NOT NULL REFERENCES resources(id),
  start_at timestamptz NOT NULL,
  end_at timestamptz NOT NULL,
  quantity integer NOT NULL CHECK (quantity > 0),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','CANCELLED')),
  created_by text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  version bigint NOT NULL DEFAULT 1,
  CHECK (start_at < end_at)
);
CREATE INDEX resource_reservations_resource_time ON resource_reservations(resource_id, start_at, end_at);
CREATE INDEX resource_reservations_item ON resource_reservations(schedule_item_id);

-- ---------------------------------------------------------------------------
-- Dependencies. FS is implemented; SS/FF/SF are reserved, not yet enforced.
-- ---------------------------------------------------------------------------
CREATE TABLE schedule_dependencies (
  id text PRIMARY KEY,
  predecessor_item_id text NOT NULL REFERENCES schedule_items(id),
  successor_item_id text NOT NULL REFERENCES schedule_items(id),
  dependency_type text NOT NULL DEFAULT 'FS' CHECK (dependency_type IN ('FS','SS','FF','SF')),
  lag_minutes integer NOT NULL DEFAULT 0,
  CHECK (predecessor_item_id <> successor_item_id)
);
CREATE INDEX schedule_dependencies_successor ON schedule_dependencies(successor_item_id);

-- ---------------------------------------------------------------------------
-- Conflicts. A stable fingerprint dedupes; reconciliation updates, never deletes.
-- ---------------------------------------------------------------------------
CREATE TABLE schedule_conflicts (
  id bigserial PRIMARY KEY,
  conflict_type text NOT NULL CHECK (conflict_type IN ('RESOURCE_OVERLAP','RESOURCE_OVER_CAPACITY','SITE_OVERLAP','RUNWAY_OVERLAP','ASSET_DOWNTIME_OVERLAP','DEPENDENCY_VIOLATION','CALENDAR_UNAVAILABLE','SITE_ACCESS_WINDOW','COMMISSIONING_CONFLICT','NETWORK_CUTOVER_CONFLICT')),
  severity text NOT NULL CHECK (severity IN ('WARNING','BLOCKING')),
  left_item_id text NOT NULL REFERENCES schedule_items(id),
  right_item_id text REFERENCES schedule_items(id),
  resource_id text REFERENCES resources(id),
  fingerprint text NOT NULL,
  detected_at timestamptz NOT NULL DEFAULT now(),
  details_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','RESOLVED','OVERRIDDEN')),
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(fingerprint)
);
CREATE INDEX schedule_conflicts_status ON schedule_conflicts(status, detected_at DESC);

CREATE TABLE schedule_conflict_overrides (
  id bigserial PRIMARY KEY,
  conflict_id bigint NOT NULL REFERENCES schedule_conflicts(id),
  reason text NOT NULL,
  overridden_by text NOT NULL,
  overridden_at timestamptz NOT NULL DEFAULT now(),
  request_id text NOT NULL DEFAULT '',
  CHECK (length(trim(reason)) >= 8)
);
CREATE INDEX schedule_conflict_overrides_conflict ON schedule_conflict_overrides(conflict_id);

-- ---------------------------------------------------------------------------
-- Baselines: immutable snapshot; adjustments create a new revision.
-- ---------------------------------------------------------------------------
CREATE TABLE schedule_baselines (
  id text PRIMARY KEY,
  portfolio_scope_id text NOT NULL REFERENCES portfolio_scopes(id),
  project_id text REFERENCES projects(id),
  name text NOT NULL,
  baseline_version integer NOT NULL DEFAULT 1,
  approved_by text NOT NULL DEFAULT '',
  approved_at timestamptz NOT NULL DEFAULT now(),
  snapshot_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  snapshot_sha256 text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedule_baselines_portfolio ON schedule_baselines(portfolio_scope_id);

-- +goose Down
-- Forward-only: portfolio/scheduling foundation references frozen B1 anchors.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'portfolio/scheduling migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
