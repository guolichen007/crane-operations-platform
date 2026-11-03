// Package asset owns the business asset master-data domain: Asset, Location,
// Manufacturer, Equipment Catalog, Equipment Installation and Relations.
// BUSINESS_OWNED fields are writable; DEPLOYMENT_OWNED and TELEMETRY_OWNED
// fields are never writable through the business API. Lifecycle changes retire
// or disable; nothing is hard-deleted.
package asset

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipment"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrVersionConflict = errors.New("version conflict")
	ErrNotFound        = errors.New("not found")
	ErrIdempotencyKey  = errors.New("idempotency key conflict")
	ErrRetired         = errors.New("retired asset cannot be modified")
	ErrActiveBinding   = errors.New("equipment has an active runtime binding")
)

func idempotency(ctx context.Context, tx *sql.Tx, actor, operation, key string, payload []byte) (json.RawMessage, bool, error) {
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	var existing sql.NullString
	var existingDigest string
	err := tx.QueryRowContext(ctx, `SELECT payload_sha256,result_json::text FROM idempotency_keys WHERE key=$1 AND actor=$2 AND operation=$3 AND expires_at>now()`, key, actor, operation).Scan(&existingDigest, &existing)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO idempotency_keys(key,actor,operation,payload_sha256,expires_at) VALUES($1,$2,$3,$4,now()+interval '24 hours')`, key, actor, operation, digest)
		return nil, false, err
	}
	if err != nil {
		return nil, false, err
	}
	if existingDigest != digest {
		return nil, false, ErrIdempotencyKey
	}
	if existing.Valid {
		return json.RawMessage(existing.String), true, nil
	}
	return nil, true, nil
}

func revision(ctx context.Context, tx *sql.Tx, projectID, assetID, entityType, entityID string, changedBy string, requestID string, changedFields []string, snapshot any) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	_, err = tx.ExecContext(ctx, `INSERT INTO entity_revisions(project_id,asset_id,entity_type,entity_id,recorded_at,effective_at,changed_by,request_id,changed_fields,snapshot_json,snapshot_sha256) VALUES($1,$2,$3,$4,now(),now(),$5,$6,$7,$8,$9)`,
		projectID, assetID, entityType, entityID, changedBy, requestID, changedFields, b, hex.EncodeToString(sum[:]))
	return err
}

// CreateAsset inserts a DRAFT asset.
func CreateAsset(ctx context.Context, db *sql.DB, in contract.AssetMasterData, actor, requestID, idemKey string) (contract.AssetMasterData, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	in.AssetStatus = "DRAFT"
	in.Version = 1
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if idemKey != "" {
		payload, _ := json.Marshal(in)
		existing, done, err := idempotency(ctx, tx, actor, "asset_create", idemKey, payload)
		if err != nil {
			return in, err
		}
		if done {
			if len(existing) > 0 {
				var out contract.AssetMasterData
				if err := json.Unmarshal(existing, &out); err != nil {
					return in, err
				}
				return out, nil
			}
			return in, ErrIdempotencyKey
		}
	}
	displayName := in.AssetCode
	if displayName == "" {
		displayName = in.ProjectId + "-asset"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_devices(id,project_id,asset_code,asset_type,asset_status,display_name,manufacturer_id,manufacturer_snapshot,brand_snapshot,model_snapshot,serial_number,manufacture_date,installation_date,commissioning_date,lifting_height_m,application_type,control_mode,special_equipment_code,use_registration_no,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13::date,$14::date,$15,$16,$17,$18,$19,$20)`,
		in.Id, in.ProjectId, in.AssetCode, in.AssetType, in.AssetStatus, displayName, in.ManufacturerId, in.ManufacturerSnapshot, in.BrandSnapshot, in.ModelSnapshot, in.SerialNumber, in.ManufactureDate, in.InstallationDate, in.CommissioningDate, in.LiftingHeightM, in.ApplicationType, in.ControlMode, in.SpecialEquipmentCode, in.UseRegistrationNo, in.Version)
	if err != nil {
		return in, err
	}
	if err := revision(ctx, tx, in.ProjectId, in.Id, "asset", in.Id, actor, requestID, []string{"created"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: in.ProjectId, Action: "asset_created", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

// UpdateAsset applies an optimistic-concurrency update to BUSINESS_OWNED fields.
func UpdateAsset(ctx context.Context, db *sql.DB, in contract.AssetMasterData, actor, requestID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT asset_status,version FROM asset_devices WHERE id=$1 FOR UPDATE`, in.Id).Scan(&status, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status == "RETIRED" {
		return ErrRetired
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_devices SET asset_code=$2,asset_type=$3,manufacturer_id=$4,manufacturer_snapshot=$5,brand_snapshot=$6,model_snapshot=$7,serial_number=$8,manufacture_date=$9,installation_date=$10,commissioning_date=$11,lifting_height_m=$12,application_type=$13,control_mode=$14,special_equipment_code=$15,use_registration_no=$16,version=version+1,updated_at=now() WHERE id=$1 AND version=$17`,
		in.Id, in.AssetCode, in.AssetType, in.ManufacturerId, in.ManufacturerSnapshot, in.BrandSnapshot, in.ModelSnapshot, in.SerialNumber, in.ManufactureDate, in.InstallationDate, in.CommissioningDate, in.LiftingHeightM, in.ApplicationType, in.ControlMode, in.SpecialEquipmentCode, in.UseRegistrationNo, version)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrVersionConflict
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, in.Id).Scan(&projectID); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID, in.Id, "asset", in.Id, actor, requestID, []string{"updated"}, in); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "asset_updated", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// RetireAsset retires an asset. A retired asset can never be reactivated, and
// retirement is refused while an equipment has an active runtime binding.
func RetireAsset(ctx context.Context, db *sql.DB, assetID, actor, requestID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT asset_status FROM asset_devices WHERE id=$1 FOR UPDATE`, assetID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status == "RETIRED" {
		return ErrRetired
	}
	var bindingCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM equipment_runtime_bindings b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id WHERE e.asset_id=$1 AND b.valid_to IS NULL`, assetID).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return ErrActiveBinding
	}
	if _, err := tx.ExecContext(ctx, `UPDATE asset_devices SET asset_status='RETIRED',version=version+1,updated_at=now() WHERE id=$1`, assetID); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: "", Action: "asset_retired", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateLocation inserts a location. code may be empty (unnumbered location).
