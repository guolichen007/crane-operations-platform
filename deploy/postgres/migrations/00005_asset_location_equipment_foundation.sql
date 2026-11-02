-- +goose Up
-- R2 B1 — Asset / Location / Equipment foundation.
-- Additive-first: extends the existing asset_devices anchor; never rewrites identity/provenance.
-- type_code stays a stable text code with a code-owned registry (no PostgreSQL hard enum).

-- ---------------------------------------------------------------------------
-- Catalog anchors (no dependencies)
-- ---------------------------------------------------------------------------
CREATE TABLE equipment_types (
  type_code text PRIMARY KEY,
  display_name text NOT NULL,
  domain text NOT NULL CHECK (domain IN ('MECHANICAL','CONTROL','PERCEPTION','COMPUTE','NETWORK','POWER','SENSOR','OTHER')),
  spec_schema_version text NOT NULL DEFAULT '1',
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE manufacturers (
  id text PRIMARY KEY,
  project_id text REFERENCES projects(id),
  name text NOT NULL,
  short_name text NOT NULL DEFAULT '',
  country_region text,
  website text,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX manufacturers_project ON manufacturers(project_id);

-- ---------------------------------------------------------------------------
-- Asset master-data additive extension (existing asset_devices anchor).
-- manufacturer_id is a catalog reference to manufacturers; snapshots stay the
-- authoritative frozen data so a later catalog rename never rewrites history.
-- ---------------------------------------------------------------------------
ALTER TABLE asset_devices
  ADD COLUMN asset_code text,
  ADD COLUMN asset_type text NOT NULL DEFAULT 'CRANE',
  ADD COLUMN asset_status text NOT NULL DEFAULT 'ACTIVE' CHECK (asset_status IN ('DRAFT','ACTIVE','OUT_OF_SERVICE','RETIRED')),
  ADD COLUMN manufacturer_id text REFERENCES manufacturers(id),
  ADD COLUMN manufacturer_snapshot text NOT NULL DEFAULT '',
  ADD COLUMN brand_snapshot text NOT NULL DEFAULT '',
  ADD COLUMN model_snapshot text NOT NULL DEFAULT '',
  ADD COLUMN serial_number text,
  ADD COLUMN manufacture_date date,
  ADD COLUMN installation_date date,
  ADD COLUMN commissioning_date date,
  ADD COLUMN lifting_height_m double precision CHECK (lifting_height_m >= 0),
  ADD COLUMN application_type text,
  ADD COLUMN control_mode text,
  ADD COLUMN special_equipment_code text,
  ADD COLUMN use_registration_no text,
  ADD COLUMN latest_inspection_date date,
  ADD COLUMN inspection_valid_until date,
  ADD COLUMN version bigint NOT NULL DEFAULT 1;

-- asset_code is project-unique; NULL (unconfigured) is allowed any number of times.
CREATE UNIQUE INDEX asset_devices_project_code ON asset_devices(project_id, asset_code) WHERE asset_code IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Location hierarchy. A parent location must belong to the same project, so the
-- composite FK pins (parent_id, project_id) to the parent's (id, project_id).
-- ---------------------------------------------------------------------------
CREATE TABLE asset_locations (
  id text PRIMARY KEY,
  project_id text NOT NULL REFERENCES projects(id),
  parent_id text,
  kind text NOT NULL CHECK (kind IN ('SITE','PLANT','WORKSHOP','BAY','RUNWAY','OTHER')),
  code text NOT NULL DEFAULT '',
  name text NOT NULL,
  address text NOT NULL DEFAULT '',
  province text NOT NULL DEFAULT '',
  city text NOT NULL DEFAULT '',
  county text NOT NULL DEFAULT '',
  longitude double precision,
  latitude double precision,
  coordinate_system text NOT NULL DEFAULT 'UNKNOWN' CHECK (coordinate_system IN ('WGS84','GCJ02','LOCAL_SITE','UNKNOWN')),
  coordinate_source text NOT NULL DEFAULT 'MANUAL' CHECK (coordinate_source IN ('MANUAL','SURVEY','IMPORTED','SITE_CENTER')),
  accuracy_m double precision CHECK (accuracy_m >= 0),
  local_x_m double precision,
  local_y_m double precision,
  local_z_m double precision,
  orientation_deg double precision,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(project_id, code),
  UNIQUE(id, project_id)
);
CREATE INDEX asset_locations_project ON asset_locations(project_id);
ALTER TABLE asset_locations ADD CONSTRAINT asset_locations_parent_project_fk
  FOREIGN KEY (parent_id, project_id) REFERENCES asset_locations (id, project_id);

-- ---------------------------------------------------------------------------
-- Asset installation history (asset <-> location, one active at a time).
-- An asset must never be installed at a location of another project.
-- ---------------------------------------------------------------------------
CREATE TABLE asset_installations (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  location_id text NOT NULL REFERENCES asset_locations(id),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  local_x_m double precision,
  local_y_m double precision,
  local_z_m double precision,
  orientation_deg double precision,
  source text NOT NULL DEFAULT '',
  notes text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (valid_to IS NULL OR valid_to > valid_from)
);
CREATE INDEX asset_installations_asset ON asset_installations(asset_id, valid_from);
CREATE UNIQUE INDEX asset_installations_one_active ON asset_installations(asset_id) WHERE valid_to IS NULL;

-- Authoritative project-boundary guard: the asset and the location must share a project.
-- +goose StatementBegin
CREATE FUNCTION asset_installation_same_project() RETURNS trigger AS $$
BEGIN
  IF (SELECT a.project_id FROM asset_devices a WHERE a.id = NEW.asset_id)
      IS DISTINCT FROM
     (SELECT l.project_id FROM asset_locations l WHERE l.id = NEW.location_id) THEN
    RAISE EXCEPTION 'asset installation crosses project boundary';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER asset_installation_same_project_trigger
  BEFORE INSERT OR UPDATE OF asset_id, location_id ON asset_installations
  FOR EACH ROW EXECUTE FUNCTION asset_installation_same_project();

-- ---------------------------------------------------------------------------
-- Equipment catalog (type templates; never field serial/pose/runtime here)
-- ---------------------------------------------------------------------------
CREATE TABLE equipment_catalog_items (
  id text PRIMARY KEY,
  project_id text REFERENCES projects(id),
  name text NOT NULL,
  type_code text NOT NULL REFERENCES equipment_types(type_code),
  manufacturer_id text REFERENCES manufacturers(id),
  brand text NOT NULL DEFAULT '',
  model text NOT NULL DEFAULT '',
  product_code text,
  spec_schema_version text NOT NULL DEFAULT '1',
  specifications jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX equipment_catalog_items_project ON equipment_catalog_items(project_id);
CREATE INDEX equipment_catalog_items_type ON equipment_catalog_items(type_code);

-- ---------------------------------------------------------------------------
-- Asset equipment installation (the maintainable, replaceable equipment instance).
-- equipment_code is a business number and stays permanently unique per asset:
-- an installation instance is a historical record and is never deleted, so the
-- code cannot be reused without making history ambiguous. semantic_slot is the
-- reusable slot; the code is the immutable instance label.
-- ---------------------------------------------------------------------------
CREATE TABLE asset_equipment_installations (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  equipment_code text NOT NULL,
  type_code text NOT NULL REFERENCES equipment_types(type_code),
  catalog_item_id text REFERENCES equipment_catalog_items(id),
  manufacturer_snapshot text NOT NULL DEFAULT '',
  brand_snapshot text NOT NULL DEFAULT '',
  model_snapshot text NOT NULL DEFAULT '',
  serial_number text,
  batch_number text,
  asset_tag text,
  parent_semantic_slot text,
  semantic_slot text,
  position_label text,
  criticality text NOT NULL DEFAULT 'MEDIUM' CHECK (criticality IN ('LOW','MEDIUM','HIGH','SAFETY_CRITICAL')),
  administrative_state text NOT NULL DEFAULT 'IN_SERVICE' CHECK (administrative_state IN ('IN_SERVICE','MAINTENANCE','OUT_OF_SERVICE','FAULT_REPORTED')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  installed_at timestamptz,
  commissioned_at timestamptz,
  removed_at timestamptz,
  warranty_until timestamptz,
  supplier_id text,
  coordinate_frame text,
  x_m double precision,
  y_m double precision,
  z_m double precision,
  roll_deg double precision,
  pitch_deg double precision,
  yaw_deg double precision,
  pose_source text NOT NULL DEFAULT 'MANUAL' CHECK (pose_source IN ('MANUAL','CAD','SURVEY','CALIBRATED')),
  pose_accuracy_m double precision CHECK (pose_accuracy_m >= 0),
  notes text NOT NULL DEFAULT '',
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(asset_id, equipment_code),
  UNIQUE(id, asset_id),
  CHECK (valid_to IS NULL OR valid_to > valid_from)
);
CREATE INDEX asset_equipment_installations_asset ON asset_equipment_installations(asset_id, valid_from);

-- ---------------------------------------------------------------------------
-- Equipment relations. Both ends must belong to the same asset: the composite
-- FK pins (installation_id, asset_id) to the installation's (id, asset_id).
-- ---------------------------------------------------------------------------
CREATE TABLE equipment_relations (
  id text PRIMARY KEY,
  asset_id text NOT NULL REFERENCES asset_devices(id),
  from_equipment_installation_id text NOT NULL,
  to_equipment_installation_id text NOT NULL,
  relation_type text NOT NULL CHECK (relation_type IN ('CONTAINS','MOUNTED_ON','DRIVES','CONTROLS','SENSES','MONITORS','POWERED_BY','NETWORKED_TO','PROTECTS','BACKS_UP')),
  valid_from timestamptz NOT NULL,
  valid_to timestamptz,
  source text NOT NULL DEFAULT '',
  notes text NOT NULL DEFAULT '',
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  CHECK (from_equipment_installation_id <> to_equipment_installation_id),
  FOREIGN KEY (from_equipment_installation_id, asset_id) REFERENCES asset_equipment_installations (id, asset_id),
  FOREIGN KEY (to_equipment_installation_id, asset_id) REFERENCES asset_equipment_installations (id, asset_id)
);
CREATE INDEX equipment_relations_asset ON equipment_relations(asset_id, valid_from);

-- +goose Down
-- Forward-only: B1 foundation tables are additive and reference the R2 asset anchor.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'asset/location/equipment foundation migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
