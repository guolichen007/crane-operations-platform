-- +goose Up
-- R2 B3-R1 — asset business hardening.
-- 00001..00010 are frozen.

-- ---------------------------------------------------------------------------
-- Idempotency-Key composite identity. The same text key may be reused by
-- different actors or operations; (actor, operation, key) is the durable
-- identity. result_json already carries the canonical response; entity_id and
-- http_status record the persisted entity and outcome for durable replay.
-- ---------------------------------------------------------------------------
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (actor, operation, key);
ALTER TABLE idempotency_keys ADD COLUMN entity_id text;
ALTER TABLE idempotency_keys ADD COLUMN http_status integer;

-- ---------------------------------------------------------------------------
-- Cross-project reference DB guards (authoritative; the service layer is the
-- second line of defense). A project-scoped manufacturer/catalog can never be
-- referenced across a project boundary. Global (project_id IS NULL) references
-- remain read-only through the business HTTP layer.
-- ---------------------------------------------------------------------------

-- Asset -> Manufacturer: manufacturer global or same project as the asset.
-- +goose StatementBegin
CREATE FUNCTION asset_manufacturer_same_project() RETURNS trigger AS $$
BEGIN
  IF NEW.manufacturer_id IS NOT NULL THEN
    IF EXISTS (SELECT 1 FROM manufacturers m
               WHERE m.id = NEW.manufacturer_id
                 AND m.project_id IS NOT NULL
                 AND m.project_id IS DISTINCT FROM NEW.project_id) THEN
      RAISE EXCEPTION 'asset manufacturer crosses project boundary';
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER asset_manufacturer_same_project_trigger
  BEFORE INSERT OR UPDATE OF manufacturer_id, project_id ON asset_devices
  FOR EACH ROW EXECUTE FUNCTION asset_manufacturer_same_project();

-- Catalog -> Manufacturer: manufacturer global or same project as the catalog.
-- +goose StatementBegin
CREATE FUNCTION catalog_manufacturer_same_project() RETURNS trigger AS $$
BEGIN
  IF NEW.manufacturer_id IS NOT NULL THEN
    IF EXISTS (SELECT 1 FROM manufacturers m
               WHERE m.id = NEW.manufacturer_id
                 AND m.project_id IS NOT NULL
                 AND m.project_id IS DISTINCT FROM NEW.project_id) THEN
      RAISE EXCEPTION 'catalog manufacturer crosses project boundary';
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER catalog_manufacturer_same_project_trigger
  BEFORE INSERT OR UPDATE OF manufacturer_id, project_id ON equipment_catalog_items
  FOR EACH ROW EXECUTE FUNCTION catalog_manufacturer_same_project();

-- Equipment -> Catalog: catalog global or same project as the asset.
-- +goose StatementBegin
CREATE FUNCTION equipment_catalog_same_project() RETURNS trigger AS $$
BEGIN
  IF NEW.catalog_item_id IS NOT NULL THEN
    IF EXISTS (SELECT 1 FROM equipment_catalog_items c
               JOIN asset_devices a ON a.id = NEW.asset_id
               WHERE c.id = NEW.catalog_item_id
                 AND c.project_id IS NOT NULL
                 AND c.project_id IS DISTINCT FROM a.project_id) THEN
      RAISE EXCEPTION 'equipment catalog reference crosses project boundary';
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER equipment_catalog_same_project_trigger
  BEFORE INSERT OR UPDATE OF catalog_item_id, asset_id ON asset_equipment_installations
  FOR EACH ROW EXECUTE FUNCTION equipment_catalog_same_project();

-- ---------------------------------------------------------------------------
-- Server-side paging / filter / sort indexes for business list APIs.
-- ---------------------------------------------------------------------------
CREATE INDEX asset_devices_project_created ON asset_devices(project_id, created_at DESC);
CREATE INDEX manufacturers_project_name ON manufacturers(project_id, name);
CREATE INDEX equipment_catalog_items_project_name ON equipment_catalog_items(project_id, name);
CREATE INDEX asset_equipment_installations_asset_code ON asset_equipment_installations(asset_id, equipment_code);

-- +goose Down
-- Forward-only: asset business hardening is additive over frozen B3.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'asset business hardening is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
