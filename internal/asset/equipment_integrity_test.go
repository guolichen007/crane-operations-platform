package asset

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func equipmentInput(assetID, code string) contract.EquipmentInstallation {
	return contract.EquipmentInstallation{AssetId: assetID, EquipmentCode: code, TypeCode: "MOTOR", Criticality: "MEDIUM"}
}

var motorSpec = json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`)

func TestCreateEquipmentIdempotencyRetry(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "ekey-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-2", "ekey-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Id != second.Id || first.Version != second.Version {
		t.Fatalf("idempotent retry returned different result: (%s v%d) vs (%s v%d)", first.Id, first.Version, second.Id, second.Version)
	}
	var installs, revisions, audits int
	if err := db.QueryRow(`SELECT count(*) FROM asset_equipment_installations WHERE asset_id=$1`, asset.Id).Scan(&installs); err != nil || installs != 1 {
		t.Fatalf("idempotent retry inserted %d installs (err=%v)", installs, err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1`, first.Id).Scan(&revisions)
	_ = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='equipment_created' AND project_id='Project-A'`).Scan(&audits)
	if revisions != 1 || audits != 1 {
		t.Fatalf("idempotent retry produced revisions=%d audits=%d, want 1/1", revisions, audits)
	}
}

func TestCreateEquipmentIdempotencyDifferentPayloadRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	if _, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "ekey-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-2"), motorSpec, "actor", "req-2", "ekey-2"); !errors.Is(err, ErrIdempotencyKey) {
		t.Fatalf("different payload under same key = %v, want ErrIdempotencyKey", err)
	}
}

func TestCreateEquipmentOnRetiredAssetRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	if err := RetireAsset(ctx, db, asset.Id, asset.Version, "actor", "req-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-2", ""); !errors.Is(err, ErrRetired) {
		t.Fatalf("install on retired asset = %v, want ErrRetired", err)
	}
}

func TestRemoveEquipmentTerminalAndUpdateGuard(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveEquipment(ctx, db, eq.Id, eq.Version, "actor", "req-2"); err != nil {
		t.Fatal(err)
	}
	// Double removal must be refused, history must stay intact.
	if err := RemoveEquipment(ctx, db, eq.Id, eq.Version, "actor", "req-3"); !errors.Is(err, ErrRemoved) {
		t.Fatalf("second removal = %v, want ErrRemoved", err)
	}
	// A removed equipment cannot be re-activated through a normal update.
	update := eq
	update.AdministrativeState = "IN_SERVICE"
	if _, err := UpdateEquipment(ctx, db, update, eq.Version, "actor", "req-4"); !errors.Is(err, ErrRemoved) {
		t.Fatalf("update removed equipment = %v, want ErrRemoved", err)
	}
	// valid_to / removed_at must not be overwritten by the refused second removal.
	var validTo, removedAt sql.NullTime
	_ = db.QueryRow(`SELECT valid_to, removed_at FROM asset_equipment_installations WHERE id=$1`, eq.Id).Scan(&validTo, &removedAt)
	if !validTo.Valid || !removedAt.Valid {
		t.Fatal("history valid_to/removed_at missing after refused second removal")
	}
}

func TestUpdateEquipmentTypeAndCatalogGuard(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	// Unknown type must be rejected.
	badType := eq
	badType.TypeCode = "NOT_A_TYPE"
	if _, err := UpdateEquipment(ctx, db, badType, eq.Version, "actor", "req-2"); err == nil {
		t.Fatal("update to unregistered type_code must fail")
	}
	// Unknown catalog item must be rejected.
	badCatalog := eq
	missing := "missing-catalog"
	badCatalog.CatalogItemId = &missing
	if _, err := UpdateEquipment(ctx, db, badCatalog, eq.Version, "actor", "req-3"); err == nil {
		t.Fatal("update to unknown catalog item must fail")
	}
}

func TestCreateEquipmentCatalogTypeMismatchRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	seedEquipmentType(t, db, "PLC")
	// A same-project PLC catalog item.
	plcCatalog, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "PLC-1", TypeCode: "PLC"}, "actor", "req-c")
	if err != nil {
		t.Fatal(err)
	}
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	// MOTOR equipment referencing a PLC catalog must be rejected.
	in := equipmentInput(asset.Id, "EQ-1")
	in.CatalogItemId = &plcCatalog.Id
	if _, err := CreateEquipment(ctx, db, in, motorSpec, "actor", "req-1", ""); err == nil {
		t.Fatal("MOTOR equipment with PLC catalog must be rejected")
	}
	// Disabled catalog must not be used for a new install.
	// (disable is project-scoped through DisableCatalogItem)
	if err := DisableCatalogItem(ctx, db, plcCatalog.Id, plcCatalog.Version, "actor", "req-d"); err != nil {
		t.Fatal(err)
	}
	// Same type but now-disabled catalog: still must be rejected.
	in.TypeCode = "PLC"
	in.EquipmentCode = "EQ-2"
	if _, err := CreateEquipment(ctx, db, in, json.RawMessage(`{}`), "actor", "req-2", ""); err == nil {
		t.Fatal("install referencing a disabled catalog must be rejected")
	}
}

func TestUpdateEquipmentCatalogImmutability(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	// Two same-type MOTOR catalogs in the same project: re-linking from one to the
	// other must be refused because the persisted spec was validated against the
	// original catalog's schema.
	catA, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "MOTOR-A", TypeCode: "MOTOR"}, "actor", "req-ca")
	if err != nil {
		t.Fatal(err)
	}
	catB, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "MOTOR-B", TypeCode: "MOTOR"}, "actor", "req-cb")
	if err != nil {
		t.Fatal(err)
	}
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	in := equipmentInput(asset.Id, "EQ-1")
	in.CatalogItemId = &catA.Id
	eq, err := CreateEquipment(ctx, db, in, motorSpec, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}

	// Re-linking to a different same-type catalog must be rejected.
	relink := eq
	relink.CatalogItemId = &catB.Id
	if _, err := UpdateEquipment(ctx, db, relink, eq.Version, "actor", "req-2"); err == nil {
		t.Fatal("re-linking equipment to a different catalog must be rejected")
	}
	// Unlinking the catalog entirely must be rejected too.
	unlink := eq
	unlink.CatalogItemId = nil
	if _, err := UpdateEquipment(ctx, db, unlink, eq.Version, "actor", "req-3"); err == nil {
		t.Fatal("unlinking equipment catalog must be rejected")
	}
	// Keeping the same catalog is allowed for ordinary field edits.
	keep := eq
	keep.Criticality = "HIGH"
	if _, err := UpdateEquipment(ctx, db, keep, eq.Version, "actor", "req-4"); err != nil {
		t.Fatalf("update with unchanged catalog = %v, want nil", err)
	}
	// The created revision snapshot must record the validated spec content.
	var snapshot string
	if err := db.QueryRow(`SELECT snapshot_json::text FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1 ORDER BY revision ASC LIMIT 1`, eq.Id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `"rated_power_kw"`) {
		t.Fatalf("created revision snapshot does not record spec content: %s", snapshot)
	}
}
