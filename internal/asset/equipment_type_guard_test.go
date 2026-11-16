package asset

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// R6-BIZ-R1 B: an equipment type that is DISABLED in equipment_types must never
// receive a new installation, whether the request names the type directly or
// through an (ACTIVE) catalog item. Existing installations of the type keep
// their history and stay maintainable (update / remove).

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func disableType(t *testing.T, db *sql.DB, code string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE equipment_types SET status='DISABLED', version=version+1, updated_at=now() WHERE type_code=$1`, code); err != nil {
		t.Fatal(err)
	}
}

func TestCreateEquipmentRejectsDisabledType(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	a, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	if err != nil {
		t.Fatal(err)
	}
	existing, err := CreateEquipment(ctx, db, equipmentInput(a.Id, "EQ-OLD"), motorSpec, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	disableType(t, db, "MOTOR")
	before := countRows(t, db, `SELECT count(*) FROM asset_equipment_installations`)
	_, err = CreateEquipment(ctx, db, equipmentInput(a.Id, "EQ-NEW"), motorSpec, "actor", "req-2", "")
	if err == nil {
		t.Fatal("install of a DISABLED equipment type was accepted")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput (type not installable)", err)
	}
	if after := countRows(t, db, `SELECT count(*) FROM asset_equipment_installations`); after != before {
		t.Fatalf("refused install wrote a row: %d → %d", before, after)
	}
	if n := countRows(t, db, `SELECT count(*) FROM audit_logs WHERE request_id='req-2'`); n != 0 {
		t.Fatalf("refused install wrote %d audit rows", n)
	}
	// History of the existing installation is untouched and it stays maintainable.
	existing.Criticality = "HIGH"
	updated, err := UpdateEquipment(ctx, db, existing, existing.Version, "actor", "req-3")
	if err != nil {
		t.Fatalf("existing installation of a disabled type must stay updatable: %v", err)
	}
	if err := RemoveEquipment(ctx, db, updated.Id, updated.Version, "actor", "req-4"); err != nil {
		t.Fatalf("existing installation of a disabled type must stay removable: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1`, existing.Id); n != 3 {
		t.Fatalf("equipment revisions = %d, want 3 (created/updated/removed)", n)
	}
}

func TestCreateEquipmentRejectsDisabledTypeThroughActiveCatalog(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	catalog, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "M-1", TypeCode: "MOTOR"}, "actor", "req-c")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	disableType(t, db, "MOTOR")
	in := equipmentInput(a.Id, "EQ-CAT")
	in.CatalogItemId = &catalog.Id
	if _, err := CreateEquipment(ctx, db, in, motorSpec, "actor", "req-1", ""); err == nil {
		t.Fatal("an ACTIVE catalog item must not reopen a DISABLED equipment type")
	} else if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM asset_equipment_installations WHERE asset_id=$1`, a.Id); n != 0 {
		t.Fatalf("installations = %d, want 0", n)
	}
}

func TestCreateEquipmentRejectsUnregisteredOrMissingType(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	a, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	// 1. Registered in code but absent from equipment_types (the F-02 empty table).
	if _, err := CreateEquipment(ctx, db, equipmentInput(a.Id, "EQ-1"), motorSpec, "actor", "req-1", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("type missing from equipment_types: err = %v, want ErrInvalidInput", err)
	}
	// 2. ACTIVE in equipment_types but unknown to the code registry.
	seedEquipmentType(t, db, "NOT_IN_REGISTRY")
	in := equipmentInput(a.Id, "EQ-2")
	in.TypeCode = "NOT_IN_REGISTRY"
	if _, err := CreateEquipment(ctx, db, in, json.RawMessage(`{}`), "actor", "req-2", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("type outside the code registry: err = %v, want ErrInvalidInput", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM asset_equipment_installations`); n != 0 {
		t.Fatalf("installations = %d, want 0", n)
	}
}

func TestCreateEquipmentCatalogGuards(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedProject(t, db, "Project-B")
	seedEquipmentType(t, db, "MOTOR")
	seedEquipmentType(t, db, "PLC")
	a, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	foreign, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-B"), Name: "B-M", TypeCode: "MOTOR"}, "actor", "req-c1")
	if err != nil {
		t.Fatal(err)
	}
	plc, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "A-PLC", TypeCode: "PLC"}, "actor", "req-c2")
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "A-M-OFF", TypeCode: "MOTOR"}, "actor", "req-c3")
	if err != nil {
		t.Fatal(err)
	}
	if err := DisableCatalogItem(ctx, db, disabled.Id, disabled.Version, "actor", "req-c4"); err != nil {
		t.Fatal(err)
	}
	missing := "no-such-catalog"
	for _, c := range []struct {
		name    string
		catalog *string
		want    error
	}{
		{"cross-project catalog", &foreign.Id, ErrCrossProject},
		{"catalog type differs from instance type", &plc.Id, ErrInvalidInput},
		{"disabled catalog", &disabled.Id, ErrInvalidInput},
		{"unknown catalog", &missing, ErrInvalidInput},
	} {
		in := equipmentInput(a.Id, "EQ-"+c.name)
		in.CatalogItemId = c.catalog
		if _, err := CreateEquipment(ctx, db, in, motorSpec, "actor", "req-x", ""); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM asset_equipment_installations`); n != 0 {
		t.Fatalf("installations = %d, want 0", n)
	}
}

// A type being disabled in a concurrent, not yet committed transaction must not be
// raced: the install waits for the disabling transaction and then refuses. The
// old implementation read the type without a lock and installed against the
// pre-disable snapshot.
func TestConcurrentTypeDisableBlocksInstall(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	a, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")

	disabler, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer disabler.Rollback()
	if _, err := disabler.Exec(`UPDATE equipment_types SET status='DISABLED', version=version+1 WHERE type_code='MOTOR'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := CreateEquipment(ctx, db, equipmentInput(a.Id, "EQ-RACE"), motorSpec, "actor", "req-race", "")
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	blocked := false
	for !blocked && time.Now().Before(deadline) {
		select {
		case err := <-done:
			// Finished while the disabling transaction is still open: it must have refused.
			if err == nil {
				t.Fatal("install ran against the pre-disable snapshot and succeeded")
			}
			t.Fatalf("install finished before the disable committed (err=%v); expected it to wait on the type row lock", err)
		default:
		}
		var waiting int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%equipment_types%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		blocked = waiting > 0
		if !blocked {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("install neither finished nor waited on the equipment type lock")
	}
	if err := disabler.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("install after concurrent disable: err = %v, want ErrInvalidInput", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("install did not finish after the disable committed")
	}
	if n := countRows(t, db, `SELECT count(*) FROM asset_equipment_installations WHERE asset_id=$1`, a.Id); n != 0 {
		t.Fatalf("installations = %d, want 0", n)
	}
}
