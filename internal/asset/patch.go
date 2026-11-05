package asset

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
)

// Field is a presence-aware JSON value for PATCH: it distinguishes an omitted
// key (Set=false), an explicit null (Set=true, Null=true) and a value.
type Field[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (f *Field[T]) UnmarshalJSON(b []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		f.Null = true
		return nil
	}
	return json.Unmarshal(b, &f.Value)
}

// AssetPatch is the HTTP PATCH body for an asset. Only BUSINESS_OWNED fields are
// writable. id / projectId / version are accepted solely as read-only echoes so
// that clients sending a whole object keep working: if present they must equal
// the stored id, the stored project, and the If-Match version. Any other key is
// rejected by the strict decoder (400).
type AssetPatch struct {
	Id      Field[string] `json:"id"`
	Project Field[string] `json:"projectId"`
	Version Field[int64]  `json:"version"`

	DisplayName          Field[string]  `json:"displayName"`
	AssetCode            Field[string]  `json:"assetCode"`
	AssetType            Field[string]  `json:"assetType"`
	AssetStatus          Field[string]  `json:"assetStatus"`
	ManufacturerId       Field[string]  `json:"manufacturerId"`
	ManufacturerSnapshot Field[string]  `json:"manufacturerSnapshot"`
	BrandSnapshot        Field[string]  `json:"brandSnapshot"`
	ModelSnapshot        Field[string]  `json:"modelSnapshot"`
	SerialNumber         Field[string]  `json:"serialNumber"`
	ManufactureDate      Field[string]  `json:"manufactureDate"`
	InstallationDate     Field[string]  `json:"installationDate"`
	CommissioningDate    Field[string]  `json:"commissioningDate"`
	LiftingHeightM       Field[float64] `json:"liftingHeightM"`
	ApplicationType      Field[string]  `json:"applicationType"`
	ControlMode          Field[string]  `json:"controlMode"`
	SpecialEquipmentCode Field[string]  `json:"specialEquipmentCode"`
	UseRegistrationNo    Field[string]  `json:"useRegistrationNo"`
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, args...)...)
}

func maxRunes(field, value string, limit int) error {
	if utf8.RuneCountInString(value) > limit {
		return invalid("%s exceeds %d characters", field, limit)
	}
	return nil
}

// requiredText applies a non-nullable text field: null or blank is a 422.
func requiredText(name string, f Field[string], limit int, dst *string, changed *[]string) error {
	if !f.Set {
		return nil
	}
	v := strings.TrimSpace(f.Value)
	if f.Null || v == "" {
		return invalid("%s is required and cannot be cleared", name)
	}
	if err := maxRunes(name, v, limit); err != nil {
		return err
	}
	*dst = v
	*changed = append(*changed, name)
	return nil
}

// plainText applies a non-nullable text field that may be empty (snapshots).
func plainText(name string, f Field[string], limit int, dst *string, changed *[]string) error {
	if !f.Set {
		return nil
	}
	if f.Null {
		return invalid("%s cannot be null", name)
	}
	v := strings.TrimSpace(f.Value)
	if err := maxRunes(name, v, limit); err != nil {
		return err
	}
	*dst = v
	*changed = append(*changed, name)
	return nil
}

// nullableText applies a nullable text field: null or blank clears it.
func nullableText(name string, f Field[string], limit int, dst **string, changed *[]string) error {
	if !f.Set {
		return nil
	}
	*changed = append(*changed, name)
	v := strings.TrimSpace(f.Value)
	if f.Null || v == "" {
		*dst = nil
		return nil
	}
	if err := maxRunes(name, v, limit); err != nil {
		return err
	}
	*dst = &v
	return nil
}

// nullableDate applies a nullable YYYY-MM-DD date.
func nullableDate(name string, f Field[string], dst **string, changed *[]string) error {
	if !f.Set {
		return nil
	}
	*changed = append(*changed, name)
	v := strings.TrimSpace(f.Value)
	if f.Null || v == "" {
		*dst = nil
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return invalid("%s must be a YYYY-MM-DD date", name)
	}
	*dst = &v
	return nil
}

