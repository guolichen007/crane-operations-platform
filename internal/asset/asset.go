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
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipment"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrVersionConflict  = errors.New("version conflict")
	ErrNotFound         = errors.New("not found")
	ErrIdempotencyKey   = errors.New("idempotency key conflict")
	ErrRetired          = errors.New("retired asset cannot be modified")
	ErrActiveBinding    = errors.New("equipment has an active runtime binding")
	ErrCrossProject     = errors.New("reference crosses project boundary")
	ErrGlobalReadOnly   = errors.New("global record is read-only through the business API")
	ErrDisabledLocation = errors.New("disabled location cannot be an installation target")
	ErrTerminalStatus   = errors.New("asset status is terminal")
	ErrExpectedVersion  = errors.New("expected version is required")
)

const maxPageSize = 100

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// revision writes a canonical post-mutation snapshot. projectID must be non-empty
// for any entity anchored to a project; global (project_id IS NULL) entities
// have no project anchor and are never mutable through the business API.
func revision(ctx context.Context, tx *sql.Tx, projectID, assetID, entityType, entityID, changedBy, requestID string, changedFields []string, snapshot any) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	_, err = tx.ExecContext(ctx, `INSERT INTO entity_revisions(project_id,asset_id,entity_type,entity_id,revision,recorded_at,effective_at,changed_by,request_id,changed_fields,snapshot_json,snapshot_sha256) VALUES($1,NULLIF($2,''),$3,$4,(SELECT COALESCE(MAX(revision),0)+1 FROM entity_revisions WHERE entity_type=$3 AND entity_id=$4),now(),now(),$5,$6,$7,$8,$9)`,
		projectID, assetID, entityType, entityID, changedBy, requestID, changedFields, b, hex.EncodeToString(sum[:]))
	return err
}