func CreateLocation(ctx context.Context, db *sql.DB, in contract.AssetLocation, actor, requestID string) (contract.AssetLocation, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if in.Code == "" {
		in.Code = ""
	}
	var code any = in.Code
	if in.Code == "" {
		code = nil
	}
	in.Version = 1
	_, err := db.ExecContext(ctx, `INSERT INTO asset_locations(id,project_id,parent_id,kind,code,name,address,province,city,county,longitude,latitude,coordinate_system,coordinate_source,status,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		in.Id, in.ProjectId, in.ParentId, in.Kind, code, in.Name, in.Province, in.City, in.County, in.Longitude, in.Latitude, in.CoordinateSystem, in.CoordinateSource, in.Status, in.Version)
	if err != nil {
		return in, err
	}
	return in, nil
}

// DisableLocation disables a location rather than deleting it.
func DisableLocation(ctx context.Context, db *sql.DB, locationID, actor, requestID string) error {
	result, err := db.ExecContext(ctx, `UPDATE asset_locations SET status='DISABLED',version=version+1,updated_at=now() WHERE id=$1`, locationID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrNotFound
	}
	return auditlog.Write(ctx, db, auditlog.Event{Actor: actor, Action: "location_disabled", Result: "success", RequestID: requestID})
}

// CreateEquipment installs an equipment instance on an asset, validating the
// type-specific specification against the versioned schema.
func CreateEquipment(ctx context.Context, db *sql.DB, in contract.EquipmentInstallation, spec json.RawMessage, actor, requestID string) (contract.EquipmentInstallation, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if err := equipment.ValidateSpec(in.TypeCode, "1", spec); err != nil {
		return in, fmt.Errorf("equipment spec: %w", err)
	}
	in.Version = 1
	_, err := db.ExecContext(ctx, `INSERT INTO asset_equipment_installations(id,asset_id,equipment_code,type_code,catalog_item_id,manufacturer_snapshot,brand_snapshot,model_snapshot,serial_number,semantic_slot,position_label,criticality,administrative_state,valid_from,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now(),$14)`,
		in.Id, in.AssetId, in.EquipmentCode, in.TypeCode, in.CatalogItemId, "", "", "", in.SerialNumber, in.SemanticSlot, in.PositionLabel, in.Criticality, in.AdministrativeState, in.Version)
	if err != nil {
		return in, err
	}
	return in, nil
}

// RemoveEquipment closes an equipment installation's validity interval; it never
// hard-deletes and is refused while an active runtime binding exists.
func RemoveEquipment(ctx context.Context, db *sql.DB, equipmentID, actor, requestID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bindingCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM equipment_runtime_bindings WHERE equipment_installation_id=$1 AND valid_to IS NULL`, equipmentID).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return ErrActiveBinding
	}
	if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment_installations SET valid_to=now(),removed_at=now(),administrative_state='OUT_OF_SERVICE',version=version+1,updated_at=now() WHERE id=$1`, equipmentID); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "equipment_removed", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}
