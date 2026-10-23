-- +goose Up
-- NULL lifecycle preserves unregistered legacy fixtures, outside the inventory API.
ALTER TABLE projects ADD COLUMN lifecycle text CHECK (lifecycle IN ('PLANNED','PRE_DEVELOPMENT','COMPLETED')),
 ADD COLUMN province text NOT NULL DEFAULT '', ADD COLUMN city text NOT NULL DEFAULT '',
 ADD COLUMN county text NOT NULL DEFAULT '', ADD COLUMN location_label text NOT NULL DEFAULT '',
 ADD COLUMN admin_code integer CHECK (admin_code BETWEEN 100000 AND 999999),
 ADD COLUMN schedule_text text NOT NULL DEFAULT '', ADD COLUMN notes text NOT NULL DEFAULT '',
 ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE TABLE asset_devices (
 id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), display_name text NOT NULL,
 crane_model text, rated_capacity_t double precision CHECK (rated_capacity_t >= 0),
 span_m double precision CHECK (span_m >= 0), work_class text, grab_type text,
 lifecycle text NOT NULL CHECK (lifecycle IN ('PLANNED','PRE_DEVELOPMENT','COMPLETED')),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,project_id)
);
ALTER TABLE devices ADD COLUMN asset_device_id text,
 ADD COLUMN source_mode text NOT NULL DEFAULT 'TEST' CHECK (source_mode IN ('SIMULATION','REAL','TEST')),
 ADD CONSTRAINT device_asset_project FOREIGN KEY(asset_device_id,project_id) REFERENCES asset_devices(id,project_id);
UPDATE devices SET source_mode='SIMULATION' WHERE id ~ '^SIM-[0-9]{3}$';
CREATE UNIQUE INDEX device_asset_source ON devices(asset_device_id,source_mode) WHERE asset_device_id IS NOT NULL;
-- +goose Down
DROP INDEX device_asset_source;
ALTER TABLE devices DROP CONSTRAINT device_asset_project, DROP COLUMN asset_device_id, DROP COLUMN source_mode;
DROP TABLE asset_devices;
ALTER TABLE projects DROP COLUMN lifecycle, DROP COLUMN province, DROP COLUMN city, DROP COLUMN county,
 DROP COLUMN location_label, DROP COLUMN admin_code, DROP COLUMN schedule_text, DROP COLUMN notes, DROP COLUMN updated_at;
