package asset

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// Once an asset is RETIRED, ordinary child-resource writes (equipment update,
// relation create) must be refused at the transaction layer — no version bump, no
// success audit, no new revision. History correction is via termination, not via
// ordinary update/create.

func TestRetiredAssetDeniesEquipmentUpdate(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	// Retire without any active runtime binding (the existing rule).
	if err := RetireAsset(ctx, db, asset.Id, asset.Version, "actor", "req-2"); err != nil {
		t.Fatal(err)
	}
	// Ordinary update of the still-installed equipment must be refused.
	update := eq
	update.Criticality = "HIGH"
	if _, err := UpdateEquipment(ctx, db, update, eq.Version, "actor", "req-3"); !errors.Is(err, ErrRetired) {
		t.Fatalf("update on retired asset = %v, want ErrRetired", err)
	}
	var version int64
	if err := db.QueryRow(`SELECT version FROM asset_equipment_installations WHERE id=$1`, eq.Id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != eq.Version {
		t.Fatalf("denied update bumped equipment version to %d, want %d", version, eq.Version)
	}
	var audits, revisions int
	_ = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='equipment_updated' AND result='success' AND request_id='req-3'`).Scan(&audits)
	_ = db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1 AND request_id='req-3'`, eq.Id).Scan(&revisions)
	if audits != 0 || revisions != 0 {
		t.Fatalf("denied update produced audits=%d revisions=%d, want 0/0", audits, revisions)
	}
}

func TestRetiredAssetDeniesRelationCreate(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq1, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")
	eq2, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-2"), motorSpec, "actor", "req-2", "")
	if err := RetireAsset(ctx, db, asset.Id, asset.Version, "actor", "req-3"); err != nil {
		t.Fatal(err)
	}
	rel := contract.EquipmentRelation{AssetId: asset.Id, FromEquipmentInstallationId: eq1.Id, ToEquipmentInstallationId: eq2.Id, RelationType: "DRIVES"}
	if _, err := CreateRelation(ctx, db, rel, "actor", "req-4"); !errors.Is(err, ErrRetired) {
		t.Fatalf("create relation on retired asset = %v, want ErrRetired", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM equipment_relations WHERE asset_id=$1`, asset.Id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("relation rows on retired asset = %d (err=%v), want 0", count, err)
	}
}

func TestRemoveRelationDoubleTerminateRefused(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq1, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")
	eq2, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-2"), motorSpec, "actor", "req-2", "")
	rel, err := CreateRelation(ctx, db, contract.EquipmentRelation{AssetId: asset.Id, FromEquipmentInstallationId: eq1.Id, ToEquipmentInstallationId: eq2.Id, RelationType: "DRIVES"}, "actor", "req-3")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveRelation(ctx, db, rel.Id, rel.Version, "actor", "req-4"); err != nil {
		t.Fatal(err)
	}
	// Read the post-remove version and valid_to; the second termination (using the
	// latest version) must be refused without changing anything.
	var version int64
	var validTo1 sql.NullTime
	if err := db.QueryRow(`SELECT version, valid_to FROM equipment_relations WHERE id=$1`, rel.Id).Scan(&version, &validTo1); err != nil {
		t.Fatal(err)
	}
	if !validTo1.Valid {
		t.Fatal("valid_to missing after first termination")
	}
	if err := RemoveRelation(ctx, db, rel.Id, version, "actor", "req-5"); !errors.Is(err, ErrRemoved) {
		t.Fatalf("second termination = %v, want ErrRemoved", err)
	}
	var validTo2 sql.NullTime
	if err := db.QueryRow(`SELECT valid_to FROM equipment_relations WHERE id=$1`, rel.Id).Scan(&validTo2); err != nil {
		t.Fatal(err)
	}
	if !validTo2.Valid || !validTo2.Time.Equal(validTo1.Time) {
		t.Fatalf("valid_to changed on refused second termination: %v -> %v", validTo1, validTo2)
	}
	var audits, req5Revisions int
	_ = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='relation_removed' AND result='success'`).Scan(&audits)
	_ = db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='relation' AND entity_id=$1 AND request_id='req-5'`, rel.Id).Scan(&req5Revisions)
	if audits != 1 {
		t.Fatalf("relation_removed success audits = %d, want 1", audits)
	}
	if req5Revisions != 0 {
		t.Fatalf("refused second termination produced %d revisions", req5Revisions)
	}
}