// ---------------------------------------------------------------------------
// Idempotency-Key persistence. (actor, operation, key) is the durable identity;
// the payload digest is the canonical client request, never a server-mutated
// structure. The INSERT ... ON CONFLICT DO NOTHING serializes concurrent
// duplicates: exactly one caller sees RowsAffected==1 and becomes the winner.
// ---------------------------------------------------------------------------
func idempotencyClaim(ctx context.Context, tx *sql.Tx, actor, operation, key string, payload []byte) (json.RawMessage, bool, error) {
	digest := sha256hex(payload)
	result, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(actor,operation,key,payload_sha256,expires_at) VALUES($1,$2,$3,$4,now()+interval '24 hours') ON CONFLICT (actor,operation,key) DO NOTHING`, actor, operation, key, digest)
	if err != nil {
		return nil, false, err
	}
	if n, err := result.RowsAffected(); err == nil && n == 1 {
		return nil, false, nil
	}
	var existingDigest string
	var existing sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT payload_sha256,result_json::text FROM idempotency_keys WHERE actor=$1 AND operation=$2 AND key=$3 AND expires_at>now()`, actor, operation, key).Scan(&existingDigest, &existing); err != nil {
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

func idempotencyPersist(ctx context.Context, tx *sql.Tx, actor, operation, key, entityID string, resultJSON []byte) error {
	_, err := tx.ExecContext(ctx, `UPDATE idempotency_keys SET result_json=$4::jsonb,entity_id=$5,http_status=201,expires_at=now()+interval '24 hours' WHERE actor=$1 AND operation=$2 AND key=$3`, actor, operation, key, string(resultJSON), entityID)
	return err
}

// canonicalAssetRequest strips server-managed fields so a client retry with the
// same business payload hashes identically regardless of the generated id.
func canonicalAssetRequest(in contract.AssetMasterData) []byte {
	c := in
	c.Id = ""
	c.AssetStatus = ""
	c.Version = 0
	b, _ := json.Marshal(c)
	return b
}

// CreateAsset inserts a DRAFT asset. Idempotency is evaluated on the canonical
// client request before any server-side id/status/version assignment.
func CreateAsset(ctx context.Context, db *sql.DB, in contract.AssetMasterData, actor, requestID, idemKey string) (contract.AssetMasterData, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if idemKey != "" {
		existing, replay, err := idempotencyClaim(ctx, tx, actor, "asset_create", idemKey, canonicalAssetRequest(in))
		if err != nil {
			return in, err
		}
		if replay {
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
	in.Id = platform.ID()
	in.AssetStatus = "DRAFT"
	in.Version = 1
	displayName := ""
	if in.AssetCode != nil {
		displayName = strings.TrimSpace(*in.AssetCode)
	}
	if displayName == "" {
		displayName = in.ProjectId + "-asset"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_devices(id,project_id,asset_code,asset_type,asset_status,display_name,lifecycle,manufacturer_id,manufacturer_snapshot,brand_snapshot,model_snapshot,serial_number,manufacture_date,installation_date,commissioning_date,lifting_height_m,application_type,control_mode,special_equipment_code,use_registration_no,version) VALUES($1,$2,$3,$4,$5,$6,'PLANNED',$7,$8,$9,$10,$11,$12::date,$13::date,$14::date,$15,$16,$17,$18,$19,$20)`,
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
	if idemKey != "" {
		resultJSON, _ := json.Marshal(in)
		if err := idempotencyPersist(ctx, tx, actor, "asset_create", idemKey, in.Id, resultJSON); err != nil {
			return in, err
		}
	}
	return in, tx.Commit()
}

// assetColumns is the canonical asset_devices SELECT list used by readers.
const assetColumns = `id,project_id,asset_code,asset_type,asset_status,manufacturer_id,manufacturer_snapshot,brand_snapshot,model_snapshot,serial_number,manufacture_date,installation_date,commissioning_date,lifting_height_m,application_type,control_mode,special_equipment_code,use_registration_no,version`

func scanAsset(row interface{ Scan(...any) error }) (contract.AssetMasterData, error) {
	var v contract.AssetMasterData
	err := row.Scan(&v.Id, &v.ProjectId, &v.AssetCode, &v.AssetType, &v.AssetStatus, &v.ManufacturerId, &v.ManufacturerSnapshot, &v.BrandSnapshot, &v.ModelSnapshot, &v.SerialNumber, &v.ManufactureDate, &v.InstallationDate, &v.CommissioningDate, &v.LiftingHeightM, &v.ApplicationType, &v.ControlMode, &v.SpecialEquipmentCode, &v.UseRegistrationNo, &v.Version)
	return v, err
}

// GetAsset returns an asset by id, hiding a missing row as ErrNotFound.
func GetAsset(ctx context.Context, db *sql.DB, id string) (contract.AssetMasterData, error) {
	v, err := scanAsset(db.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM asset_devices WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// ListOptions carries server-side paging / filter / sort / search controls.
// Filter fields are allowlisted column conditions, never raw client SQL.
type ListOptions struct {
	Limit              int
	Offset             int
	Sort               string
	Q                  string
	AssetStatus        string
	AssetType          string
	ManufacturerId     string
	LocationKind       string
	LocationStatus     string
	LocationParent     string
	EquipmentType      string
	EquipmentState     string
	Criticality        string
	CatalogType        string
	CatalogStatus      string
	CatalogMfg         string
	ManufacturerStatus string
}

func (o ListOptions) normalized() (int, int) {
	limit, offset := o.Limit, o.Offset
	if limit <= 0 || limit > maxPageSize {
		limit = maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// assetFilters appends allowlisted asset filter conditions to the WHERE clause.
func assetFilters(where string, args []any, o ListOptions) (string, []any) {
	if o.AssetStatus != "" {
		args = append(args, o.AssetStatus)
		where += fmt.Sprintf(" AND asset_status=$%d", len(args))
	}
	if o.AssetType != "" {
		args = append(args, o.AssetType)
		where += fmt.Sprintf(" AND asset_type=$%d", len(args))
	}
	if o.ManufacturerId != "" {
		args = append(args, o.ManufacturerId)
		where += fmt.Sprintf(" AND manufacturer_id=$%d", len(args))
	}
	return where, args
}

// assetSortColumn maps an allowlisted sort key to a column; any other key fails
// closed. Client sort strings are never concatenated into SQL.
func assetSortColumn(sort string) (string, error) {
	dir := "ASC"
	if strings.HasSuffix(sort, ":desc") {
		dir, sort = "DESC", strings.TrimSuffix(sort, ":desc")
	} else if strings.HasSuffix(sort, ":asc") {
		sort = strings.TrimSuffix(sort, ":asc")
	}
	col := ""
	switch sort {
	case "", "createdAt":
		col = "created_at"
	case "displayName":
		col = "display_name"
	case "assetCode":
		col = "asset_code"
	case "assetType":
		col = "asset_type"
	case "assetStatus":
		col = "asset_status"
	case "version":
		col = "version"
	default:
		return "", fmt.Errorf("unsupported sort field %q", sort)
	}
	return col + " " + dir, nil
}

// ListAssets returns project-scoped assets with server-side paging, search and
// allowlisted sort. q is an ILIKE search over display_name / asset_code.
func ListAssets(ctx context.Context, db *sql.DB, projectID string, opts ListOptions) ([]contract.AssetMasterData, int, error) {
	limit, offset := opts.normalized()
	order, err := assetSortColumn(opts.Sort)
	if err != nil {
		return nil, 0, err
	}
	where := "project_id=$1"
	args := []any{projectID}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND (display_name ILIKE $%d OR asset_code ILIKE $%d)", len(args), len(args))
	}
	where, args = assetFilters(where, args, opts)
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_devices WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+assetColumns+` FROM asset_devices WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.AssetMasterData{}
	for rows.Next() {
		v, err := scanAsset(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

// UpdateAsset applies an optimistic-concurrency update to BUSINESS_OWNED fields
// and allows non-terminal asset_status transitions (DRAFT/ACTIVE/OUT_OF_SERVICE).
// RETIRED is terminal: a retired asset can never be patched back to ACTIVE.
func UpdateAsset(ctx context.Context, db *sql.DB, in contract.AssetMasterData, expectedVersion int64, actor, requestID string) (contract.AssetMasterData, error) {
	if expectedVersion <= 0 {
		return in, ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var status, projectID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT asset_status,version,project_id FROM asset_devices WHERE id=$1 FOR UPDATE`, in.Id).Scan(&status, &version, &projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrNotFound
		}
		return in, err
	}
	if status == "RETIRED" {
		return in, ErrRetired
	}
	if version != expectedVersion {
		return in, ErrVersionConflict
	}
	newStatus := status
	if in.AssetStatus != "" && in.AssetStatus != status {
		switch in.AssetStatus {
		case "DRAFT", "ACTIVE", "OUT_OF_SERVICE":
			newStatus = in.AssetStatus
		case "RETIRED":
			return in, ErrTerminalStatus
		default:
			return in, fmt.Errorf("invalid asset status %q", in.AssetStatus)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_devices SET asset_code=$2,asset_type=$3,manufacturer_id=$4,manufacturer_snapshot=$5,brand_snapshot=$6,model_snapshot=$7,serial_number=$8,manufacture_date=$9,installation_date=$10,commissioning_date=$11,lifting_height_m=$12,application_type=$13,control_mode=$14,special_equipment_code=$15,use_registration_no=$16,asset_status=$17,version=version+1,updated_at=now() WHERE id=$1 AND version=$18`,
		in.Id, in.AssetCode, in.AssetType, in.ManufacturerId, in.ManufacturerSnapshot, in.BrandSnapshot, in.ModelSnapshot, in.SerialNumber, in.ManufactureDate, in.InstallationDate, in.CommissioningDate, in.LiftingHeightM, in.ApplicationType, in.ControlMode, in.SpecialEquipmentCode, in.UseRegistrationNo, newStatus, expectedVersion)
	if err != nil {
		return in, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return in, ErrVersionConflict
	}
	in.Version = version + 1
	in.AssetStatus = newStatus
	in.ProjectId = projectID
	if err := revision(ctx, tx, projectID, in.Id, "asset", in.Id, actor, requestID, []string{"updated"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "asset_updated", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

// RetireAsset retires an asset. A retired asset can never be reactivated, and
// retirement is refused while an equipment has an active runtime binding.
func RetireAsset(ctx context.Context, db *sql.DB, assetID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, projectID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT asset_status,version,project_id FROM asset_devices WHERE id=$1 FOR UPDATE`, assetID).Scan(&status, &version, &projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status == "RETIRED" {
		return ErrRetired
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	var bindingCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM equipment_runtime_bindings b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id WHERE e.asset_id=$1 AND b.valid_to IS NULL`, assetID).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return ErrActiveBinding
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_devices SET asset_status='RETIRED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, assetID, expectedVersion)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrVersionConflict
	}
	var snapshot contract.AssetMasterData
	if err := tx.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM asset_devices WHERE id=$1`, assetID).Scan(&snapshot.Id, &snapshot.ProjectId, &snapshot.AssetCode, &snapshot.AssetType, &snapshot.AssetStatus, &snapshot.ManufacturerId, &snapshot.ManufacturerSnapshot, &snapshot.BrandSnapshot, &snapshot.ModelSnapshot, &snapshot.SerialNumber, &snapshot.ManufactureDate, &snapshot.InstallationDate, &snapshot.CommissioningDate, &snapshot.LiftingHeightM, &snapshot.ApplicationType, &snapshot.ControlMode, &snapshot.SpecialEquipmentCode, &snapshot.UseRegistrationNo, &snapshot.Version); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID, assetID, "asset", assetID, actor, requestID, []string{"retired"}, snapshot); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "asset_retired", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveAsset closes the current active installation and creates a new one at a
// target location in the same project. Historical installations are preserved.
func MoveAsset(ctx context.Context, db *sql.DB, assetID, locationID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var assetProject, status string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,asset_status,version FROM asset_devices WHERE id=$1 FOR UPDATE`, assetID).Scan(&assetProject, &status, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status == "RETIRED" {
		return ErrRetired
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	var locProject, locStatus string
	if err := tx.QueryRowContext(ctx, `SELECT project_id,status FROM asset_locations WHERE id=$1 FOR UPDATE`, locationID).Scan(&locProject, &locStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if locProject != assetProject {
		return ErrCrossProject
	}
	if locStatus == "DISABLED" {
		return ErrDisabledLocation
	}
	if _, err := tx.ExecContext(ctx, `UPDATE asset_installations SET valid_to=now() WHERE asset_id=$1 AND valid_to IS NULL`, assetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_installations(id,asset_id,location_id,valid_from,source,notes) VALUES($1,$2,$3,now(),'BUSINESS','moved')`, platform.ID(), assetID, locationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE asset_devices SET version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, assetID, expectedVersion); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: assetProject, Action: "asset_moved", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Location lifecycle.
// ---------------------------------------------------------------------------
const locationColumns = `id,project_id,parent_id,kind,code,name,address,province,city,county,longitude,latitude,coordinate_system,coordinate_source,status,version`

func scanLocation(row interface{ Scan(...any) error }) (contract.AssetLocation, error) {
	var v contract.AssetLocation
	err := row.Scan(&v.Id, &v.ProjectId, &v.ParentId, &v.Kind, &v.Code, &v.Name, &v.Address, &v.Province, &v.City, &v.County, &v.Longitude, &v.Latitude, &v.CoordinateSystem, &v.CoordinateSource, &v.Status, &v.Version)
	return v, err
}

// CreateLocation inserts a location. code may be empty (unnumbered location);
// blank code is normalized to NULL so the UNIQUE(project_id, code) permits
// multiple unnumbered locations.
func CreateLocation(ctx context.Context, db *sql.DB, in contract.AssetLocation, actor, requestID string) (contract.AssetLocation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if in.Id == "" {
		in.Id = platform.ID()
	}
	var code any
	if in.Code != nil && strings.TrimSpace(*in.Code) != "" {
		code = strings.TrimSpace(*in.Code)
		in.Code = strPtr(code.(string))
	} else {
		code = nil
	}
	in.Version = 1
	if in.Status == "" {
		in.Status = "ACTIVE"
	}
	if in.CoordinateSystem == "" {
		in.CoordinateSystem = "UNKNOWN"
	}
	if in.CoordinateSource == "" {
		in.CoordinateSource = "MANUAL"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_locations(id,project_id,parent_id,kind,code,name,address,province,city,county,longitude,latitude,coordinate_system,coordinate_source,status,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		in.Id, in.ProjectId, in.ParentId, in.Kind, code, in.Name, in.Address, in.Province, in.City, in.County, in.Longitude, in.Latitude, in.CoordinateSystem, in.CoordinateSource, in.Status, in.Version)
	if err != nil {
		return in, err
	}
	if err := revision(ctx, tx, in.ProjectId, "", "location", in.Id, actor, requestID, []string{"created"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: in.ProjectId, Action: "location_created", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func GetLocation(ctx context.Context, db *sql.DB, id string) (contract.AssetLocation, error) {
	v, err := scanLocation(db.QueryRowContext(ctx, `SELECT `+locationColumns+` FROM asset_locations WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func locationSortColumn(sort string) (string, error) {
	dir := "ASC"
	if strings.HasSuffix(sort, ":desc") {
		dir, sort = "DESC", strings.TrimSuffix(sort, ":desc")
	} else if strings.HasSuffix(sort, ":asc") {
		sort = strings.TrimSuffix(sort, ":asc")
	}
	col := ""
	switch sort {
	case "", "createdAt":
		col = "created_at"
	case "name":
		col = "name"
	case "code":
		col = "code"
	case "kind":
		col = "kind"
	case "status":
		col = "status"
	default:
		return "", fmt.Errorf("unsupported sort field %q", sort)
	}
	return col + " " + dir, nil
}

func ListLocations(ctx context.Context, db *sql.DB, projectID string, opts ListOptions) ([]contract.AssetLocation, int, error) {
	limit, offset := opts.normalized()
	order, err := locationSortColumn(opts.Sort)
	if err != nil {
		return nil, 0, err
	}
	where := "project_id=$1"
	args := []any{projectID}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND (name ILIKE $%d OR code ILIKE $%d)", len(args), len(args))
	}
	// Allowlisted location filters: kind / status / parent are column conditions,
	// never raw client SQL.
	if opts.LocationKind != "" {
		args = append(args, opts.LocationKind)
		where += fmt.Sprintf(" AND kind=$%d", len(args))
	}
	if opts.LocationStatus != "" {
		args = append(args, opts.LocationStatus)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if opts.LocationParent != "" {
		args = append(args, opts.LocationParent)
		where += fmt.Sprintf(" AND parent_id=$%d", len(args))
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_locations WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+locationColumns+` FROM asset_locations WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.AssetLocation{}
	for rows.Next() {
		v, err := scanLocation(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

func UpdateLocation(ctx context.Context, db *sql.DB, in contract.AssetLocation, expectedVersion int64, actor, requestID string) (contract.AssetLocation, error) {
	if expectedVersion <= 0 {
		return in, ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var projectID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,version FROM asset_locations WHERE id=$1 FOR UPDATE`, in.Id).Scan(&projectID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrNotFound
		}
		return in, err
	}
	if version != expectedVersion {
		return in, ErrVersionConflict
	}
	var code any
	if in.Code != nil && strings.TrimSpace(*in.Code) != "" {
		code = strings.TrimSpace(*in.Code)
	} else {
		code = nil
	}
	if in.CoordinateSystem == "" {
		in.CoordinateSystem = "UNKNOWN"
	}
	if in.CoordinateSource == "" {
		in.CoordinateSource = "MANUAL"
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_locations SET parent_id=$2,kind=$3,code=$4,name=$5,address=$6,province=$7,city=$8,county=$9,longitude=$10,latitude=$11,coordinate_system=$12,coordinate_source=$13,version=version+1,updated_at=now() WHERE id=$1 AND version=$14`,
		in.Id, in.ParentId, in.Kind, code, in.Name, in.Address, in.Province, in.City, in.County, in.Longitude, in.Latitude, in.CoordinateSystem, in.CoordinateSource, expectedVersion)
	if err != nil {
		return in, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return in, ErrVersionConflict
	}
	in.Version = version + 1
	in.ProjectId = projectID
	if err := revision(ctx, tx, projectID, "", "location", in.Id, actor, requestID, []string{"updated"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "location_updated", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

// DisableLocation disables a location rather than deleting it. Historical
// relationships are preserved.
func DisableLocation(ctx context.Context, db *sql.DB, locationID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,version FROM asset_locations WHERE id=$1 FOR UPDATE`, locationID).Scan(&projectID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_locations SET status='DISABLED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, locationID, expectedVersion)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrVersionConflict
	}
	var snapshot contract.AssetLocation
	if err := tx.QueryRowContext(ctx, `SELECT `+locationColumns+` FROM asset_locations WHERE id=$1`, locationID).Scan(&snapshot.Id, &snapshot.ProjectId, &snapshot.ParentId, &snapshot.Kind, &snapshot.Code, &snapshot.Name, &snapshot.Address, &snapshot.Province, &snapshot.City, &snapshot.County, &snapshot.Longitude, &snapshot.Latitude, &snapshot.CoordinateSystem, &snapshot.CoordinateSource, &snapshot.Status, &snapshot.Version); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID, "", "location", locationID, actor, requestID, []string{"disabled"}, snapshot); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "location_disabled", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Manufacturer. project_id IS NULL means GLOBAL (read-only through business HTTP).
// ---------------------------------------------------------------------------
const manufacturerColumns = `id,project_id,name,short_name,country_region,website,status,version`

func scanManufacturer(row interface{ Scan(...any) error }) (contract.Manufacturer, error) {
	var v contract.Manufacturer
	err := row.Scan(&v.Id, &v.ProjectId, &v.Name, &v.ShortName, &v.CountryRegion, &v.Website, &v.Status, &v.Version)
	return v, err
}

// CreateManufacturer creates a GLOBAL (project_id NULL) or project-scoped
// manufacturer. GLOBAL records are read-only through the business HTTP layer.
func CreateManufacturer(ctx context.Context, db *sql.DB, in contract.ManufacturerInput, actor, requestID string) (contract.Manufacturer, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return contract.Manufacturer{}, err
	}
	defer tx.Rollback()
	out := contract.Manufacturer{Id: platform.ID(), ProjectId: in.ProjectId, Name: in.Name, ShortName: in.ShortName, CountryRegion: in.CountryRegion, Website: in.Website, Status: "ACTIVE", Version: 1}
	if out.ShortName == "" {
		out.ShortName = in.Name
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO manufacturers(id,project_id,name,short_name,country_region,website,status,version) VALUES($1,$2,$3,$4,$5,$6,'ACTIVE',1)`, out.Id, out.ProjectId, out.Name, out.ShortName, out.CountryRegion, out.Website); err != nil {
		return out, err
	}
	if out.ProjectId != nil && *out.ProjectId != "" {
		if err := revision(ctx, tx, *out.ProjectId, "", "manufacturer", out.Id, actor, requestID, []string{"created"}, out); err != nil {
			return out, err
		}
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: strOrEmpty(out.ProjectId), Action: "manufacturer_created", Result: "success", RequestID: requestID}); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func GetManufacturer(ctx context.Context, db *sql.DB, id string) (contract.Manufacturer, error) {
	v, err := scanManufacturer(db.QueryRowContext(ctx, `SELECT `+manufacturerColumns+` FROM manufacturers WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func ListManufacturers(ctx context.Context, db *sql.DB, projectID string, opts ListOptions) ([]contract.Manufacturer, int, error) {
	limit, offset := opts.normalized()
	order := "name ASC"
	if strings.HasSuffix(opts.Sort, ":desc") {
		order = "name DESC"
	}
	where := "(project_id IS NULL OR project_id=$1)"
	args := []any{projectID}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manufacturers WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+manufacturerColumns+` FROM manufacturers WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.Manufacturer{}
	for rows.Next() {
		v, err := scanManufacturer(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

func UpdateManufacturer(ctx context.Context, db *sql.DB, in contract.Manufacturer, expectedVersion int64, actor, requestID string) (contract.Manufacturer, error) {
	if expectedVersion <= 0 {
		return in, ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var projectID sql.NullString
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,version FROM manufacturers WHERE id=$1 FOR UPDATE`, in.Id).Scan(&projectID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrNotFound
		}
		return in, err
	}
	if !projectID.Valid {
		return in, ErrGlobalReadOnly
	}
	if version != expectedVersion {
		return in, ErrVersionConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE manufacturers SET name=$2,short_name=$3,country_region=$4,website=$5,version=version+1,updated_at=now() WHERE id=$1 AND version=$6`, in.Id, in.Name, in.ShortName, in.CountryRegion, in.Website, expectedVersion)
	if err != nil {
		return in, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return in, ErrVersionConflict
	}
	in.Version = version + 1
	if err := revision(ctx, tx, projectID.String, "", "manufacturer", in.Id, actor, requestID, []string{"updated"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID.String, Action: "manufacturer_updated", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func DisableManufacturer(ctx context.Context, db *sql.DB, manufacturerID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID sql.NullString
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,version FROM manufacturers WHERE id=$1 FOR UPDATE`, manufacturerID).Scan(&projectID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !projectID.Valid {
		return ErrGlobalReadOnly
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE manufacturers SET status='DISABLED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, manufacturerID, expectedVersion); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID.String, "", "manufacturer", manufacturerID, actor, requestID, []string{"disabled"}, map[string]string{"id": manufacturerID, "status": "DISABLED"}); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID.String, Action: "manufacturer_disabled", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Equipment Catalog. project_id IS NULL means GLOBAL (read-only through business HTTP).
// ---------------------------------------------------------------------------
const catalogColumns = `id,project_id,name,type_code,manufacturer_id,brand,model,product_code,spec_schema_version,status,version`

func scanCatalog(row interface{ Scan(...any) error }) (contract.EquipmentCatalogItem, error) {
	var v contract.EquipmentCatalogItem
	err := row.Scan(&v.Id, &v.ProjectId, &v.Name, &v.TypeCode, &v.ManufacturerId, &v.Brand, &v.Model, &v.ProductCode, &v.SpecSchemaVersion, &v.Status, &v.Version)
	return v, err
}

func CreateCatalogItem(ctx context.Context, db *sql.DB, in contract.EquipmentCatalogItem, actor, requestID string) (contract.EquipmentCatalogItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if in.Id == "" {
		in.Id = platform.ID()
	}
	in.Version = 1
	if in.Status == "" {
		in.Status = "ACTIVE"
	}
	if in.SpecSchemaVersion == "" {
		in.SpecSchemaVersion = equipment.SpecSchemaVersion
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO equipment_catalog_items(id,project_id,name,type_code,manufacturer_id,brand,model,product_code,spec_schema_version,status,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		in.Id, in.ProjectId, in.Name, in.TypeCode, in.ManufacturerId, in.Brand, in.Model, in.ProductCode, in.SpecSchemaVersion, in.Status, in.Version); err != nil {
		return in, err
	}
	if in.ProjectId != nil && *in.ProjectId != "" {
		if err := revision(ctx, tx, *in.ProjectId, "", "catalog", in.Id, actor, requestID, []string{"created"}, in); err != nil {
			return in, err
		}
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: strOrEmpty(in.ProjectId), Action: "catalog_created", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func GetCatalogItem(ctx context.Context, db *sql.DB, id string) (contract.EquipmentCatalogItem, error) {
	v, err := scanCatalog(db.QueryRowContext(ctx, `SELECT `+catalogColumns+` FROM equipment_catalog_items WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func ListCatalogItems(ctx context.Context, db *sql.DB, projectID string, opts ListOptions) ([]contract.EquipmentCatalogItem, int, error) {
	limit, offset := opts.normalized()
	order := "name ASC"
	if strings.HasSuffix(opts.Sort, ":desc") {
		order = "name DESC"
	}
	where := "(project_id IS NULL OR project_id=$1)"
	args := []any{projectID}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM equipment_catalog_items WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+catalogColumns+` FROM equipment_catalog_items WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.EquipmentCatalogItem{}
	for rows.Next() {
		v, err := scanCatalog(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

func UpdateCatalogItem(ctx context.Context, db *sql.DB, in contract.EquipmentCatalogItem, expectedVersion int64, actor, requestID string) (contract.EquipmentCatalogItem, error) {
	if expectedVersion <= 0 {
		return in, ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var projectID sql.NullString
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,version FROM equipment_catalog_items WHERE id=$1 FOR UPDATE`, in.Id).Scan(&projectID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrNotFound
		}
		return in, err
	}
	if !projectID.Valid {
		return in, ErrGlobalReadOnly
	}
	if version != expectedVersion {
		return in, ErrVersionConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE equipment_catalog_items SET name=$2,type_code=$3,manufacturer_id=$4,brand=$5,model=$6,product_code=$7,spec_schema_version=$8,version=version+1,updated_at=now() WHERE id=$1 AND version=$9`,
		in.Id, in.Name, in.TypeCode, in.ManufacturerId, in.Brand, in.Model, in.ProductCode, in.SpecSchemaVersion, expectedVersion)
	if err != nil {
		return in, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return in, ErrVersionConflict
	}
	in.Version = version + 1
	if err := revision(ctx, tx, projectID.String, "", "catalog", in.Id, actor, requestID, []string{"updated"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID.String, Action: "catalog_updated", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func DisableCatalogItem(ctx context.Context, db *sql.DB, catalogID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID sql.NullString
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id,version FROM equipment_catalog_items WHERE id=$1 FOR UPDATE`, catalogID).Scan(&projectID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !projectID.Valid {
		return ErrGlobalReadOnly
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE equipment_catalog_items SET status='DISABLED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, catalogID, expectedVersion); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID.String, "", "catalog", catalogID, actor, requestID, []string{"disabled"}, map[string]string{"id": catalogID, "status": "DISABLED"}); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID.String, Action: "catalog_disabled", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Equipment installation.
// ---------------------------------------------------------------------------
const equipmentColumns = `id,asset_id,equipment_code,type_code,catalog_item_id,serial_number,semantic_slot,position_label,criticality,administrative_state,version`

func scanEquipment(row interface{ Scan(...any) error }) (contract.EquipmentInstallation, error) {
	var v contract.EquipmentInstallation
	err := row.Scan(&v.Id, &v.AssetId, &v.EquipmentCode, &v.TypeCode, &v.CatalogItemId, &v.SerialNumber, &v.SemanticSlot, &v.PositionLabel, &v.Criticality, &v.AdministrativeState, &v.Version)
	return v, err
}

// resolveSpecSchemaVersion derives the spec schema version from the catalog item
// when present, otherwise from the registered equipment type. It never hardcodes
// "1".
func resolveSpecSchemaVersion(ctx context.Context, tx *sql.Tx, typeCode string, catalogItemID *string) (string, error) {
	if catalogItemID != nil && *catalogItemID != "" {
		var v string
		if err := tx.QueryRowContext(ctx, `SELECT spec_schema_version FROM equipment_catalog_items WHERE id=$1`, *catalogItemID).Scan(&v); err != nil {
			return "", fmt.Errorf("catalog spec version: %w", err)
		}
		return v, nil
	}
	var v string
	if err := tx.QueryRowContext(ctx, `SELECT spec_schema_version FROM equipment_types WHERE type_code=$1`, typeCode).Scan(&v); err != nil {
		return "", fmt.Errorf("equipment type spec version: %w", err)
	}
	return v, nil
}

// CreateEquipment installs an equipment instance on an asset, validating the
// type-specific specification against the resolved versioned schema.
func CreateEquipment(ctx context.Context, db *sql.DB, in contract.EquipmentInstallation, spec json.RawMessage, actor, requestID string) (contract.EquipmentInstallation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	schemaVersion, err := resolveSpecSchemaVersion(ctx, tx, in.TypeCode, in.CatalogItemId)
	if err != nil {
		return in, err
	}
	if err := equipment.ValidateSpec(in.TypeCode, schemaVersion, spec); err != nil {
		return in, fmt.Errorf("equipment spec: %w", err)
	}
	if in.Id == "" {
		in.Id = platform.ID()
	}
	in.Version = 1
	if in.AdministrativeState == "" {
		in.AdministrativeState = "IN_SERVICE"
	}
	if in.Criticality == "" {
		in.Criticality = "MEDIUM"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_equipment_installations(id,asset_id,equipment_code,type_code,catalog_item_id,manufacturer_snapshot,brand_snapshot,model_snapshot,serial_number,semantic_slot,position_label,criticality,administrative_state,valid_from,version) VALUES($1,$2,$3,$4,$5,'','','',$6,$7,$8,$9,$10,now(),$11)`,
		in.Id, in.AssetId, in.EquipmentCode, in.TypeCode, in.CatalogItemId, in.SerialNumber, in.SemanticSlot, in.PositionLabel, in.Criticality, in.AdministrativeState, in.Version)
	if err != nil {
		return in, err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, in.AssetId).Scan(&projectID); err != nil {
		return in, err
	}
	if err := revision(ctx, tx, projectID, in.AssetId, "equipment", in.Id, actor, requestID, []string{"created"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "equipment_created", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func GetEquipment(ctx context.Context, db *sql.DB, id string) (contract.EquipmentInstallation, error) {
	v, err := scanEquipment(db.QueryRowContext(ctx, `SELECT `+equipmentColumns+` FROM asset_equipment_installations WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func ListEquipment(ctx context.Context, db *sql.DB, assetID string, opts ListOptions) ([]contract.EquipmentInstallation, int, error) {
	limit, offset := opts.normalized()
	order := "equipment_code ASC"
	if strings.HasSuffix(opts.Sort, ":desc") {
		order = "equipment_code DESC"
	}
	where := "asset_id=$1"
	args := []any{assetID}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND equipment_code ILIKE $%d", len(args))
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_equipment_installations WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+equipmentColumns+` FROM asset_equipment_installations WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.EquipmentInstallation{}
	for rows.Next() {
		v, err := scanEquipment(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

func UpdateEquipment(ctx context.Context, db *sql.DB, in contract.EquipmentInstallation, expectedVersion int64, actor, requestID string) (contract.EquipmentInstallation, error) {
	if expectedVersion <= 0 {
		return in, ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var assetID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT asset_id,version FROM asset_equipment_installations WHERE id=$1 FOR UPDATE`, in.Id).Scan(&assetID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrNotFound
		}
		return in, err
	}
	if version != expectedVersion {
		return in, ErrVersionConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_equipment_installations SET equipment_code=$2,type_code=$3,catalog_item_id=$4,serial_number=$5,semantic_slot=$6,position_label=$7,criticality=$8,administrative_state=$9,version=version+1,updated_at=now() WHERE id=$1 AND version=$10`,
		in.Id, in.EquipmentCode, in.TypeCode, in.CatalogItemId, in.SerialNumber, in.SemanticSlot, in.PositionLabel, in.Criticality, in.AdministrativeState, expectedVersion)
	if err != nil {
		return in, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return in, ErrVersionConflict
	}
	in.Version = version + 1
	in.AssetId = assetID
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, assetID).Scan(&projectID); err != nil {
		return in, err
	}
	if err := revision(ctx, tx, projectID, assetID, "equipment", in.Id, actor, requestID, []string{"updated"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "equipment_updated", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

// RemoveEquipment closes an equipment installation's validity interval; it never
// hard-deletes and is refused while an active runtime binding exists.
func RemoveEquipment(ctx context.Context, db *sql.DB, equipmentID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var assetID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT asset_id,version FROM asset_equipment_installations WHERE id=$1 FOR UPDATE`, equipmentID).Scan(&assetID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	var bindingCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM equipment_runtime_bindings WHERE equipment_installation_id=$1 AND valid_to IS NULL`, equipmentID).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return ErrActiveBinding
	}
	if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment_installations SET valid_to=now(),removed_at=now(),administrative_state='OUT_OF_SERVICE',version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, equipmentID, expectedVersion); err != nil {
		return err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, assetID).Scan(&projectID); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID, assetID, "equipment", equipmentID, actor, requestID, []string{"removed"}, map[string]string{"id": equipmentID, "state": "OUT_OF_SERVICE"}); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "equipment_removed", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Equipment relations.
// ---------------------------------------------------------------------------
const relationColumns = `id,asset_id,from_equipment_installation_id,to_equipment_installation_id,relation_type,source,notes,version`

func scanRelation(row interface{ Scan(...any) error }) (contract.EquipmentRelation, error) {
	var v contract.EquipmentRelation
	err := row.Scan(&v.Id, &v.AssetId, &v.FromEquipmentInstallationId, &v.ToEquipmentInstallationId, &v.RelationType, &v.Source, &v.Notes, &v.Version)
	return v, err
}

// CreateRelation creates an equipment relation; both ends must belong to the
// same asset (the DB composite FK enforces this too).
func CreateRelation(ctx context.Context, db *sql.DB, in contract.EquipmentRelation, actor, requestID string) (contract.EquipmentRelation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if in.Id == "" {
		in.Id = platform.ID()
	}
	in.Version = 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO equipment_relations(id,asset_id,from_equipment_installation_id,to_equipment_installation_id,relation_type,valid_from,source,notes,version) VALUES($1,$2,$3,$4,$5,now(),$6,$7,$8)`,
		in.Id, in.AssetId, in.FromEquipmentInstallationId, in.ToEquipmentInstallationId, in.RelationType, in.Source, in.Notes, in.Version); err != nil {
		return in, err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, in.AssetId).Scan(&projectID); err != nil {
		return in, err
	}
	if err := revision(ctx, tx, projectID, in.AssetId, "relation", in.Id, actor, requestID, []string{"created"}, in); err != nil {
		return in, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "relation_created", Result: "success", RequestID: requestID}); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func ListRelations(ctx context.Context, db *sql.DB, assetID string, opts ListOptions) ([]contract.EquipmentRelation, int, error) {
	limit, offset := opts.normalized()
	where := "asset_id=$1"
	args := []any{assetID}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM equipment_relations WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+relationColumns+` FROM equipment_relations WHERE `+where+` ORDER BY created_at DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.EquipmentRelation{}
	for rows.Next() {
		v, err := scanRelation(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

// RemoveRelation closes an equipment relation's validity interval; it never
// hard-deletes.
func RemoveRelation(ctx context.Context, db *sql.DB, relationID string, expectedVersion int64, actor, requestID string) error {
	if expectedVersion <= 0 {
		return ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var assetID string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT asset_id,version FROM equipment_relations WHERE id=$1 FOR UPDATE`, relationID).Scan(&assetID, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE equipment_relations SET valid_to=now(),version=version+1 WHERE id=$1 AND version=$2`, relationID, expectedVersion); err != nil {
		return err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, assetID).Scan(&projectID); err != nil {
		return err
	}
	if err := revision(ctx, tx, projectID, assetID, "relation", relationID, actor, requestID, []string{"removed"}, map[string]string{"id": relationID, "removed": "true"}); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: projectID, Action: "relation_removed", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string { return &s }

// ListAssetsScoped lists assets across the caller's readable projects in one
// bounded query (no per-entity N+1). A nil projectIDs means no project filter
// (development mode); a non-nil empty list returns empty.
func ListAssetsScoped(ctx context.Context, db *sql.DB, projectIDs []string, opts ListOptions) ([]contract.AssetMasterData, int, error) {
	if projectIDs != nil && len(projectIDs) == 0 {
		return []contract.AssetMasterData{}, 0, nil
	}
	limit, offset := opts.normalized()
	order, err := assetSortColumn(opts.Sort)
	if err != nil {
		return nil, 0, err
	}
	where := "1=1"
	args := []any{}
	if projectIDs != nil {
		where = "project_id = ANY($1)"
		args = append(args, projectIDs)
	}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND (display_name ILIKE $%d OR asset_code ILIKE $%d)", len(args), len(args))
	}
	where, args = assetFilters(where, args, opts)
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM asset_devices WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+assetColumns+` FROM asset_devices WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.AssetMasterData{}
	for rows.Next() {
		v, err := scanAsset(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

// ListManufacturersScoped lists global + project-scoped manufacturers visible to
// the caller's projects in one bounded query. nil projectIDs means no filter.
func ListManufacturersScoped(ctx context.Context, db *sql.DB, projectIDs []string, opts ListOptions) ([]contract.Manufacturer, int, error) {
	limit, offset := opts.normalized()
	order := "name ASC"
	if strings.HasSuffix(opts.Sort, ":desc") {
		order = "name DESC"
	}
	where := "1=1"
	args := []any{}
	if projectIDs != nil {
		where = "(project_id IS NULL"
		if len(projectIDs) > 0 {
			where += " OR project_id = ANY($1)"
			args = append(args, projectIDs)
		}
		where += ")"
	}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manufacturers WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+manufacturerColumns+` FROM manufacturers WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.Manufacturer{}
	for rows.Next() {
		v, err := scanManufacturer(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}

// ListCatalogItemsScoped lists global + project-scoped catalog items visible to
// the caller's projects in one bounded query. nil projectIDs means no filter.
func ListCatalogItemsScoped(ctx context.Context, db *sql.DB, projectIDs []string, opts ListOptions) ([]contract.EquipmentCatalogItem, int, error) {
	limit, offset := opts.normalized()
	order := "name ASC"
	if strings.HasSuffix(opts.Sort, ":desc") {
		order = "name DESC"
	}
	where := "1=1"
	args := []any{}
	if projectIDs != nil {
		where = "(project_id IS NULL"
		if len(projectIDs) > 0 {
			where += " OR project_id = ANY($1)"
			args = append(args, projectIDs)
		}
		where += ")"
	}
	if strings.TrimSpace(opts.Q) != "" {
		args = append(args, "%"+opts.Q+"%")
		where += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM equipment_catalog_items WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+catalogColumns+` FROM equipment_catalog_items WHERE `+where+` ORDER BY `+order+` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []contract.EquipmentCatalogItem{}
	for rows.Next() {
		v, err := scanCatalog(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
	}
	return values, total, rows.Err()
}