// apply merges the provided patch fields into current, validating each. It
// returns the names of the fields that were provided.
func (p AssetPatch) apply(current *contract.AssetMasterData) ([]string, error) {
	changed := []string{}
	if err := requiredText("displayName", p.DisplayName, maxDisplayNameRunes, &current.DisplayName, &changed); err != nil {
		return nil, err
	}
	if err := nullableText("assetCode", p.AssetCode, 160, &current.AssetCode, &changed); err != nil {
		return nil, err
	}
	if err := requiredText("assetType", p.AssetType, 80, &current.AssetType, &changed); err != nil {
		return nil, err
	}
	if p.AssetStatus.Set {
		if p.AssetStatus.Null {
			return nil, invalid("assetStatus is required and cannot be cleared")
		}
		switch p.AssetStatus.Value {
		case "DRAFT", "ACTIVE", "OUT_OF_SERVICE":
			if p.AssetStatus.Value != current.AssetStatus {
				changed = append(changed, "assetStatus")
			}
			current.AssetStatus = p.AssetStatus.Value
		case "RETIRED":
			if current.AssetStatus != "RETIRED" {
				// Retirement goes through the dedicated retire flow (binding checks).
				return nil, ErrTerminalStatus
			}
		default:
			return nil, invalid("assetStatus %q is not a valid status", p.AssetStatus.Value)
		}
	}
	if err := nullableText("manufacturerId", p.ManufacturerId, 80, &current.ManufacturerId, &changed); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		name  string
		field Field[string]
		dst   *string
	}{
		{"manufacturerSnapshot", p.ManufacturerSnapshot, &current.ManufacturerSnapshot},
		{"brandSnapshot", p.BrandSnapshot, &current.BrandSnapshot},
		{"modelSnapshot", p.ModelSnapshot, &current.ModelSnapshot},
	} {
		if err := plainText(f.name, f.field, 256, f.dst, &changed); err != nil {
			return nil, err
		}
	}
	for _, f := range []struct {
		name  string
		field Field[string]
		limit int
		dst   **string
	}{
		{"serialNumber", p.SerialNumber, 256, &current.SerialNumber},
		{"applicationType", p.ApplicationType, 80, &current.ApplicationType},
		{"controlMode", p.ControlMode, 80, &current.ControlMode},
		{"specialEquipmentCode", p.SpecialEquipmentCode, 160, &current.SpecialEquipmentCode},
		{"useRegistrationNo", p.UseRegistrationNo, 160, &current.UseRegistrationNo},
	} {
		if err := nullableText(f.name, f.field, f.limit, f.dst, &changed); err != nil {
			return nil, err
		}
	}
	for _, f := range []struct {
		name  string
		field Field[string]
		dst   **string
	}{
		{"manufactureDate", p.ManufactureDate, &current.ManufactureDate},
		{"installationDate", p.InstallationDate, &current.InstallationDate},
		{"commissioningDate", p.CommissioningDate, &current.CommissioningDate},
	} {
		if err := nullableDate(f.name, f.field, f.dst, &changed); err != nil {
			return nil, err
		}
	}
	if p.LiftingHeightM.Set {
		changed = append(changed, "liftingHeightM")
		if p.LiftingHeightM.Null {
			current.LiftingHeightM = nil
		} else {
			if p.LiftingHeightM.Value < 0 {
				return nil, invalid("liftingHeightM must be >= 0")
			}
			v := p.LiftingHeightM.Value
			current.LiftingHeightM = &v
		}
	}
	return changed, nil
}

func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// diffAsset lists the business fields whose merged value differs from the stored
// row, in a fixed order. nil and "" are different values (a nullable text field
// is stored as NULL when cleared, so a cleared field compares as nil on both
// sides); dates are compared in their stored YYYY-MM-DD form.
func diffAsset(cur, next contract.AssetMasterData) []string {
	out := []string{}
	add := func(name string, same bool) {
		if !same {
			out = append(out, name)
		}
	}
	add("displayName", cur.DisplayName == next.DisplayName)
	add("assetCode", sameStr(cur.AssetCode, next.AssetCode))
	add("assetType", cur.AssetType == next.AssetType)
	add("assetStatus", cur.AssetStatus == next.AssetStatus)
	add("manufacturerId", sameStr(cur.ManufacturerId, next.ManufacturerId))
	add("manufacturerSnapshot", cur.ManufacturerSnapshot == next.ManufacturerSnapshot)
	add("brandSnapshot", cur.BrandSnapshot == next.BrandSnapshot)
	add("modelSnapshot", cur.ModelSnapshot == next.ModelSnapshot)
	add("serialNumber", sameStr(cur.SerialNumber, next.SerialNumber))
	add("applicationType", sameStr(cur.ApplicationType, next.ApplicationType))
	add("controlMode", sameStr(cur.ControlMode, next.ControlMode))
	add("specialEquipmentCode", sameStr(cur.SpecialEquipmentCode, next.SpecialEquipmentCode))
	add("useRegistrationNo", sameStr(cur.UseRegistrationNo, next.UseRegistrationNo))
	add("manufactureDate", sameStr(cur.ManufactureDate, next.ManufactureDate))
	add("installationDate", sameStr(cur.InstallationDate, next.InstallationDate))
	add("commissioningDate", sameStr(cur.CommissioningDate, next.CommissioningDate))
	add("liftingHeightM", sameFloat(cur.LiftingHeightM, next.LiftingHeightM))
	return out
}

// PatchAsset applies a presence-aware partial update. Omitted fields keep their
// stored value; explicit null clears a nullable field; clearing a required field
// is ErrInvalidInput. The row is locked, lifecycle and If-Match are checked, the
// patch is merged and validated, and the merged row is written — all in one
// transaction. The revision snapshot is the stored row after the update, and its
// changed_fields are the fields whose stored value actually changed. If nothing
// changed the call is a no-op (see diffAsset): the current row is returned and no
// version bump, revision or audit is written. Validation (422), lifecycle and
// If-Match (409) checks still run first, so a no-op with a stale version is 409.
func PatchAsset(ctx context.Context, db *sql.DB, id string, patch AssetPatch, expectedVersion int64, actor, requestID string) (contract.AssetMasterData, error) {
	if expectedVersion <= 0 {
		return contract.AssetMasterData{}, ErrExpectedVersion
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return contract.AssetMasterData{}, err
	}
	defer tx.Rollback()
	current, err := scanAsset(tx.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM asset_devices WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.AssetMasterData{}, ErrNotFound
	}
	if err != nil {
		return contract.AssetMasterData{}, err
	}
	if current.AssetStatus == "RETIRED" {
		return current, ErrRetired
	}
	if current.Version != expectedVersion {
		return current, ErrVersionConflict
	}
	// Read-only echoes must not attempt to change identity, ownership or version.
	if patch.Id.Set && (patch.Id.Null || patch.Id.Value != current.Id) {
		return current, invalid("id cannot be changed")
	}
	if patch.Project.Set && (patch.Project.Null || patch.Project.Value != current.ProjectId) {
		return current, invalid("projectId cannot be changed through PATCH")
	}
	if patch.Version.Set && (patch.Version.Null || patch.Version.Value != expectedVersion) {
		return current, invalid("body version must equal the If-Match version")
	}
	merged := current
	if _, err := patch.apply(&merged); err != nil {
		return current, err
	}
	// Whether anything changed is decided by comparing the merged values with the
	// stored row, never by which keys were present. A request that changes
	// nothing ({} or same-value fields) is an idempotent no-op: 200 with the
	// current entity, version/ETag unchanged, no revision and no success audit.
	changed := diffAsset(current, merged)
	if len(changed) == 0 {
		return current, nil
	}
	if merged.ManufacturerId != nil && !sameStr(current.ManufacturerId, merged.ManufacturerId) {
		var mfrProject sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM manufacturers WHERE id=$1`, *merged.ManufacturerId).Scan(&mfrProject); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return current, invalid("manufacturerId does not exist")
			}
			return current, err
		}
		if mfrProject.Valid && mfrProject.String != current.ProjectId {
			return current, ErrCrossProject
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_devices SET asset_code=$2,display_name=$3,asset_type=$4,manufacturer_id=$5,manufacturer_snapshot=$6,brand_snapshot=$7,model_snapshot=$8,serial_number=$9,manufacture_date=$10::date,installation_date=$11::date,commissioning_date=$12::date,lifting_height_m=$13,application_type=$14,control_mode=$15,special_equipment_code=$16,use_registration_no=$17,asset_status=$18,version=version+1,updated_at=now() WHERE id=$1 AND version=$19`,
		id, merged.AssetCode, merged.DisplayName, merged.AssetType, merged.ManufacturerId, merged.ManufacturerSnapshot, merged.BrandSnapshot, merged.ModelSnapshot, merged.SerialNumber, merged.ManufactureDate, merged.InstallationDate, merged.CommissioningDate, merged.LiftingHeightM, merged.ApplicationType, merged.ControlMode, merged.SpecialEquipmentCode, merged.UseRegistrationNo, merged.AssetStatus, expectedVersion)
	if err != nil {
		return current, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return current, ErrVersionConflict
	}
	stored, err := scanAsset(tx.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM asset_devices WHERE id=$1`, id))
	if err != nil {
		return current, err
	}
	if err := revision(ctx, tx, stored.ProjectId, id, "asset", id, actor, requestID, append([]string{"updated"}, changed...), stored); err != nil {
		return current, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: stored.ProjectId, Action: "asset_updated", Result: "success", RequestID: requestID}); err != nil {
		return current, err
	}
	return stored, tx.Commit()
}
